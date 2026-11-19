package main

import (
	"fmt"
	"io"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// report is what the tool prints, as text or, with -json, as declared here.
type report struct {
	GeneratedAt string `json:"generated_at"`
	// Source says where the files came from: a corpus tier or "files".
	Source string `json:"source"`
	Go     string `json:"go"`
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	CPUs   int    `json:"cpus"`
	// Runs is how many times each file was timed; the fastest run counts.
	Runs int `json:"runs"`

	Files  int   `json:"files"`
	Bytes  int64 `json:"bytes"`
	Frames int   `json:"frames"`
	Pixels int64 `json:"pixels"`
	// Failed counts files whose decoding stopped early.
	Failed int `json:"failed"`

	// EndToEnd totals the files' end-to-end measurements.
	EndToEnd cost `json:"end_to_end"`
	// PeakHeap is the largest peak of any file.
	PeakHeap int64 `json:"peak_heap_bytes"`
	// PeakRSS is the most physical memory the whole run held, 0 if unknown.
	PeakRSS int64 `json:"peak_rss_bytes"`

	Stages []stageReport `json:"stages"`
	// PerFile is how the files are spread, for time, peak heap and memory
	// allocated.
	PerFile struct {
		Time       spread `json:"time_ns"`
		PeakHeap   spread `json:"peak_heap_bytes"`
		AllocBytes spread `json:"alloc_bytes"`
	} `json:"per_file"`
	Results []fileReport `json:"results"`
}

// stageReport totals one stage over all the files.
type stageReport struct {
	Name string `json:"name"`
	cost
	// Share is the stage's part of the time of all stages, from 0 to 1.
	Share float64 `json:"share"`
}

type fileReport struct {
	fileResult
	Stages []stageReport `json:"stages"`
}

// spread describes a set of values by its percentiles.
type spread struct {
	P50 int64 `json:"p50"`
	P90 int64 `json:"p90"`
	P99 int64 `json:"p99"`
	Max int64 `json:"max"`
}

func newSpread(values []int64) spread {
	if len(values) == 0 {
		return spread{}
	}
	v := append([]int64(nil), values...)
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	// The nearest-rank percentile: the smallest value with at least p% of
	// the values at or below it.
	at := func(p int) int64 { return v[(len(v)*p+99)/100-1] }
	return spread{P50: at(50), P90: at(90), P99: at(99), Max: v[len(v)-1]}
}

func stageReports(stages [numStages]cost) []stageReport {
	total := sumTimes(stages)
	out := make([]stageReport, numStages)
	for i, c := range stages {
		out[i] = stageReport{Name: stageName(i), cost: c}
		if total > 0 {
			out[i].Share = float64(c.Time) / float64(total)
		}
	}
	return out
}

func buildReport(source string, runs int, now time.Time, results []fileResult) *report {
	r := &report{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Source:      source,
		Go:          runtime.Version(),
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		CPUs:        runtime.NumCPU(),
		Runs:        runs,
		Files:       len(results),
		PeakRSS:     peakRSS(),
		Results:     make([]fileReport, len(results)),
	}
	var stages [numStages]cost
	var times, peaks, allocs []int64
	for i, f := range results {
		r.Bytes += f.Bytes
		r.Frames += f.Frames
		r.Pixels += f.Pixels
		if f.Error != "" {
			r.Failed++
		}
		r.EndToEnd.add(f.EndToEnd)
		r.PeakHeap = max(r.PeakHeap, f.PeakHeap)
		for s := range stages {
			stages[s].add(f.Stages[s])
		}
		times = append(times, int64(f.EndToEnd.Time))
		peaks = append(peaks, f.PeakHeap)
		allocs = append(allocs, f.EndToEnd.AllocBytes)
		r.Results[i] = fileReport{fileResult: f, Stages: stageReports(f.Stages)}
	}
	r.Stages = stageReports(stages)
	r.PerFile.Time = newSpread(times)
	r.PerFile.PeakHeap = newSpread(peaks)
	r.PerFile.AllocBytes = newSpread(allocs)
	return r
}

// writeText prints the report for people, listing the top files that took
// the longest and that needed the most memory.
func (r *report) writeText(w io.Writer, top int) error {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	// table prints rows in columns: the first holds names and is aligned
	// left, the others hold figures and are aligned right.
	table := func(fill func(row func(cells ...string))) {
		var rows [][]string
		fill(func(cells ...string) { rows = append(rows, cells) })
		width := make([]int, len(rows[0]))
		for _, r := range rows {
			for i, c := range r {
				width[i] = max(width[i], utf8.RuneCountInString(c))
			}
		}
		for _, r := range rows {
			line := "  " + r[0] + strings.Repeat(" ", width[0]-utf8.RuneCountInString(r[0]))
			for i, c := range r[1:] {
				line += strings.Repeat(" ", 2+width[i+1]-utf8.RuneCountInString(c)) + c
			}
			p("%s\n", strings.TrimRight(line, " "))
		}
	}

	p("everyframe benchmark, %s\n\n", r.GeneratedAt)
	p("  source   %s: %s, %s\n", r.Source, plural(r.Files, "file"), humanBytes(r.Bytes))
	p("  content  %s, %s stored\n", plural(r.Frames, "frame"), humanPixels(r.Pixels))
	if r.Failed > 0 {
		p("  failed   %s stopped decoding early; what was read is included\n", plural(r.Failed, "file"))
	}
	p("  system   %s %s/%s, %s\n", r.Go, r.OS, r.Arch, plural(r.CPUs, "CPU"))
	p("  timing   fastest of %s per file, one file at a time\n", plural(r.Runs, "run"))

	p("\nEnd to end\n\n")
	secs := r.EndToEnd.Time.Seconds()
	p("  time       %s", humanDuration(r.EndToEnd.Time))
	if secs > 0 {
		p("  (%s/s, %.0f frames/s, %s/s)", humanBytes(int64(float64(r.Bytes)/secs)), float64(r.Frames)/secs, humanPixels(int64(float64(r.Pixels)/secs)))
	}
	p("\n  allocated  %s in %s\n", humanBytes(r.EndToEnd.AllocBytes), plural(int(r.EndToEnd.Allocs), "allocation"))
	p("  peak heap  %s for the most demanding file\n", humanBytes(r.PeakHeap))
	if r.PeakRSS > 0 {
		p("  peak RSS   %s for the whole run, files held in memory included\n", humanBytes(r.PeakRSS))
	}

	p("\nPer stage\n\n")
	table(func(row func(...string)) {
		row("STAGE", "TIME", "SHARE", "PER FRAME", "PER MPX", "ALLOCATED", "ALLOCATIONS")
		var total cost
		line := func(name string, c cost, share float64) {
			perFrame, perMpx := "-", "-"
			if r.Frames > 0 {
				perFrame = humanDuration(c.Time / time.Duration(r.Frames))
			}
			if r.Pixels > 0 {
				perMpx = humanDuration(time.Duration(float64(c.Time) * 1e6 / float64(r.Pixels)))
			}
			row(name, humanDuration(c.Time), fmt.Sprintf("%.1f%%", 100*share), perFrame, perMpx,
				humanBytes(c.AllocBytes), fmt.Sprint(c.Allocs))
		}
		for _, s := range r.Stages {
			line(s.Name, s.cost, s.Share)
			total.add(s.cost)
		}
		line("all stages", total, 1)
	})
	p("\n  decode reads the frames out of the file and setup allocates the canvas and\n")
	p("  buffers; the others are the features. change also keeps the hash's\n")
	p("  brightness grid up to date. Stages are timed in a separate pass, so their\n")
	p("  sum differs a little from the end-to-end time.\n")

	p("\nPer file\n\n")
	table(func(row func(...string)) {
		row("", "P50", "P90", "P99", "MAX")
		d := func(n int64) string { return humanDuration(time.Duration(n)) }
		for _, l := range []struct {
			name string
			s    spread
			f    func(int64) string
		}{{"time", r.PerFile.Time, d}, {"peak heap", r.PerFile.PeakHeap, humanBytes}, {"allocated", r.PerFile.AllocBytes, humanBytes}} {
			row(l.name, l.f(l.s.P50), l.f(l.s.P90), l.f(l.s.P99), l.f(l.s.Max))
		}
	})

	ranked := func(title string, less func(a, b *fileReport) bool) {
		if top <= 0 || len(r.Results) == 0 {
			return
		}
		order := make([]*fileReport, len(r.Results))
		for i := range r.Results {
			order[i] = &r.Results[i]
		}
		sort.SliceStable(order, func(i, j int) bool { return less(order[i], order[j]) })
		p("\n%s\n\n", title)
		table(func(row func(...string)) {
			row("FILE", "TIME", "PEAK HEAP", "ALLOCATED", "FRAMES", "CANVAS", "SIZE", "SLOWEST STAGE")
			for _, f := range order[:min(top, len(order))] {
				slowest := 0
				for s := range f.Stages {
					if f.Stages[s].Time > f.Stages[slowest].Time {
						slowest = s
					}
				}
				row(shorten(f.Name, 40), humanDuration(f.EndToEnd.Time), humanBytes(f.PeakHeap), humanBytes(f.EndToEnd.AllocBytes),
					fmt.Sprint(f.Frames), fmt.Sprintf("%dx%d", f.Width, f.Height), humanBytes(f.Bytes),
					fmt.Sprintf("%s %.0f%%", f.Stages[slowest].Name, 100*f.Stages[slowest].Share))
			}
		})
	}
	ranked("Slowest files", func(a, b *fileReport) bool { return a.EndToEnd.Time > b.EndToEnd.Time })
	ranked("Largest peak heap", func(a, b *fileReport) bool { return a.PeakHeap > b.PeakHeap })

	_, err := io.WriteString(w, b.String())
	return err
}

// shorten cuts a name to at most n characters, keeping its end, which holds
// the extension, and marking the cut.
func shorten(name string, n int) string {
	r := []rune(name)
	if len(r) <= n {
		return name
	}
	keep := n / 3
	return string(r[:n-keep-1]) + "…" + string(r[len(r)-keep:])
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func humanPixels(n int64) string {
	if n >= 1e6 {
		return fmt.Sprintf("%.1f Mpx", float64(n)/1e6)
	}
	return fmt.Sprintf("%d px", n)
}

// humanDuration keeps three significant digits or so.
func humanDuration(d time.Duration) string {
	switch {
	case d >= time.Minute:
		return d.Round(time.Second).String()
	case d >= time.Second:
		return fmt.Sprintf("%.2fs", d.Seconds())
	case d >= time.Millisecond:
		return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
	case d >= time.Microsecond:
		return fmt.Sprintf("%.1fµs", float64(d)/float64(time.Microsecond))
	}
	return fmt.Sprintf("%dns", d.Nanoseconds())
}
