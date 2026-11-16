package corpus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"
)

// apiFile is the test-side description of one file returned by the fake API.
type apiFile struct {
	title    string
	content  string // only used to derive a unique checksum
	size     int64
	mime     string
	duration float64
	license  string
	artist   string
}

func (f apiFile) page(id int) map[string]any {
	e := testEntry(f.title, "", []byte(f.content))
	meta := map[string]any{}
	if f.license != "" {
		meta["LicenseShortName"] = map[string]any{"value": f.license}
	}
	if f.artist != "" {
		meta["Artist"] = map[string]any{"value": f.artist}
	}
	return map[string]any{
		"pageid": id,
		"title":  f.title,
		"imageinfo": []any{map[string]any{
			"size":           f.size,
			"width":          10,
			"height":         20,
			"duration":       f.duration,
			"url":            "https://upload.example.org/" + e.SHA1 + ".gif?utm_source=x",
			"descriptionurl": "https://commons.example.org/wiki/" + f.title,
			"sha1":           e.SHA1,
			"mime":           f.mime,
			"extmetadata":    meta,
		}},
	}
}

func gif(title string, size int64) apiFile {
	return apiFile{title: title, content: title, size: size, mime: "image/gif", duration: 1.5, license: "CC0"}
}

// fakeAPI serves canned responses keyed by "<prefix>|<gcmcontinue>" and
// records every request.
type fakeAPI struct {
	t         *testing.T
	responses map[string]map[string]any
	mu        sync.Mutex
	requests  []*http.Request
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r)
	f.mu.Unlock()
	q := r.URL.Query()
	key := q.Get("gcmstartsortkeyprefix") + "|" + q.Get("gcmcontinue")
	resp, ok := f.responses[key]
	if !ok {
		f.t.Errorf("unexpected API request %q", key)
		http.Error(w, "unexpected", http.StatusBadRequest)
		return
	}
	json.NewEncoder(w).Encode(resp)
}

func listing(complete bool, cont string, files ...apiFile) map[string]any {
	pages := make([]any, len(files))
	for i, f := range files {
		pages[i] = f.page(i + 1)
	}
	resp := map[string]any{"query": map[string]any{"pages": pages}}
	if complete {
		resp["batchcomplete"] = true
	}
	if cont != "" {
		resp["continue"] = map[string]any{"gcmcontinue": cont, "continue": "gcmcontinue||"}
	}
	return resp
}

