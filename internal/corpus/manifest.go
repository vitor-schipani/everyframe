// Package corpus manages the real-world GIF corpus used by end-to-end tests.
//
// The corpus is a pinned list of freely licensed GIFs hosted on Wikimedia
// Commons. Only the manifest describing them is committed; the files are
// downloaded on demand into a git-ignored directory.
//
// It comes in two tiers. Standard is small enough to run on every `go test`.
// Extended is about ten times larger, drawn from more categories, and is only
// downloaded and tested on request.
package corpus

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

const (
	// DefaultManifestPath is the committed manifest, relative to the repo root.
	DefaultManifestPath = "testdata/corpus/manifest.json"
	// DefaultDir is where the files are downloaded, relative to the repo root.
	DefaultDir = "testdata/corpus/files"
	// DirEnv overrides DefaultDir when set.
	DirEnv = "EVERYFRAME_CORPUS_DIR"
	// DefaultUserAgent identifies the tool to Wikimedia, as their policy requires.
	DefaultUserAgent = "everyframe-gifcorpus/0.1 (+https://github.com/vitor-schipani/everyframe)"
)

const (
	// ExtendedManifestPath is the committed manifest of the extended tier.
	ExtendedManifestPath = "testdata/corpus/extended.json"
	// ExtendedDir is where the extended tier is downloaded.
	ExtendedDir = "testdata/corpus/extended"
	// ExtendedDirEnv overrides ExtendedDir when set.
	ExtendedDirEnv = "EVERYFRAME_CORPUS_EXTENDED_DIR"
	// ExtendedTestEnv, when set, makes the end-to-end tests also run against
	// the extended tier.
	ExtendedTestEnv = "EVERYFRAME_CORPUS_EXTENDED"
)

// Tier is one of the corpora: its manifest and where its files are kept.
type Tier struct {
	Name string
	// ManifestPath is relative to the repo root.
	ManifestPath string
	// MakeTarget is the make target that downloads the tier.
	MakeTarget string
	defaultDir string
	dirEnv     string
}

var (
	// Standard is the tier every `go test` runs against once downloaded.
	Standard = Tier{"standard", DefaultManifestPath, "corpus", DefaultDir, DirEnv}
	// Extended is the larger, more varied tier that is tested only on request.
	Extended = Tier{"extended", ExtendedManifestPath, "corpus-extended", ExtendedDir, ExtendedDirEnv}
)

// Dir is where the tier's files are kept: the directory named by its
// environment variable if that is set, else its default. A relative result
// is relative to the repo root.
func (t Tier) Dir() string {
	if dir := os.Getenv(t.dirEnv); dir != "" {
		return dir
	}
	return t.defaultDir
}

// DirEnv is the environment variable that overrides the tier's directory.
func (t Tier) DirEnv() string { return t.dirEnv }

// Dir is where the standard tier's files are kept.
func Dir() string { return Standard.Dir() }

// Manifest is the pinned list of corpus files.
type Manifest struct {
	Source string `json:"source"`
	// Categories are the Commons categories the files were sampled from.
	Categories  []string  `json:"categories"`
	GeneratedAt string    `json:"generated_at"`
	Selection   Selection `json:"selection"`
	Entries     []Entry   `json:"entries"`
}

// Selection records the parameters the manifest was generated with.
type Selection struct {
	Count         int   `json:"count"`
	MaxFileBytes  int64 `json:"max_file_bytes"`
	MaxTotalBytes int64 `json:"max_total_bytes"`
	// Still records that GIFs with a single frame were accepted too.
	Still bool `json:"still,omitempty"`
}

// Entry describes one corpus file.
type Entry struct {
	Title          string  `json:"title"`
	PageID         int64   `json:"page_id"`
	URL            string  `json:"url"`
	DescriptionURL string  `json:"description_url"`
	SHA1           string  `json:"sha1"`
	Size           int64   `json:"size"`
	Width          int     `json:"width"`
	Height         int     `json:"height"`
	Duration       float64 `json:"duration"`
	License        string  `json:"license"`
	Artist         string  `json:"artist,omitempty"`
}

// Filename is the name the entry is stored under locally. It is derived from
// the checksum because Commons titles contain quotes, slashes and spaces.
func (e Entry) Filename() string { return e.SHA1 + ".gif" }

// LocalPath is the entry's location inside dir.
func (e Entry) LocalPath(dir string) string { return filepath.Join(dir, e.Filename()) }

func (e Entry) validate() error {
	if !validSHA1(e.SHA1) {
		return fmt.Errorf("entry %q: sha1 %q is not 40 lowercase hex characters", e.Title, e.SHA1)
	}
	if !strings.HasPrefix(e.URL, "https://") && !strings.HasPrefix(e.URL, "http://") {
		return fmt.Errorf("entry %q: url %q is not an http(s) URL", e.Title, e.URL)
	}
	if e.Size <= 0 {
		return fmt.Errorf("entry %q: size must be positive, got %d", e.Title, e.Size)
	}
	return nil
}

func validSHA1(s string) bool {
	if len(s) != 40 || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// TotalBytes is the combined size of all entries.
func (m *Manifest) TotalBytes() int64 {
	var total int64
	for _, e := range m.Entries {
		total += e.Size
	}
	return total
}

// SameSelection reports whether other describes the same files with the same
// parameters, ignoring when each manifest was generated.
func (m *Manifest) SameSelection(other *Manifest) bool {
	a, b := *m, *other
	a.GeneratedAt, b.GeneratedAt = "", ""
	return reflect.DeepEqual(a, b)
}

// Load reads and validates a manifest.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	seen := make(map[string]bool, len(m.Entries))
	for _, e := range m.Entries {
		if err := e.validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if seen[e.SHA1] {
			return nil, fmt.Errorf("%s: duplicate sha1 %s", path, e.SHA1)
		}
		seen[e.SHA1] = true
	}
	return &m, nil
}

// Save writes the manifest as indented JSON, creating parent directories.
func (m *Manifest) Save(path string) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
