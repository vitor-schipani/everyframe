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
		Categories:  []string{DefaultCategory},
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

func TestSameSelection(t *testing.T) {
	base := func() *Manifest {
		return &Manifest{
			Source:      DefaultAPIURL,
			Categories:  []string{DefaultCategory},
			GeneratedAt: "2026-01-02T03:04:05Z",
			Selection:   Selection{Count: 1, MaxFileBytes: 10, MaxTotalBytes: 20},
			Entries:     []Entry{testEntry("File:a.gif", "https://example.org/a.gif", []byte("aaa"))},
		}
	}
	later := base()
	later.GeneratedAt = "2027-06-07T08:09:10Z"
	if !base().SameSelection(later) {
		t.Error("manifests differing only in generation time should match")
	}
	cases := map[string]func(*Manifest){
		"entry added": func(m *Manifest) {
			m.Entries = append(m.Entries, testEntry("File:b.gif", "https://example.org/b.gif", []byte("b")))
		},
		"entry changed": func(m *Manifest) { m.Entries[0].License = "CC BY 4.0" },
		"parameters":    func(m *Manifest) { m.Selection.Count = 2 },
		"category":      func(m *Manifest) { m.Categories = append(m.Categories, "Category:Other") },
		"still":         func(m *Manifest) { m.Selection.Still = true },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			other := base()
			mutate(other)
			if base().SameSelection(other) {
				t.Error("manifests should differ")
			}
		})
	}
}

func TestDir(t *testing.T) {
	t.Setenv(DirEnv, "")
	if got := Dir(); got != DefaultDir {
		t.Errorf("Dir() = %q, want %q", got, DefaultDir)
	}
	t.Setenv(DirEnv, "/somewhere/else")
	if got := Dir(); got != "/somewhere/else" {
		t.Errorf("Dir() = %q with %s set, want /somewhere/else", got, DirEnv)
	}
}

func TestTierDir(t *testing.T) {
	t.Setenv(DirEnv, "/standard")
	t.Setenv(ExtendedDirEnv, "")
	if got := Extended.Dir(); got != ExtendedDir {
		t.Errorf("Extended.Dir() = %q, want %q: %s must not move the extended tier", got, ExtendedDir, DirEnv)
	}
	if got := Standard.Dir(); got != "/standard" {
		t.Errorf("Standard.Dir() = %q, want /standard", got)
	}
	t.Setenv(ExtendedDirEnv, "/extended")
	if got := Extended.Dir(); got != "/extended" {
		t.Errorf("Extended.Dir() = %q with %s set, want /extended", got, ExtendedDirEnv)
	}
}

// TestCommittedManifests checks that the manifests in the repository load
// and that the tiers do not share files.
func TestCommittedManifests(t *testing.T) {
	const root = "../.."
	seen := map[string]string{}
	for _, tier := range []Tier{Standard, Extended} {
		m, err := Load(filepath.Join(root, tier.ManifestPath))
		if err != nil {
			t.Fatal(err)
		}
		if len(m.Entries) == 0 || len(m.Entries) > m.Selection.Count {
			t.Errorf("%s: %d entries for a selection of %d", tier.ManifestPath, len(m.Entries), m.Selection.Count)
		}
		if m.TotalBytes() > m.Selection.MaxTotalBytes {
			t.Errorf("%s: %d bytes, over the cap of %d", tier.ManifestPath, m.TotalBytes(), m.Selection.MaxTotalBytes)
		}
		for _, e := range m.Entries {
			if other, dup := seen[e.SHA1]; dup {
				t.Errorf("%s is in both the %s and the %s tier", e.Title, other, tier.Name)
			}
			seen[e.SHA1] = tier.Name
		}
	}
}
