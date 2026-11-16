package gif

import (
	"errors"
	"hash/crc32"
	"image"
	stdgif "image/gif"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/vitor-schipani/everyframe/internal/corpus"
)

// crossCheckBudget is the most decoded pixel data a corpus file may have for
// it to also be decoded with image/gif, which holds every frame in memory.
const crossCheckBudget = 256 << 20

// frameSummary is what the cross-check compares for each frame.
type frameSummary struct {
	rect     image.Rectangle
	delay    time.Duration
	disposal Disposal
	pixels   uint32 // CRC-32 of the palette indices
}

// corpusStats totals what the corpus test saw, for its closing log line.
type corpusStats struct {
	sync.Mutex
	files, frames         int
	truncated             int // files that end inside a block
	crossChecked          int // files image/gif agreed on
	stdRejected, tooLarge int // files it could not be compared on
	irregularFiles        int
}

// TestCorpus decodes and composites every file of the real-world corpus, and
// compares the result with image/gif where that decoder accepts the file.
func TestCorpus(t *testing.T) {
	if testing.Short() {
		t.Skip("the corpus test is skipped in -short mode")
	}
	const root = ".." // tests run in the package directory
	dir := corpus.Dir()
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("corpus not downloaded (%v); run `make corpus` in the repo root", err)
	}
	m, err := corpus.Load(filepath.Join(root, corpus.DefaultManifestPath))
	if err != nil {
		t.Fatal(err)
	}

	var stats corpusStats
	// The group returns once all its parallel subtests have finished.
	t.Run("files", func(t *testing.T) {
		for _, e := range m.Entries {
			t.Run(e.SHA1[:12], func(t *testing.T) {
				t.Parallel()
				checkCorpusFile(t, e, e.LocalPath(dir), &stats)
			})
		}
	})
	t.Logf("%d files, %d frames; %d files truncated; image/gif agreed on %d files, rejected %d, %d too large to compare; %d files with irregular frames or structure",
		stats.files, stats.frames, stats.truncated, stats.crossChecked, stats.stdRejected, stats.tooLarge, stats.irregularFiles)
	if stats.files == 0 {
		t.Errorf("no corpus file found in %s", dir)
	}
}

func checkCorpusFile(t *testing.T, e corpus.Entry, path string, stats *corpusStats) {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("not downloaded")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()

	d, err := NewDecoder(file, Options{})
	if err != nil {
		t.Fatalf("%s: %v", e.Title, err)
	}
	h := d.Header()
	if h.Width != e.Width || h.Height != e.Height {
		t.Errorf("%s: canvas is %dx%d, manifest says %dx%d", e.Title, h.Width, h.Height, e.Width, e.Height)
	}

	canvas := NewCanvas(h)
	var frames []frameSummary
	var decoded int64
	irregular := false
	truncated := false
	for f, err := range d.Frames() {
		// Real files do get cut short, and the corpus has one. All the
		// frames before the cut must still have been delivered.
		if errors.Is(err, io.ErrUnexpectedEOF) {
			t.Logf("%s: truncated after %d frames (%v)", e.Title, len(frames), err)
			truncated = true
			break
		}
		if err != nil {
			t.Fatalf("%s: frame %d: %v", e.Title, len(frames), err)
		}
		if shown := canvas.Draw(f); shown.Bounds() != image.Rect(0, 0, h.Width, h.Height) {
			t.Fatalf("%s: frame %d: canvas bounds are %v", e.Title, f.Index, shown.Bounds())
		}
		frames = append(frames, frameSummary{f.Rect, f.Delay, f.Disposal, crc32.ChecksumIEEE(f.Pixels)})
		decoded += int64(len(f.Pixels))
		if f.MissingPixels > 0 || f.SurplusData || f.OutOfCanvas || f.IndexOutOfRange {
			irregular = true
		}
	}
	if len(frames) == 0 {
		t.Errorf("%s: no frames", e.Title)
	}
	meta := d.Metadata()
	if meta.MissingTrailer || meta.TrailingBytes > 0 || meta.DroppedExtensions > 0 {
		irregular = true
	}

	stats.Lock()
	stats.files++
	stats.frames += len(frames)
	if irregular {
		stats.irregularFiles++
	}
	if truncated {
		stats.truncated++
	}
	stats.Unlock()

	if truncated {
		// image/gif gives up on such a file, so there is nothing to
		// compare, but it must agree that the file is cut short.
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		if _, err := stdgif.DecodeAll(file); err == nil {
			t.Errorf("%s: reported as truncated, but image/gif decodes it", e.Title)
		}
		return
	}
	if decoded > crossCheckBudget {
		stats.Lock()
		stats.tooLarge++
		stats.Unlock()
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	std, err := stdgif.DecodeAll(file)
	if err != nil {
		t.Logf("%s: image/gif rejects it (%v); not compared", e.Title, err)
		stats.Lock()
		stats.stdRejected++
		stats.Unlock()
		return
	}
	if len(std.Image) != len(frames) {
		t.Fatalf("%s: %d frames, image/gif has %d", e.Title, len(frames), len(std.Image))
	}
	for i, got := range frames {
		want := frameSummary{
			rect:     std.Image[i].Rect,
			delay:    time.Duration(std.Delay[i]) * 10 * time.Millisecond,
			disposal: Disposal(std.Disposal[i]),
			pixels:   crc32.ChecksumIEEE(std.Image[i].Pix),
		}
		if got != want {
			t.Errorf("%s: frame %d = %+v, image/gif has %+v", e.Title, i, got, want)
		}
	}
	// image/gif reports -1 when the file has no loop extension.
	if loops := std.LoopCount; meta.HasLoopCount != (loops >= 0) || (meta.HasLoopCount && meta.LoopCount != loops) {
		t.Errorf("%s: loop count %d (present %v), image/gif has %d", e.Title, meta.LoopCount, meta.HasLoopCount, loops)
	}
	stats.Lock()
	stats.crossChecked++
	stats.Unlock()
}
