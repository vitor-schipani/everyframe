package features

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/vitor-schipani/everyframe/gif"
	"github.com/vitor-schipani/everyframe/internal/corpus"
)

// TestCorpus measures every file of the real-world corpus, and checks the
// result against what the frames and a second, independent canvas say.
func TestCorpus(t *testing.T) { testCorpus(t, corpus.Standard) }

// TestExtendedCorpus does the same for the extended corpus, which is large
// enough that it only runs when asked for.
func TestExtendedCorpus(t *testing.T) {
	if os.Getenv(corpus.ExtendedTestEnv) == "" {
		t.Skipf("set %s=1 to test the extended corpus", corpus.ExtendedTestEnv)
	}
	testCorpus(t, corpus.Extended)
}

func testCorpus(t *testing.T, tier corpus.Tier) {
	if testing.Short() {
		t.Skip("the corpus test is skipped in -short mode")
	}
	const root = ".." // tests run in the package directory
	dir := tier.Dir()
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("%s corpus not downloaded (%v); run `make %s` in the repo root", tier.Name, err, tier.MakeTarget)
	}
	m, err := corpus.Load(filepath.Join(root, tier.ManifestPath))
	if err != nil {
		t.Fatal(err)
	}

	var files, frames atomic.Int64
	// The group returns once all its parallel subtests have finished.
	t.Run("files", func(t *testing.T) {
		for _, e := range m.Entries {
			t.Run(e.SHA1[:12], func(t *testing.T) {
				t.Parallel()
				n := checkCorpusFile(t, e, e.LocalPath(dir))
				files.Add(1)
				frames.Add(int64(n))
			})
		}
	})
	t.Logf("%d files, %d frames", files.Load(), frames.Load())
	if files.Load() == 0 {
		t.Errorf("no corpus file found in %s", dir)
	}
}

// checkCorpusFile returns the number of frames measured.
func checkCorpusFile(t *testing.T, entry corpus.Entry, path string) int {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("not downloaded")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()

	d, err := gif.NewDecoder(file, gif.Options{})
	if err != nil {
		t.Fatalf("%s: %v", entry.Title, err)
	}
	h := d.Header()
	e := NewExtractor(h)
	canvas := gif.NewCanvas(h)
	area := int64(h.Width) * int64(h.Height)

	var sum Summary
	n := 0
	for f, err := range d.Frames() {
		// Real files do get cut short, and the corpus has one.
		if errors.Is(err, io.ErrUnexpectedEOF) {
			break
		}
		if err != nil {
			t.Fatalf("%s: frame %d: %v", entry.Title, n, err)
		}
		ff := e.Add(f)
		want := canvas.Draw(f)
		n++

		if ff.Index != f.Index || ff.Delay != f.Delay {
			t.Fatalf("%s: frame %d reported as frame %d with delay %v", entry.Title, f.Index, ff.Index, ff.Delay)
		}
		if ff.StoredPixels != int64(len(f.Pixels)) {
			t.Errorf("%s: frame %d: stored %d, the frame has %d pixels", entry.Title, f.Index, ff.StoredPixels, len(f.Pixels))
		}
		if inside := ff.StoredPixels - ff.OutsidePixels; ff.DrawnPixels > inside || ff.DrawnPixels < inside-ff.TransparentPixels && !f.IndexOutOfRange {
			t.Errorf("%s: frame %d: %d drawn of %d inside the canvas with %d transparent",
				entry.Title, f.Index, ff.DrawnPixels, inside, ff.TransparentPixels)
		}
		if ff.ChangedPixels < 0 || ff.ChangedPixels > area || ff.Change < 0 || ff.Change > 1 || (ff.ChangedPixels == 0) != (ff.Change == 0) {
			t.Errorf("%s: frame %d: %d pixels changed by %v on a canvas of %d", entry.Title, f.Index, ff.ChangedPixels, ff.Change, area)
		}
		if ff.ChangedPixels == 0 && ff.HashDistance != 0 {
			t.Errorf("%s: frame %d: hash moved by %d with no pixel changed", entry.Title, f.Index, ff.HashDistance)
		}
		// Comparing whole pictures is slow, so it is done for a sample.
		if n <= 3 || n%50 == 0 {
			if !bytes.Equal(e.Shown().Pix, want.Pix) {
				t.Fatalf("%s: frame %d: Shown() differs from the canvas", entry.Title, f.Index)
			}
			if got := referenceHash(want); ff.Hash != got {
				t.Fatalf("%s: frame %d: hash %v, want %v", entry.Title, f.Index, ff.Hash, got)
			}
		}
		sum.Duration += f.Delay
		sum.StoredPixels += int64(len(f.Pixels))
	}
	if n == 0 {
		t.Errorf("%s: no frames", entry.Title)
	}

	got := e.Summary(d.Metadata())
	if got.Frames != n || got.Duration != sum.Duration || got.StoredPixels != sum.StoredPixels {
		t.Errorf("%s: summary has %d frames lasting %v with %d pixels; want %d, %v, %d",
			entry.Title, got.Frames, got.Duration, got.StoredPixels, n, sum.Duration, sum.StoredPixels)
	}
	if got.DistinctHashes < 1 || got.DistinctHashes > n || got.MaxChange < 0 || got.MaxChange > 1 {
		t.Errorf("%s: %d distinct hashes over %d frames, largest change %v", entry.Title, got.DistinctHashes, n, got.MaxChange)
	}
	return n
}
