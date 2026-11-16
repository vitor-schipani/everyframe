package corpus

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fileServer serves fixed bodies by path, counts hits, and can be told to
// fail the first few requests for a path with a given status.
type fileServer struct {
	mu       sync.Mutex
	bodies   map[string][]byte
	failures map[string][]int
	hits     map[string]int
}

func newFileServer() *fileServer {
	return &fileServer{bodies: map[string][]byte{}, failures: map[string][]int{}, hits: map[string]int{}}
}

func (s *fileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.hits[r.URL.Path]++
	var fail int
	if q := s.failures[r.URL.Path]; len(q) > 0 {
		fail, s.failures[r.URL.Path] = q[0], q[1:]
	}
	body, ok := s.bodies[r.URL.Path]
	s.mu.Unlock()
	switch {
	case fail != 0:
		w.Header().Set("Retry-After", "0")
		http.Error(w, "try later", fail)
	case !ok:
		http.NotFound(w, r)
	default:
		w.Write(body)
	}
}

func (s *fileServer) hitCount(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

type fetchFixture struct {
	files    *fileServer
	url      string
	dir      string
	manifest *Manifest
}

// newFetchFixture serves three files and returns a manifest describing them.
func newFetchFixture(t *testing.T) *fetchFixture {
	t.Helper()
	files := newFileServer()
	srv := httptest.NewServer(files)
	t.Cleanup(srv.Close)
	fx := &fetchFixture{files: files, url: srv.URL, dir: filepath.Join(t.TempDir(), "files"), manifest: &Manifest{}}
	for _, name := range []string{"one", "two", "three"} {
		body := []byte(strings.Repeat(name, 1000))
		files.bodies["/"+name+".gif"] = body
		fx.manifest.Entries = append(fx.manifest.Entries, testEntry("File:"+name+".gif", srv.URL+"/"+name+".gif", body))
	}
	return fx
}

func (fx *fetchFixture) fetch(t *testing.T, opts FetchOptions) FetchResult {
	t.Helper()
	opts.Dir = fx.dir
	opts.Backoff = time.Millisecond
	res, err := Fetch(context.Background(), fx.manifest, opts)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (fx *fetchFixture) assertStored(t *testing.T, e Entry) {
	t.Helper()
	got, err := os.ReadFile(e.LocalPath(fx.dir))
	if err != nil {
		t.Fatal(err)
	}
	want := fx.files.bodies[strings.TrimPrefix(e.URL, fx.url)]
	if !bytes.Equal(got, want) {
		t.Errorf("%s: stored content differs from served content", e.Title)
	}
}

// assertNoLeftovers fails if the directory holds anything but the named files.
func (fx *fetchFixture) assertNoLeftovers(t *testing.T, want ...Entry) {
	t.Helper()
	names := map[string]bool{}
	for _, e := range want {
		names[e.Filename()] = true
	}
	files, err := os.ReadDir(fx.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if !names[f.Name()] {
			t.Errorf("unexpected file left in corpus dir: %s", f.Name())
		}
	}
}

func TestFetchDownloadsThenSkips(t *testing.T) {
	fx := newFetchFixture(t)
	var events []FetchEvent
	res := fx.fetch(t, FetchOptions{Progress: func(ev FetchEvent) { events = append(events, ev) }})

	if res.Downloaded != 3 || res.Present != 0 || len(res.Failed) != 0 {
		t.Fatalf("first run: %+v", res)
	}
	if want := fx.manifest.TotalBytes(); res.DownloadedBytes != want {
		t.Errorf("DownloadedBytes = %d, want %d", res.DownloadedBytes, want)
	}
	if len(events) != 3 {
		t.Errorf("got %d progress events, want 3", len(events))
	}
	for _, e := range fx.manifest.Entries {
		fx.assertStored(t, e)
		info, err := os.Stat(e.LocalPath(fx.dir))
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
			t.Errorf("%s: mode %v, want 0644", e.Title, info.Mode().Perm())
		}
	}
	fx.assertNoLeftovers(t, fx.manifest.Entries...)

	res = fx.fetch(t, FetchOptions{})
	if res.Downloaded != 0 || res.Present != 3 {
		t.Errorf("second run: %+v, want everything already present", res)
	}
	if n := fx.files.hitCount("/one.gif"); n != 1 {
		t.Errorf("/one.gif requested %d times, want 1", n)
	}
}

func TestFetchLimit(t *testing.T) {
	fx := newFetchFixture(t)
	res := fx.fetch(t, FetchOptions{Limit: 2})
	if res.Downloaded != 2 {
		t.Fatalf("Downloaded = %d, want 2", res.Downloaded)
	}
	fx.assertNoLeftovers(t, fx.manifest.Entries[:2]...)
}

func TestFetchRepairsCorruptFile(t *testing.T) {
	fx := newFetchFixture(t)
	fx.fetch(t, FetchOptions{})
	bad := fx.manifest.Entries[1]
	// Same length as the original, so only the checksum can tell.
	if err := os.WriteFile(bad.LocalPath(fx.dir), bytes.Repeat([]byte("x"), int(bad.Size)), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := Verify(fx.manifest, fx.dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK != 2 || len(rep.Corrupt) != 1 || rep.Corrupt[0].SHA1 != bad.SHA1 || rep.Complete() {
		t.Fatalf("verify before repair: %+v", rep)
	}

	res := fx.fetch(t, FetchOptions{})
	if res.Downloaded != 1 || res.Present != 2 {
		t.Fatalf("repair run: %+v", res)
	}
	fx.assertStored(t, bad)
	if rep, _ := Verify(fx.manifest, fx.dir); !rep.Complete() {
		t.Errorf("verify after repair: %+v", rep)
	}
}

func TestFetchChecksumMismatchLeavesNothingBehind(t *testing.T) {
	fx := newFetchFixture(t)
	bad := fx.manifest.Entries[0]
	// The server now returns different bytes of the same length.
	fx.files.bodies["/one.gif"] = bytes.Repeat([]byte("z"), int(bad.Size))

	res := fx.fetch(t, FetchOptions{})
	if res.Downloaded != 2 || len(res.Failed) != 1 || res.Failed[0].Entry.SHA1 != bad.SHA1 {
		t.Fatalf("result: %+v", res)
	}
	if !strings.Contains(res.Failed[0].Err.Error(), "sha1") {
		t.Errorf("error should name the checksum: %v", res.Failed[0].Err)
	}
	if n := fx.files.hitCount("/one.gif"); n != 1 {
		t.Errorf("a checksum mismatch was retried (%d requests)", n)
	}
	fx.assertNoLeftovers(t, fx.manifest.Entries[1:]...)
}

func TestFetchSizeMismatch(t *testing.T) {
	for name, body := range map[string][]byte{
		"truncated": []byte("short"),
		"oversized": bytes.Repeat([]byte("one"), 5000),
	} {
		t.Run(name, func(t *testing.T) {
			fx := newFetchFixture(t)
			fx.files.bodies["/one.gif"] = body
			res := fx.fetch(t, FetchOptions{})
			if len(res.Failed) != 1 || res.Downloaded != 2 {
				t.Fatalf("result: %+v", res)
			}
			fx.assertNoLeftovers(t, fx.manifest.Entries[1:]...)
		})
	}
}

func TestFetchRetriesTransientStatus(t *testing.T) {
	fx := newFetchFixture(t)
	fx.files.failures["/one.gif"] = []int{http.StatusTooManyRequests, http.StatusServiceUnavailable}
	res := fx.fetch(t, FetchOptions{})
	if res.Downloaded != 3 || len(res.Failed) != 0 {
		t.Fatalf("result: %+v", res)
	}
	if n := fx.files.hitCount("/one.gif"); n != 3 {
		t.Errorf("/one.gif requested %d times, want 3", n)
	}
}

func TestFetchGivesUp(t *testing.T) {
	t.Run("after retries are exhausted", func(t *testing.T) {
		fx := newFetchFixture(t)
		fx.files.failures["/one.gif"] = []int{503, 503, 503, 503}
		res := fx.fetch(t, FetchOptions{Retries: 2})
		if len(res.Failed) != 1 {
			t.Fatalf("result: %+v", res)
		}
		if n := fx.files.hitCount("/one.gif"); n != 3 {
			t.Errorf("/one.gif requested %d times, want 3 (1 try + 2 retries)", n)
		}
	})
	t.Run("immediately on 404", func(t *testing.T) {
		fx := newFetchFixture(t)
		delete(fx.files.bodies, "/one.gif")
		res := fx.fetch(t, FetchOptions{})
		if len(res.Failed) != 1 {
			t.Fatalf("result: %+v", res)
		}
		if n := fx.files.hitCount("/one.gif"); n != 1 {
			t.Errorf("a 404 was retried (%d requests)", n)
		}
	})
}

func TestVerifyMissingAndExtra(t *testing.T) {
	fx := newFetchFixture(t)
	fx.fetch(t, FetchOptions{Limit: 1})
	if err := os.WriteFile(filepath.Join(fx.dir, "stray.gif"), []byte("?"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Verify(fx.manifest, fx.dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK != 1 || len(rep.Missing) != 2 || len(rep.Corrupt) != 0 {
		t.Errorf("report: %+v", rep)
	}
	if len(rep.Extra) != 1 || rep.Extra[0] != "stray.gif" {
		t.Errorf("Extra = %v, want [stray.gif]", rep.Extra)
	}

	// A directory that does not exist yet is simply an empty corpus.
	rep, err = Verify(fx.manifest, filepath.Join(fx.dir, "nope"))
	if err != nil || len(rep.Missing) != 3 {
		t.Errorf("missing dir: %+v, %v", rep, err)
	}
}

// closeFailTransport makes Close on the first few response bodies fail.
type closeFailTransport struct{ failures atomic.Int32 }

func (t *closeFailTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	resp.Body = &failingCloser{ReadCloser: resp.Body, t: t}
	return resp, nil
}

type failingCloser struct {
	io.ReadCloser
	t *closeFailTransport
}

func (b *failingCloser) Close() error {
	err := b.ReadCloser.Close()
	if b.t.failures.Add(-1) >= 0 {
		return errors.New("close failed")
	}
	return err
}

func TestFetchBodyCloseFailure(t *testing.T) {
	t.Run("is retried", func(t *testing.T) {
		fx := newFetchFixture(t)
		tr := &closeFailTransport{}
		tr.failures.Store(1)
		res := fx.fetch(t, FetchOptions{Jobs: 1, Client: &http.Client{Transport: tr}})
		if res.Downloaded != 3 || len(res.Failed) != 0 {
			t.Fatalf("result: %+v", res)
		}
		if n := fx.files.hitCount("/one.gif"); n != 2 {
			t.Errorf("/one.gif requested %d times, want 2", n)
		}
		fx.assertNoLeftovers(t, fx.manifest.Entries...)
	})
	t.Run("is reported and leaves nothing behind", func(t *testing.T) {
		fx := newFetchFixture(t)
		tr := &closeFailTransport{}
		tr.failures.Store(1000)
		res := fx.fetch(t, FetchOptions{Retries: 1, Client: &http.Client{Transport: tr}})
		if res.Downloaded != 0 || len(res.Failed) != 3 {
			t.Fatalf("result: %+v", res)
		}
		if !strings.Contains(res.Failed[0].Err.Error(), "close response body") {
			t.Errorf("error should name the close failure: %v", res.Failed[0].Err)
		}
		fx.assertNoLeftovers(t)
	})
}

func TestGetReportsCloseFailureWithStatus(t *testing.T) {
	fx := newFetchFixture(t)
	tr := &closeFailTransport{}
	tr.failures.Store(1)
	_, err := get(context.Background(), &http.Client{Transport: tr}, "test", fx.url+"/missing.gif")
	var se *statusError
	if !errors.As(err, &se) || se.Code != http.StatusNotFound {
		t.Fatalf("status error lost: %v", err)
	}
	if !strings.Contains(err.Error(), "close response body") {
		t.Errorf("close failure not reported: %v", err)
	}
}
