package main

import (
	"bytes"
	"runtime"
	"time"

	"github.com/vitor-schipani/everyframe/features"
	"github.com/vitor-schipani/everyframe/gif"
)

// The stages a file goes through. The first two are the work around the
// features: reading the frames out of the file, and allocating the canvas
// and the extractor's own buffers. The rest are the stages of features.Add.
const (
	stageDecode = iota
	stageSetup
	stageFeatures // the first of the features.Stage values
	numStages     = stageFeatures + features.NumStages
)

func stageName(i int) string {
	switch i {
	case stageDecode:
		return "decode"
	case stageSetup:
		return "setup"
	}
	return features.Stage(i - stageFeatures).String()
}

// cost is what a piece of work took.
type cost struct {
	Time time.Duration `json:"time_ns"`
	// AllocBytes and Allocs are the heap memory allocated, live or not.
	AllocBytes int64 `json:"alloc_bytes"`
	Allocs     int64 `json:"allocs"`
}

func (c *cost) add(o cost) {
	c.Time += o.Time
	c.AllocBytes += o.AllocBytes
	c.Allocs += o.Allocs
}

// fileResult is the measurement of one file.
type fileResult struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Frames int    `json:"frames"`
	// Pixels is the number of pixels the frames store.
	Pixels int64 `json:"pixels"`
	// Error is why decoding stopped early, if it did.
	Error string `json:"error,omitempty"`

	// EndToEnd is one call to features.Extract on the file held in memory,
	// measured without the per-stage bookkeeping.
	EndToEnd cost `json:"end_to_end"`
	// PeakHeap is the most the heap grew over what was in use before the
	// file was opened, garbage not yet collected included.
	PeakHeap int64 `json:"peak_heap_bytes"`
	// Stages is indexed by the stage constants.
	Stages [numStages]cost `json:"-"`
}

// measure benchmarks one file. Time is taken runs times and the fastest run
// kept; memory does not vary between runs and is taken once.
func measure(name string, data []byte, runs int) fileResult {
	res := fileResult{Name: name, Bytes: int64(len(data))}

	// End to end: the public entry point, undisturbed.
	var before, after runtime.MemStats
	for i := range runs {
		runtime.GC()
		runtime.ReadMemStats(&before)
		start := time.Now()
		rep, err := extract(data)
		elapsed := time.Since(start)
		runtime.ReadMemStats(&after)
		if i == 0 || elapsed < res.EndToEnd.Time {
			res.EndToEnd.Time = elapsed
		}
		res.EndToEnd.AllocBytes = int64(after.TotalAlloc - before.TotalAlloc)
		res.EndToEnd.Allocs = int64(after.Mallocs - before.Mallocs)
		if rep != nil {
			res.Width, res.Height = rep.Summary.Width, rep.Summary.Height
			res.Frames, res.Pixels = len(rep.Frames), rep.Summary.StoredPixels
		}
		if err != nil {
			res.Error = err.Error()
		}
	}

	// Time per stage: the clock is read each time a stage ends.
	for i := range runs {
		var times [numStages]time.Duration
		var total time.Duration
		runtime.GC()
		last := time.Now()
		stages(data, func(stage int) {
			now := time.Now()
			times[stage] += now.Sub(last)
			total += now.Sub(last)
			last = now
		})
		if i == 0 || total < sumTimes(res.Stages) {
			for s := range times {
				res.Stages[s].Time = times[s]
			}
		}
	}

	// Memory per stage. Reading the allocator's counters stops the
	// program, which is why this is not done while time is measured.
	runtime.GC()
	runtime.ReadMemStats(&before)
	base := before.HeapAlloc
	stages(data, func(stage int) {
		runtime.ReadMemStats(&after)
		res.Stages[stage].AllocBytes += int64(after.TotalAlloc - before.TotalAlloc)
		res.Stages[stage].Allocs += int64(after.Mallocs - before.Mallocs)
		if after.HeapAlloc > base {
			res.PeakHeap = max(res.PeakHeap, int64(after.HeapAlloc-base))
		}
		before, after = after, before
	})
	return res
}

func sumTimes(stages [numStages]cost) time.Duration {
	var total time.Duration
	for _, c := range stages {
		total += c.Time
	}
	return total
}

func extract(data []byte) (*features.Report, error) {
	d, err := gif.NewDecoder(bytes.NewReader(data), gif.Options{})
	if err != nil {
		return nil, err
	}
	return features.Extract(d)
}

// stages does the work of extract, calling done each time a stage ends.
func stages(data []byte, done func(stage int)) {
	d, err := gif.NewDecoder(bytes.NewReader(data), gif.Options{})
	done(stageDecode)
	if err != nil {
		return
	}
	e := features.NewExtractor(d.Header())
	done(stageSetup)
	e.OnStage(func(s features.Stage) { done(stageFeatures + int(s)) })
	// Like features.Extract, keep what was measured for every frame.
	var frames []features.Frame
	for f, err := range d.Frames() {
		done(stageDecode)
		if err != nil {
			break
		}
		frames = append(frames, e.Add(f))
	}
	done(stageDecode)
	_ = e.Summary(d.Metadata())
	done(stageFeatures + int(features.StageSummary))
	runtime.KeepAlive(frames)
}
