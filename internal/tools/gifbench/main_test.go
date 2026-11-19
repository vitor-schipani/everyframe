package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	stdgif "image/gif"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// animation encodes a GIF of the given size whose frames alternate between
// two colours.
func animation(t *testing.T, w, h, frames int) []byte {
	t.Helper()
	pal := color.Palette{color.Black, color.White}
	g := &stdgif.GIF{}
	for i := range frames {
		img := image.NewPaletted(image.Rect(0, 0, w, h), pal)
		for p := range img.Pix {
			img.Pix[p] = uint8(i % 2)
		}
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, 10)
	}
	var buf bytes.Buffer
	if err := stdgif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestMeasure(t *testing.T) {
	data := animation(t, 64, 48, 5)
	res := measure("a.gif", data, 2)

	if res.Name != "a.gif" || res.Bytes != int64(len(data)) || res.Error != "" {
		t.Errorf("result = %+v", res)
	}
	if res.Width != 64 || res.Height != 48 || res.Frames != 5 || res.Pixels != 5*64*48 {
		t.Errorf("measured %dx%d, %d frames, %d pixels; want 64x48, 5, %d", res.Width, res.Height, res.Frames, res.Pixels, 5*64*48)
	}
	if res.EndToEnd.Time <= 0 {
		t.Error("no end-to-end time")
	}
	// The canvas and the previous picture are four bytes per pixel each.
	if canvas := int64(2 * 4 * 64 * 48); res.EndToEnd.AllocBytes < canvas || res.PeakHeap < canvas {
		t.Errorf("allocated %d bytes with a peak of %d, less than the %d the extractor needs", res.EndToEnd.AllocBytes, res.PeakHeap, canvas)
	}
	if got := res.Stages[stageSetup].AllocBytes; got < 2*4*64*48 {
		t.Errorf("setup allocated %d bytes, less than the canvas and the previous picture", got)
	}
	for s, c := range res.Stages {
		if c.Time <= 0 {
			t.Errorf("stage %s has no time", stageName(s))
		}
	}
}

func TestMeasureKeepsWhatATruncatedFileHolds(t *testing.T) {
	data := animation(t, 64, 48, 5)
	res := measure("cut.gif", data[:len(data)-40], 1)
	if res.Error == "" {
		t.Error("no error for a file cut short")
	}
	if res.Frames == 0 || res.Frames > 5 {
		t.Errorf("measured %d frames of a file cut in its last frame", res.Frames)
	}

	if res := measure("junk", []byte("not a gif"), 1); res.Error == "" || res.Frames != 0 {
		t.Errorf("a file that is not a GIF gave %+v", res)
	}
}

func TestStageNames(t *testing.T) {
	want := []string{"decode", "setup", "stored", "composite", "change", "hash", "summary"}
	if len(want) != numStages {
		t.Fatalf("numStages = %d, want %d", numStages, len(want))
	}
	for i, name := range want {
		if got := stageName(i); got != name {
			t.Errorf("stageName(%d) = %q, want %q", i, got, name)
		}
	}
}

func TestSpread(t *testing.T) {
	var values []int64
	for v := int64(100); v >= 1; v-- {
		values = append(values, v)
	}
	if got, want := newSpread(values), (spread{P50: 50, P90: 90, P99: 99, Max: 100}); got != want {
		t.Errorf("spread of 1..100 = %+v, want %+v", got, want)
	}
	if got, want := newSpread([]int64{7}), (spread{7, 7, 7, 7}); got != want {
		t.Errorf("spread of one value = %+v, want %+v", got, want)
	}
	if got := newSpread(nil); got != (spread{}) {
		t.Errorf("spread of nothing = %+v", got)
	}
}

func TestBuildReport(t *testing.T) {
	a := fileResult{Name: "a.gif", Bytes: 10, Frames: 2, Pixels: 100, PeakHeap: 5,
		EndToEnd: cost{Time: 3 * time.Millisecond, AllocBytes: 50, Allocs: 2}}
	a.Stages[stageDecode] = cost{Time: 3 * time.Millisecond, AllocBytes: 40, Allocs: 1}
	a.Stages[stageSetup] = cost{Time: time.Millisecond, AllocBytes: 10, Allocs: 1}
	b := fileResult{Name: "b.gif", Bytes: 20, Frames: 1, Pixels: 50, PeakHeap: 9, Error: "cut short",
		EndToEnd: cost{Time: time.Millisecond, AllocBytes: 30, Allocs: 1}}

	r := buildReport("files", 3, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), []fileResult{a, b})
	if r.GeneratedAt != "2026-01-02T03:04:05Z" || r.Source != "files" || r.Runs != 3 {
		t.Errorf("header = %q %q %d", r.GeneratedAt, r.Source, r.Runs)
	}
	if r.Files != 2 || r.Bytes != 30 || r.Frames != 3 || r.Pixels != 150 || r.Failed != 1 {
		t.Errorf("totals = %d files, %d bytes, %d frames, %d pixels, %d failed", r.Files, r.Bytes, r.Frames, r.Pixels, r.Failed)
	}
	if want := (cost{Time: 4 * time.Millisecond, AllocBytes: 80, Allocs: 3}); r.EndToEnd != want || r.PeakHeap != 9 {
		t.Errorf("end to end = %+v with peak %d, want %+v with peak 9", r.EndToEnd, r.PeakHeap, want)
	}
	if len(r.Stages) != numStages || r.Stages[stageDecode].Share != 0.75 || r.Stages[stageSetup].Share != 0.25 {
		t.Errorf("stages = %+v", r.Stages)
	}
	if r.PerFile.Time.Max != int64(3*time.Millisecond) || r.PerFile.PeakHeap.P50 != 5 {
		t.Errorf("per file = %+v", r.PerFile)
	}
}

func TestHumanDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		49:                       "49ns",
		1500:                     "1.5µs",
		23100 * time.Microsecond: "23.1ms",
		9100 * time.Millisecond:  "9.10s",
		125 * time.Second:        "2m5s",
	} {
		if got := humanDuration(d); got != want {
			t.Errorf("humanDuration(%d) = %q, want %q", d, got, want)
		}
	}
	if got := shorten("a very long file name indeed.gif", 16); got != "a very lon…d.gif" {
		t.Errorf("shorten = %q", got)
	}
}

// writeFiles puts two animations in a directory and returns their paths.
func writeFiles(t *testing.T) []string {
	t.Helper()
	dir := t.TempDir()
	var paths []string
	for name, frames := range map[string]int{"small.gif": 2, "long.gif": 30} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, animation(t, 32, 32, frames), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	return paths
}

func TestRunText(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(append([]string{"-top", "1"}, writeFiles(t)...), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"source   files: 2 files", "content  32 frames", "End to end", "Per stage", "all stages",
		"decode ", "change ", "Per file", "peak heap", "Slowest files", "Largest peak heap", "long.gif",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q:\n%s", want, out)
		}
	}
	// With -top 1 each ranking lists one file.
	if n := strings.Count(out, ".gif"); n != 2 {
		t.Errorf("report names files %d times, want 2:\n%s", n, out)
	}
}

func TestRunJSON(t *testing.T) {
	out := filepath.Join(t.TempDir(), "reports", "bench.json")
	var stdout, stderr bytes.Buffer
	args := append([]string{"-json", "-q", "-limit", "1", "-o", out}, writeFiles(t)...)
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr.String())
	}
	// The terminal still gets the text report, and is told where the copy is.
	if got := stdout.String(); !strings.Contains(got, "Per stage") || !strings.HasSuffix(got, "\nReport written to "+out+"\n") {
		t.Errorf("with -o, stdout = %q", got)
	}
	if stderr.Len() != 0 {
		t.Errorf("with -q, stderr = %q", stderr.String())
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var rep struct {
		Files   int `json:"files"`
		Stages  []struct{ Name string }
		Results []struct {
			Name     string
			EndToEnd struct {
				Time int64 `json:"time_ns"`
			} `json:"end_to_end"`
			Stages []struct {
				Name string
				Time int64 `json:"time_ns"`
			}
		}
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatalf("%v:\n%s", err, raw)
	}
	if rep.Files != 1 || len(rep.Results) != 1 || len(rep.Stages) != numStages {
		t.Fatalf("report has %d files, %d results, %d stages", rep.Files, len(rep.Results), len(rep.Stages))
	}
	if r := rep.Results[0]; r.EndToEnd.Time <= 0 || len(r.Stages) != numStages || r.Stages[0].Name != "decode" || r.Stages[0].Time <= 0 {
		t.Errorf("result = %+v", r)
	}
}

func TestProgress(t *testing.T) {
	var buf bytes.Buffer
	p := newProgress(&buf, 12, false)
	if p.live {
		t.Fatal("a buffer was taken for a terminal")
	}
	p.start("a.gif")
	p.done(fileResult{Frames: 3, PeakHeap: 2 << 20, EndToEnd: cost{Time: 2 * time.Second}})
	p.finish()
	if buf.Len() != 0 {
		t.Errorf("progress within five seconds, off a terminal: %q", buf.String())
	}
	p.last = time.Now().Add(-time.Minute)
	p.done(fileResult{Frames: 4, PeakHeap: 1 << 20, EndToEnd: cost{Time: time.Second}})
	if want := "gifbench: [ 2/12] 7 frames, 3.00s end to end, peak heap 2.0 MB ("; !strings.HasPrefix(buf.String(), want) {
		t.Errorf("progress = %q, want it to start with %q", buf.String(), want)
	}

	// On a terminal one line is rewritten in place.
	buf.Reset()
	p.live = true
	p.start("b.gif")
	p.finish()
	if want := "\r\x1b[K[ 2/12] 7 frames, 3.00s end to end, peak heap 2.0 MB  b.gif\r\x1b[K"; buf.String() != want {
		t.Errorf("live progress = %q, want %q", buf.String(), want)
	}
}

func TestRunExitCodes(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		want       int
		wantStderr string
	}{
		{"help", []string{"-h"}, 0, "usage:"},
		{"bad flag", []string{"-nope"}, 2, "-nope"},
		{"bad runs", []string{"-runs", "0"}, 2, "-runs"},
		{"missing file", []string{"does-not-exist.gif"}, 1, "does-not-exist.gif"},
		// Tests do not run in the repo root, so the manifest is not found.
		{"no corpus", []string{"-extended"}, 1, "extended.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := run(tc.args, &stdout, &stderr); got != tc.want {
				t.Errorf("exit code = %d, want %d (stderr: %s)", got, tc.want, stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tc.wantStderr)
			}
		})
	}
}
