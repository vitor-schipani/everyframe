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

// output wraps a stream and remembers its first write failure, so the many
// print calls below need no individual error checks.
type output struct {
	w   io.Writer
	err error
}

func (o *output) printf(format string, args ...any) {
	if _, err := fmt.Fprintf(o.w, format, args...); err != nil && o.err == nil {
		o.err = err
	}
}

// run executes the command line and returns the process exit code. A command
// that succeeds but could not write its output still counts as a failure.
func run(ctx context.Context, args []string, stdoutW, stderrW io.Writer) int {
	stdout, stderr := &output{w: stdoutW}, &output{w: stderrW}
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
	res, err := corpus.Fetch(ctx, m, corpus.FetchOptions{
		Dir:       *dir,
		Jobs:      *jobs,
		Limit:     *limit,
		UserAgent: *userAgent,
		Progress: func(ev corpus.FetchEvent) {
			switch ev.Status {
			case corpus.StatusDownloaded:
				stdout.printf("downloaded %9s  %s\n", humanBytes(ev.Entry.Size), ev.Entry.Title)
			case corpus.StatusFailed:
				stderr.printf("FAILED     %s: %v\n", ev.Entry.Title, ev.Err)
			}
		},
	})
	stdout.printf("%d downloaded (%s), %d already present, %d failed, in %s\n",
		res.Downloaded, humanBytes(res.DownloadedBytes), res.Present, len(res.Failed), *dir)
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
	rep, err := corpus.Verify(m, *dir)
	if err != nil {
		return err
	}
	for _, e := range rep.Missing {
		stdout.printf("missing  %s  %s\n", e.Filename(), e.Title)
	}
	for _, e := range rep.Corrupt {
		stdout.printf("corrupt  %s  %s\n", e.Filename(), e.Title)
	}
	for _, name := range rep.Extra {
		stdout.printf("extra    %s\n", name)
	}
	stdout.printf("%d of %d ok, %d missing, %d corrupt, %d not in manifest\n",
		rep.OK, len(m.Entries), len(rep.Missing), len(rep.Corrupt), len(rep.Extra))
	if !rep.Complete() {
		return errIncomplete
	}
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
	stdout.printf("sampling %s on Wikimedia Commons\n", *category)
	candidates := 0
	m, err := corpus.Discover(ctx, corpus.DiscoverOptions{
		Progress: func(ev corpus.DiscoverEvent) {
			candidates = ev.Candidates
			stdout.printf("[%2d/%d] listed prefix %s, %d candidates so far\n", ev.Done, ev.Total, ev.Prefix, ev.Candidates)
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
	stdout.printf("selected %d of %d candidates\n", len(m.Entries), candidates)
	// Rewriting an identical selection would only change the timestamp and
	// leave a pointless diff, so the existing manifest is kept as it is.
	if old, err := corpus.Load(*out); err == nil && old.SameSelection(m) {
		stdout.printf("%s is already up to date: %d files, %s total\n", *out, len(m.Entries), humanBytes(m.TotalBytes()))
	} else {
		if err := m.Save(*out); err != nil {
			return err
		}
		stdout.printf("wrote %s: %d files, %s total\n", *out, len(m.Entries), humanBytes(m.TotalBytes()))
	}
	stdout.printf("%d small, %d medium, %d large (%s)\n", small, medium, large, corpus.BucketLimits())
	if len(m.Entries) < *count {
		stderr.printf("warning: wanted %d files but only %d fit the buckets and size cap; try -pages-per-prefix 2\n",
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
