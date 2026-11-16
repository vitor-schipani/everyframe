package corpus

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// testEntry builds an entry whose checksum and size match content.
func testEntry(title, url string, content []byte) Entry {
	sum := sha1.Sum(content)
	return Entry{
		Title:    title,
		URL:      url,
		SHA1:     hex.EncodeToString(sum[:]),
		Size:     int64(len(content)),
		Duration: 1,
		License:  "CC0",
	}
}

func TestManifestRoundTrip(t *testing.T) {
	want := &Manifest{
		Source:      DefaultAPIURL,
		Category:    DefaultCategory,
		GeneratedAt: "2026-01-02T03:04:05Z",
		Selection:   Selection{Count: 2, MaxFileBytes: 10, MaxTotalBytes: 20},
		Entries: []Entry{
			testEntry(`File:"a" & <b>.gif`, "https://example.org/a.gif", []byte("aaa")),
			testEntry("File:b.gif", "https://example.org/b.gif", []byte("bbbb")),
		},
	}
	path := filepath.Join(t.TempDir(), "nested", "manifest.json")
	if err := want.Save(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `& <b>`) {
		t.Errorf("titles should be stored without HTML escaping, got:\n%s", raw)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
	if got.TotalBytes() != 7 {
		t.Errorf("TotalBytes = %d, want 7", got.TotalBytes())
	}
}

func TestLocalPath(t *testing.T) {
	e := testEntry("File:x.gif", "https://example.org/x.gif", []byte("x"))
	want := filepath.Join("dir", e.SHA1+".gif")
	if got := e.LocalPath("dir"); got != want {
		t.Errorf("LocalPath = %q, want %q", got, want)
	}
}

func TestLoadRejectsInvalidEntries(t *testing.T) {
	good := testEntry("File:x.gif", "https://example.org/x.gif", []byte("x"))
	cases := map[string]func(*Manifest){
		"path-like sha1": func(m *Manifest) { m.Entries[0].SHA1 = "../../../../../../../../../../etc/passwd0000" },
		"short sha1":     func(m *Manifest) { m.Entries[0].SHA1 = "abc" },
		"uppercase sha1": func(m *Manifest) { m.Entries[0].SHA1 = strings.ToUpper(good.SHA1) },
		"non-http url":   func(m *Manifest) { m.Entries[0].URL = "file:///etc/passwd" },
		"zero size":      func(m *Manifest) { m.Entries[0].Size = 0 },
		"duplicate sha1": func(m *Manifest) { m.Entries = append(m.Entries, m.Entries[0]) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := &Manifest{Entries: []Entry{good}}
			mutate(m)
			path := filepath.Join(t.TempDir(), "manifest.json")
			if err := m.Save(path); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Error("Load accepted an invalid manifest")
			}
		})
	}
}
