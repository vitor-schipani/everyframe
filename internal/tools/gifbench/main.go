// Command gifbench measures how much time and memory it takes to extract
// the features of GIFs, end to end and stage by stage, and prints a report.
//
//	go run ./internal/tools/gifbench               the standard corpus
//	go run ./internal/tools/gifbench -extended     the extended corpus
//	go run ./internal/tools/gifbench a.gif b.gif   the files named
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vitor-schipani/everyframe/internal/corpus"
)

const usage = `usage: go run ./internal/tools/gifbench [flags] [file.gif ...]

Measures the time and memory it takes to decode GIFs and extract their
features, end to end and per stage. Progress is shown while it runs and the
report is printed when it ends; -o also keeps a copy in a file. Without files
it runs on the downloaded corpus; run it from the repository root.

Two make targets cover the usual runs:
  make bench            the standard corpus
  make bench-extended   the extended corpus

flags:
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// input is one file to measure: the name the report shows and where it is.
type input struct{ name, path string }

// run executes the command line and returns the process exit code: 0 on
// success, 1 when the benchmark could not run or be written, 2 for a mistake
// in the command line.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gifbench", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
	}
	extended := fs.Bool("extended", false, "run on the extended corpus instead of the standard one")
	limit := fs.Int("limit", 0, "measure only the first N files (0 = all)")
	runs := fs.Int("runs", 1, "times each file is timed; the fastest run is kept")
	top := fs.Int("top", 5, "files listed among the slowest and the most demanding")
	asJSON := fs.Bool("json", false, "print the report as JSON, with every file's measurements")
	out := fs.String("o", "", "also write the report to this file; the text report is still printed")
	quiet := fs.Bool("q", false, "do not print progress on standard error")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *runs < 1 || *limit < 0 || *top < 0 {
		fmt.Fprintln(stderr, "gifbench: -runs must be at least 1, and -limit and -top not negative")
		return 2
	}

	source, inputs, err := collect(fs.Args(), *extended)
	if err != nil {
		fmt.Fprintf(stderr, "gifbench: %v\n", err)
		return 1
	}
	if *limit > 0 && *limit < len(inputs) {
		inputs = inputs[:*limit]
	}

	prog := newProgress(stderr, len(inputs), *quiet)
	results := make([]fileResult, 0, len(inputs))
	for _, in := range inputs {
		data, err := os.ReadFile(in.path)
		if err != nil {
			prog.finish()
			fmt.Fprintf(stderr, "gifbench: %v\n", err)
			return 1
		}
		prog.start(in.name)
		res := measure(in.name, data, *runs)
		results = append(results, res)
		prog.done(res)
	}
	prog.finish()
	rep := buildReport(source, *runs, time.Now(), results)

	// The terminal always gets the report. With -o a copy is kept as well,
	// and that copy is the JSON one when -json is given.
	write := func(w io.Writer, asJSON bool) error {
		if !asJSON {
			return rep.writeText(w, *top)
		}
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	if err := write(stdout, *asJSON && *out == ""); err != nil {
		fmt.Fprintf(stderr, "gifbench: write the report: %v\n", err)
		return 1
	}
	if *out != "" {
		if err := save(*out, func(w io.Writer) error { return write(w, *asJSON) }); err != nil {
			fmt.Fprintf(stderr, "gifbench: %v\n", err)
			return 1
		}
		if _, err := fmt.Fprintf(stdout, "\nReport written to %s\n", *out); err != nil {
			return 1
		}
	}
	return 0
}

// save creates the file at path, and its directory, and fills it with write.
func save(path string, write func(io.Writer) error) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	return write(f)
}

// progress shows how the run is going on standard error: on a terminal a
// single line that is rewritten as each file is measured, with the running
// totals, and elsewhere a line every few seconds.
type progress struct {
	w        io.Writer
	total    int
	live     bool
	began    time.Time
	last     time.Time
	files    int
	frames   int
	time     time.Duration
	peakHeap int64
}

func newProgress(w io.Writer, total int, quiet bool) *progress {
	if quiet {
		w = io.Discard
	}
	now := time.Now()
	return &progress{w: w, total: total, live: !quiet && isTerminal(w), began: now, last: now}
}

// isTerminal reports whether w is an interactive terminal.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok || os.Getenv("TERM") == "dumb" {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func (p *progress) status() string {
	return fmt.Sprintf("[%*d/%d] %d frames, %s end to end, peak heap %s",
		len(fmt.Sprint(p.total)), p.files, p.total, p.frames, humanDuration(p.time), humanBytes(p.peakHeap))
}

// start announces the file about to be measured.
func (p *progress) start(name string) {
	if p.live {
		fmt.Fprintf(p.w, "\r\x1b[K%s  %s", p.status(), shorten(name, 40))
	}
}

// done adds a measured file to the running totals.
func (p *progress) done(res fileResult) {
	p.files++
	p.frames += res.Frames
	p.time += res.EndToEnd.Time
	p.peakHeap = max(p.peakHeap, res.PeakHeap)
	if !p.live && time.Since(p.last) >= 5*time.Second {
		p.last = time.Now()
		fmt.Fprintf(p.w, "gifbench: %s (%s elapsed)\n", p.status(), time.Since(p.began).Round(time.Second))
	}
}

// finish clears the live line so the report starts on a clean one.
func (p *progress) finish() {
	if p.live {
		fmt.Fprint(p.w, "\r\x1b[K")
	}
}

// collect lists the files to measure: the ones named, or else the downloaded
// files of a corpus tier in manifest order.
func collect(files []string, extended bool) (source string, inputs []input, err error) {
	if len(files) > 0 {
		for _, path := range files {
			inputs = append(inputs, input{name: filepath.Base(path), path: path})
		}
		return "files", inputs, nil
	}
	tier := corpus.Standard
	if extended {
		tier = corpus.Extended
	}
	m, err := corpus.Load(tier.ManifestPath)
	if err != nil {
		return "", nil, err
	}
	dir := tier.Dir()
	for _, e := range m.Entries {
		path := e.LocalPath(dir)
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return "", nil, err
		}
		inputs = append(inputs, input{name: strings.TrimPrefix(e.Title, "File:"), path: path})
	}
	if len(inputs) == 0 {
		return "", nil, fmt.Errorf("no %s corpus file in %s; run `make %s` first", tier.Name, dir, tier.MakeTarget)
	}
	source = fmt.Sprintf("%s corpus", tier.Name)
	if len(inputs) < len(m.Entries) {
		source += fmt.Sprintf(" (%d of %d files downloaded)", len(inputs), len(m.Entries))
	}
	return source, inputs, nil
}