func discoverWith(t *testing.T, api *fakeAPI, opts DiscoverOptions) *Manifest {
	t.Helper()
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	opts.APIURL = srv.URL
	opts.Backoff = time.Millisecond
	opts.Now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	m, err := Discover(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func titles(entries []Entry) map[string]bool {
	out := map[string]bool{}
	for _, e := range entries {
		out[e.Title] = true
	}
	return out
}

func TestDiscoverFollowsContinuation(t *testing.T) {
	api := &fakeAPI{t: t, responses: map[string]map[string]any{
		"A|":     listing(true, "tok1", gif("File:a1.gif", 100)),
		"A|tok1": listing(true, "tok2", gif("File:a2.gif", 100)),
		// A third page exists but PagesPerPrefix is 2, so it must not be requested.
		"B|": listing(true, "", gif("File:b1.gif", 100)),
	}}
	m := discoverWith(t, api, DiscoverOptions{
		Prefixes: []string{"A", "B"}, PagesPerPrefix: 2, UserAgent: "test-agent/1",
	})

	want := map[string]bool{"File:a1.gif": true, "File:a2.gif": true, "File:b1.gif": true}
	if got := titles(m.Entries); !reflect.DeepEqual(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
	if len(api.requests) != 3 {
		t.Errorf("made %d requests, want 3", len(api.requests))
	}
	for _, r := range api.requests {
		if ua := r.Header.Get("User-Agent"); ua != "test-agent/1" {
			t.Errorf("User-Agent = %q", ua)
		}
		if got := r.URL.Query().Get("gcmtitle"); got != DefaultCategory {
			t.Errorf("gcmtitle = %q", got)
		}
	}
	if m.GeneratedAt != "2026-01-02T03:04:05Z" || m.Category != DefaultCategory {
		t.Errorf("header = %q %q", m.GeneratedAt, m.Category)
	}
}

func TestDiscoverMergesPartialBatches(t *testing.T) {
	// The first response is not batchcomplete: it is a partial result for the
	// same listing page and must not count towards PagesPerPrefix.
	api := &fakeAPI{t: t, responses: map[string]map[string]any{
		"A|":     listing(false, "tok1", gif("File:a1.gif", 100)),
		"A|tok1": listing(true, "", gif("File:a2.gif", 100)),
	}}
	m := discoverWith(t, api, DiscoverOptions{Prefixes: []string{"A"}})
	if len(m.Entries) != 2 {
		t.Errorf("got %d entries, want 2", len(m.Entries))
	}
}

func TestDiscoverFiltersAndCleans(t *testing.T) {
	keep := gif("File:keep.gif", 100)
	keep.artist = `<a href="//x">Jane &amp; Co</a>`
	png := gif("File:still.png", 100)
	png.mime = "image/png"
	static := gif("File:static.gif", 100)
	static.duration = 0
	unlicensed := gif("File:unlicensed.gif", 100)
	unlicensed.license = ""
	huge := gif("File:huge.gif", 1001)

	api := &fakeAPI{t: t, responses: map[string]map[string]any{
		"A|": listing(true, "", keep, png, static, unlicensed, huge),
	}}
	m := discoverWith(t, api, DiscoverOptions{Prefixes: []string{"A"}, MaxFileBytes: 1000})

	if len(m.Entries) != 1 || m.Entries[0].Title != "File:keep.gif" {
		t.Fatalf("entries = %v, want only File:keep.gif", titles(m.Entries))
	}
	e := m.Entries[0]
	if e.Artist != "Jane & Co" {
		t.Errorf("Artist = %q, want HTML stripped", e.Artist)
	}
	if want := "https://upload.example.org/" + e.SHA1 + ".gif"; e.URL != want {
		t.Errorf("URL = %q, want tracking query removed (%q)", e.URL, want)
	}
	if e.License != "CC0" || e.Width != 10 || e.Height != 20 || e.Duration != 1.5 || e.PageID != 1 {
		t.Errorf("metadata not carried over: %+v", e)
	}
}

func TestDiscoverReportsAPIError(t *testing.T) {
	api := &fakeAPI{t: t, responses: map[string]map[string]any{
		"A|": {"error": map[string]any{"code": "badvalue", "info": "nope"}},
	}}
	srv := httptest.NewServer(api)
	defer srv.Close()
	_, err := Discover(context.Background(), DiscoverOptions{APIURL: srv.URL, Prefixes: []string{"A"}})
	if err == nil {
		t.Fatal("expected an error")
	}
}

func candidates(n int, size int64, tag string) []Entry {
	out := make([]Entry, n)
	for i := range out {
		out[i] = testEntry(fmt.Sprintf("File:%s%d.gif", tag, i), "https://example.org/x", []byte(fmt.Sprintf("%s%d", tag, i)))
		out[i].Size = size
	}
	return out
}

func TestSelectEntriesBucketsAndOrder(t *testing.T) {
	var all []Entry
	all = append(all, candidates(30, 1000, "small")...)
	all = append(all, candidates(30, 1<<20, "medium")...)
	all = append(all, candidates(30, 10<<20, "large")...)

	got := selectEntries(all, 10, 1<<40)
	if len(got) != 10 {
		t.Fatalf("picked %d entries, want 10", len(got))
	}
	if s, m, l := BucketCounts(got); s != 4 || m != 4 || l != 2 {
		t.Errorf("buckets = %d/%d/%d, want 4/4/2", s, m, l)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].SHA1 >= got[i].SHA1 {
			t.Fatalf("entries not sorted by sha1 at %d", i)
		}
	}

	// The same candidates in a different order must give the same selection.
	reversed := make([]Entry, len(all))
	for i, e := range all {
		reversed[len(all)-1-i] = e
	}
	if again := selectEntries(reversed, 10, 1<<40); !reflect.DeepEqual(got, again) {
		t.Error("selection depends on candidate order")
	}
}

func TestSelectEntriesRespectsTotalCap(t *testing.T) {
	var all []Entry
	all = append(all, candidates(10, 1000, "small")...)
	all = append(all, candidates(10, 10<<20, "large")...)

	// Room for the four small files and exactly one large one.
	limit := int64(4*1000 + 10<<20 + 5)
	got := selectEntries(all, 10, limit)
	var total int64
	for _, e := range got {
		total += e.Size
	}
	if total > limit {
		t.Errorf("total %d exceeds cap %d", total, limit)
	}
	if s, m, l := BucketCounts(got); s != 4 || m != 0 || l != 1 {
		t.Errorf("buckets = %d/%d/%d, want 4/0/1", s, m, l)
	}
}
