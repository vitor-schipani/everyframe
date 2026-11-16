// Command gifcorpus manages the GIF corpus used by end-to-end tests.
//
//	go run ./internal/tools/gifcorpus fetch      download the pinned corpus
//	go run ./internal/tools/gifcorpus verify     check the local copy offline
//	go run ./internal/tools/gifcorpus discover   regenerate the manifest (maintainers)
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/vitor-schipani/everyframe/internal/corpus"
)

const usage = `usage: go run ./internal/tools/gifcorpus <command> [flags]

Run it from the repository root; there is nothing to install.

commands:
  fetch      download the corpus files listed in the manifest
  verify     check the downloaded files against the manifest, offline
  discover   rebuild the manifest from Wikimedia Commons (maintainers)

Two make targets cover the usual workflows:
  make corpus           fetch, then verify
  make corpus-refresh   discover, then fetch, then verify (maintainers)

For the flags of a command:
  go run ./internal/tools/gifcorpus <command> -h
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

// ANSI style codes used by output.paint.
const (
	styleBold   = "1"
	styleDim    = "2"
	styleRed    = "31"
	styleGreen  = "32"
	styleYellow = "33"
	styleHeader = "1;36"
)

// output wraps a stream and remembers its first write failure, so the many
// print calls below need no individual error checks. It also knows whether
// the stream is a terminal that should get colour.
type output struct {
	w     io.Writer
	color bool
	err   error
}

func newOutput(w io.Writer) *output { return &output{w: w, color: wantsColor(w)} }

// wantsColor reports whether w is an interactive terminal whose user has not
// opted out of colour (https://no-color.org).
func wantsColor(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func (o *output) printf(format string, args ...any) {
	if _, err := fmt.Fprintf(o.w, format, args...); err != nil && o.err == nil {
		o.err = err
	}
}

// paint wraps s in the given ANSI style when colour is enabled.
func (o *output) paint(style, s string) string {
	if !o.color {
		return s
	}
	return "\x1b[" + style + "m" + s + "\x1b[0m"
}

// header starts a section: "==> Fetching ...".
func (o *output) header(format string, args ...any) {
	o.printf("%s %s\n", o.paint(styleHeader, "==>"), o.paint(styleBold, fmt.Sprintf(format, args...)))
}

// step prints one numbered progress line: "  [ 12/100] ...".
func (o *output) step(done, total int, format string, args ...any) {
	width := len(strconv.Itoa(total))
	counter := fmt.Sprintf("[%*d/%d]", width, done, total)
	o.printf("  %s %s\n", o.paint(styleDim, counter), fmt.Sprintf(format, args...))
}

// mark prints an indented line led by a coloured symbol.
func (o *output) mark(style, symbol, format string, args ...any) {
	o.printf("  %s %s\n", o.paint(style, symbol), fmt.Sprintf(format, args...))
}

func (o *output) ok(format string, args ...any)   { o.mark(styleGreen, "✓", format, args...) }
func (o *output) fail(format string, args ...any) { o.mark(styleRed, "✗", format, args...) }
func (o *output) warn(format string, args ...any) { o.mark(styleYellow, "!", format, args...) }

// run executes the command line and returns the process exit code. A command
// that succeeds but could not write its output still counts as a failure.
func run(ctx context.Context, args []string, stdoutW, stderrW io.Writer) int {
	stdout, stderr := newOutput(stdoutW), newOutput(stderrW)
	code := dispatch(ctx, args, stdout, stderr)
	if code == 0 && (stdout.err != nil || stderr.err != nil) {
		return 1
	}
	return code
}

func dispatch(ctx context.Context, args []string, stdout, stderr *output) int {
	if len(args) == 0 {
		stderr.printf("%s", usage)
		return 2
	}
	var err error
	switch cmd, rest := args[0], args[1:]; cmd {
	case "fetch":
		err = runFetch(ctx, rest, stdout, stderr)
	case "verify":
		err = runVerify(rest, stdout, stderr)
	case "discover":
		err = runDiscover(ctx, rest, stdout, stderr)
	case "-h", "-help", "--help", "help":
		stdout.printf("%s", usage)
		return 0
	default:
		stderr.printf("gifcorpus: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	switch err {
	case nil:
		return 0
	case flag.ErrHelp:
		return 0
	case errIncomplete:
		return 1
	}
	stderr.printf("gifcorpus: %v\n", err)
	return 1
}

// errIncomplete signals a failure that has already been reported in detail.
var errIncomplete = fmt.Errorf("corpus incomplete")

func defaultDir() string {
	if dir := os.Getenv(corpus.DirEnv); dir != "" {
		return dir
	}
	return corpus.DefaultDir
}

func newFlags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("gifcorpus "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// displayTitle drops the "File:" namespace Commons puts on every title.
func displayTitle(e corpus.Entry) string { return strings.TrimPrefix(e.Title, "File:") }

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func runFetch(ctx context.Context, args []string, stdout, stderr *output) error {
	fs := newFlags("fetch", stderr.w)
	manifest := fs.String("manifest", corpus.DefaultManifestPath, "manifest to read")
	dir := fs.String("dir", defaultDir(), "directory to download into (env "+corpus.DirEnv+")")
	limit := fs.Int("limit", 0, "download only the first N entries (0 = all)")
	jobs := fs.Int("jobs", 3, "concurrent downloads")
	userAgent := fs.String("user-agent", corpus.DefaultUserAgent, "User-Agent sent to the file host")
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, err := corpus.Load(*manifest)
	if err != nil {
		return err
	}
	wanted := m.Entries
	if *limit > 0 && *limit < len(wanted) {
		wanted = wanted[:*limit]
	}
	var wantedBytes int64
	for _, e := range wanted {
		wantedBytes += e.Size
	}
	stdout.header("Fetching %s (%s) into %s", plural(len(wanted), "GIF"), humanBytes(wantedBytes), *dir)

	// Files that are already present advance the counter without a line of
	// their own, so a re-run prints only the summary.
	done := 0
	res, err := corpus.Fetch(ctx, m, corpus.FetchOptions{
		Dir:       *dir,
		Jobs:      *jobs,
		Limit:     *limit,
		UserAgent: *userAgent,
		Progress: func(ev corpus.FetchEvent) {
			done++
			switch ev.Status {
			case corpus.StatusDownloaded:
				stdout.step(done, len(wanted), "%9s  %s", humanBytes(ev.Entry.Size), displayTitle(ev.Entry))
			case corpus.StatusFailed:
				stderr.fail("%s: %v", displayTitle(ev.Entry), ev.Err)
			}
		},
	})
	summary := fmt.Sprintf("%d downloaded (%s), %d already present",
		res.Downloaded, humanBytes(res.DownloadedBytes), res.Present)
	if len(res.Failed) > 0 {
		stderr.fail("%d failed, %s", len(res.Failed), summary)
	} else if err == nil {
		stdout.ok("%s", summary)
	}
	if err != nil {
		return err
	}
	if len(res.Failed) > 0 {
		return errIncomplete
	}
	return nil
}

func runVerify(args []string, stdout, stderr *output) error {
	fs := newFlags("verify", stderr.w)
	manifest := fs.String("manifest", corpus.DefaultManifestPath, "manifest to read")
	dir := fs.String("dir", defaultDir(), "directory holding the downloads (env "+corpus.DirEnv+")")
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, err := corpus.Load(*manifest)
	if err != nil {
		return err
	}
	stdout.header("Verifying %s against %s", *dir, *manifest)
	rep, err := corpus.Verify(m, *dir)
	if err != nil {
		return err
	}
	for _, e := range rep.Missing {
		stdout.fail("missing  %s  %s", e.Filename(), displayTitle(e))
	}
	for _, e := range rep.Corrupt {
		stdout.fail("corrupt  %s  %s", e.Filename(), displayTitle(e))
	}
	for _, name := range rep.Extra {
		stdout.warn("not in manifest: %s", name)
	}
	if !rep.Complete() {
		stdout.fail("%d of %s ok, %d missing, %d corrupt",
			rep.OK, plural(len(m.Entries), "file"), len(rep.Missing), len(rep.Corrupt))
		return errIncomplete
	}
	stdout.ok("%d of %s ok", rep.OK, plural(len(m.Entries), "file"))
	return nil
}

func runDiscover(ctx context.Context, args []string, stdout, stderr *output) error {
	fs := newFlags("discover", stderr.w)
	out := fs.String("out", corpus.DefaultManifestPath, "manifest to write")
	category := fs.String("category", corpus.DefaultCategory, "Commons category to sample")
	count := fs.Int("count", corpus.DefaultCount, "number of files to select")
	maxTotal := fs.Int64("max-total-bytes", corpus.DefaultMaxTotalBytes, "cap on the combined size of the selection")
	maxFile := fs.Int64("max-file-bytes", corpus.DefaultMaxFileBytes, "largest single file to consider")
	pages := fs.Int("pages-per-prefix", 1, "50-file listing pages to read per sort-key prefix")
	delay := fs.Duration("delay", 250*time.Millisecond, "pause between API requests")
	userAgent := fs.String("user-agent", corpus.DefaultUserAgent, "User-Agent sent to the API")
	if err := fs.Parse(args); err != nil {
		return err
	}
	stdout.header("Sampling %s on Wikimedia Commons", *category)
	candidates := 0
	m, err := corpus.Discover(ctx, corpus.DiscoverOptions{
		Progress: func(ev corpus.DiscoverEvent) {
			candidates = ev.Candidates
			stdout.step(ev.Done, ev.Total, "prefix %s  %5d candidates", ev.Prefix, ev.Candidates)
		},
		Category:       *category,
		Count:          *count,
		MaxTotalBytes:  *maxTotal,
		MaxFileBytes:   *maxFile,
		PagesPerPrefix: *pages,
		Delay:          *delay,
		UserAgent:      *userAgent,
	})
	if err != nil {
		return err
	}
	if len(m.Entries) == 0 {
		return fmt.Errorf("no files matched in %s; manifest not written", *category)
	}
	small, medium, large := corpus.BucketCounts(m.Entries)
	stdout.ok("selected %d of %d candidates: %d small, %d medium, %d large (%s)",
		len(m.Entries), candidates, small, medium, large, corpus.BucketLimits())
	size := fmt.Sprintf("%s, %s", plural(len(m.Entries), "file"), humanBytes(m.TotalBytes()))
	// Rewriting an identical selection would only change the timestamp and
	// leave a pointless diff, so the existing manifest is kept as it is.
	if old, err := corpus.Load(*out); err == nil && old.SameSelection(m) {
		stdout.ok("%s is already up to date (%s)", *out, size)
	} else {
		if err := m.Save(*out); err != nil {
			return err
		}
		stdout.ok("wrote %s (%s)", *out, size)
	}
	if len(m.Entries) < *count {
		stderr.warn("wanted %d files but only %d fit the buckets and size cap; try -pages-per-prefix 2",
			*count, len(m.Entries))
	}
	return nil
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
