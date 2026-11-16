package corpus

import (
	"context"
	"crypto/sha1" // the checksum Commons publishes; used for integrity, not security
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// FetchOptions controls how corpus files are downloaded. Zero values select
// the defaults.
type FetchOptions struct {
	// Dir is where files are stored.
	Dir string
	// Jobs is the number of concurrent downloads.
	Jobs int
	// Limit restricts the run to the first Limit manifest entries; 0 means all.
	Limit     int
	UserAgent string
	Client    *http.Client
	Retries   int
	Backoff   time.Duration
	// Progress, if set, is called once per entry. Calls are serialized.
	Progress func(FetchEvent)
}

// FetchStatus is the outcome for one entry.
type FetchStatus string

const (
	StatusDownloaded FetchStatus = "downloaded"
	StatusPresent    FetchStatus = "present"
	StatusFailed     FetchStatus = "failed"
)

// FetchEvent reports the outcome for one entry.
type FetchEvent struct {
	Entry  Entry
	Status FetchStatus
	Err    error
}

// FetchResult summarizes a Fetch run.
type FetchResult struct {
	Downloaded      int
	DownloadedBytes int64
	Present         int
	Failed          []FetchEvent
}

func (o FetchOptions) withDefaults() FetchOptions {
	if o.Dir == "" {
		o.Dir = DefaultDir
	}
	if o.Jobs <= 0 {
		o.Jobs = 3
	}
	if o.UserAgent == "" {
		o.UserAgent = DefaultUserAgent
	}
	o.Client = defaultClient(o.Client)
	if o.Retries == 0 {
		o.Retries = defaultRetries
	}
	if o.Backoff == 0 {
		o.Backoff = defaultBackoff
	}
	return o
}

// Fetch downloads every manifest entry that is not already present and valid
// in opts.Dir. Per-entry failures are collected in the result; the returned
// error is reserved for problems that stop the whole run.
func Fetch(ctx context.Context, m *Manifest, opts FetchOptions) (FetchResult, error) {
	opts = opts.withDefaults()
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return FetchResult{}, err
	}
	entries := m.Entries
	if opts.Limit > 0 && opts.Limit < len(entries) {
		entries = entries[:opts.Limit]
	}

	var (
		mu     sync.Mutex
		result FetchResult
		wg     sync.WaitGroup
		work   = make(chan Entry)
	)
	record := func(ev FetchEvent) {
		mu.Lock()
		defer mu.Unlock()
		switch ev.Status {
		case StatusDownloaded:
			result.Downloaded++
			result.DownloadedBytes += ev.Entry.Size
		case StatusPresent:
			result.Present++
		case StatusFailed:
			result.Failed = append(result.Failed, ev)
		}
		if opts.Progress != nil {
			opts.Progress(ev)
		}
	}
	for range opts.Jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range work {
				record(fetchOne(ctx, e, opts))
			}
		}()
	}
feed:
	for _, e := range entries {
		select {
		case work <- e:
		case <-ctx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()

	sort.Slice(result.Failed, func(i, j int) bool {
		return result.Failed[i].Entry.SHA1 < result.Failed[j].Entry.SHA1
	})
	return result, ctx.Err()
}

func fetchOne(ctx context.Context, e Entry, opts FetchOptions) FetchEvent {
	path := e.LocalPath(opts.Dir)
	if err := checkFile(path, e); err == nil {
		return FetchEvent{Entry: e, Status: StatusPresent}
	}
	err := withRetry(ctx, opts.Retries, opts.Backoff, func() error {
		return download(ctx, e, path, opts)
	})
	if err != nil {
		return FetchEvent{Entry: e, Status: StatusFailed, Err: err}
	}
	return FetchEvent{Entry: e, Status: StatusDownloaded}
}

// download fetches the entry and moves it into place at path only once its
// size and checksum match the manifest.
func download(ctx context.Context, e Entry, path string, opts FetchOptions) error {
	tmp, err := downloadToTemp(ctx, e, filepath.Dir(path), opts)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return errors.Join(err, removeIfExists(tmp))
	}
	return nil
}

// downloadToTemp writes the entry to a temporary file in dir and returns its
// name. The file is verified, closed and world-readable on success, and
// removed on any failure.
func downloadToTemp(ctx context.Context, e Entry, dir string, opts FetchOptions) (name string, err error) {
	// The temp file is created before the request so that its cleanup runs
	// after the response body has been closed and can see a close failure.
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", err
	}
	defer func() {
		if cerr := tmp.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close %s: %w", tmp.Name(), cerr)
		}
		if err != nil {
			name = ""
			err = errors.Join(err, removeIfExists(tmp.Name()))
		}
	}()

	resp, err := get(ctx, opts.Client, opts.UserAgent, e.URL)
	if err != nil {
		return "", err
	}
	defer func() {
		// A close failure only matters when nothing else went wrong.
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = &transportError{fmt.Errorf("close response body for %s: %w", e.URL, cerr)}
		}
	}()

	h := sha1.New()
	// Read one byte past the expected size so an oversized body is detected
	// without storing all of it.
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, e.Size+1))
	if err != nil {
		return "", &transportError{fmt.Errorf("download %s: %w", e.URL, err)}
	}
	if n != e.Size {
		return "", fmt.Errorf("download %s: got %d bytes, manifest says %d", e.URL, n, e.Size)
	}
	if sum := hex.EncodeToString(h.Sum(nil)); sum != e.SHA1 {
		return "", fmt.Errorf("download %s: sha1 %s does not match manifest %s", e.URL, sum, e.SHA1)
	}
	// CreateTemp makes the file owner-only; corpus files are ordinary data.
	if err := tmp.Chmod(0o644); err != nil {
		return "", err
	}
	return tmp.Name(), nil
}

// removeIfExists deletes path, treating an already missing file as success.
func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// checkFile returns nil when path holds exactly the bytes the entry describes.
func checkFile(path string, e Entry) (err error) {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		// A close failure only matters when nothing else went wrong.
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close %s: %w", path, cerr)
		}
	}()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() != e.Size {
		return fmt.Errorf("%s: size %d, manifest says %d", path, info.Size(), e.Size)
	}
	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if sum := hex.EncodeToString(h.Sum(nil)); sum != e.SHA1 {
		return fmt.Errorf("%s: sha1 %s, manifest says %s", path, sum, e.SHA1)
	}
	return nil
}

// VerifyReport is the state of a local corpus directory against a manifest.
type VerifyReport struct {
	OK      int
	Missing []Entry
	Corrupt []Entry
	// Extra lists files in the directory that the manifest does not mention.
	Extra []string
}

// Complete reports whether every manifest entry is present and valid.
func (r VerifyReport) Complete() bool { return len(r.Missing) == 0 && len(r.Corrupt) == 0 }

// Verify checks dir against the manifest without using the network.
func Verify(m *Manifest, dir string) (VerifyReport, error) {
	var r VerifyReport
	known := make(map[string]bool, len(m.Entries))
	for _, e := range m.Entries {
		known[e.Filename()] = true
		switch err := checkFile(e.LocalPath(dir), e); {
		case err == nil:
			r.OK++
		case errors.Is(err, fs.ErrNotExist):
			r.Missing = append(r.Missing, e)
		default:
			r.Corrupt = append(r.Corrupt, e)
		}
	}
	files, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return r, err
	}
	for _, f := range files {
		if !known[f.Name()] && !strings.HasPrefix(f.Name(), ".") {
			r.Extra = append(r.Extra, f.Name())
		}
	}
	return r, nil
}
