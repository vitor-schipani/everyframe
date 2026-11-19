package features

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	stdgif "image/gif"
	"io"
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/vitor-schipani/everyframe/gif"
)

var (
	black = color.RGBA{A: 0xFF}
	white = color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	red   = color.RGBA{R: 0xFF, A: 0xFF}
)

// frame builds a frame filled with one palette index.
func frame(index int, r image.Rectangle, pal color.Palette, fill byte) *gif.Frame {
	return &gif.Frame{
		Index:       index,
		Rect:        r,
		Pixels:      bytes.Repeat([]byte{fill}, r.Dx()*r.Dy()),
		Palette:     pal,
		Transparent: -1,
		Disposal:    gif.DisposalKeep,
	}
}

func header(w, h int) gif.Header { return gif.Header{Version: "89a", Width: w, Height: h} }

func TestStoredPixels(t *testing.T) {
	e := NewExtractor(header(10, 10))
	// A 6x4 frame whose right half hangs off the canvas.
	f := frame(0, image.Rect(7, 2, 13, 6), color.Palette{black, white, red, white}, 1)
	f.Transparent = 2
	// The first row is transparent, and one pixel inside the canvas has
	// an index with no colour.
	for x := range 6 {
		f.Pixels[x] = 2
	}
	f.Pixels[6] = 200
	f.Pixels[7] = 0

	got := e.Add(f)
	if got.StoredPixels != 24 || got.OutsidePixels != 12 || got.TransparentPixels != 6 {
		t.Errorf("stored %d, outside %d, transparent %d; want 24, 12, 6",
			got.StoredPixels, got.OutsidePixels, got.TransparentPixels)
	}
	// 12 pixels are inside; 3 of them are transparent and 1 has no colour.
	if got.DrawnPixels != 8 {
		t.Errorf("drawn = %d, want 8", got.DrawnPixels)
	}
	// Indices 0, 1 and 200 are used; 2 is the transparent one.
	if got.Colours != 3 {
		t.Errorf("colours = %d, want 3", got.Colours)
	}
	if got.ChangedPixels != got.DrawnPixels {
		t.Errorf("changed = %d, want the %d pixels drawn on the empty canvas", got.ChangedPixels, got.DrawnPixels)
	}
}

func TestChange(t *testing.T) {
	pal := color.Palette{black, white}
	e := NewExtractor(header(4, 4))

	// Black on an empty canvas changes only alpha: a quarter of the range.
	got := e.Add(frame(0, image.Rect(0, 0, 4, 4), pal, 0))
	if got.ChangedPixels != 16 || got.Change != 0.25 {
		t.Errorf("frame 0: changed %d, change %v; want 16, 0.25", got.ChangedPixels, got.Change)
	}

	// The same picture again changes nothing.
	got = e.Add(frame(1, image.Rect(0, 0, 4, 4), pal, 0))
	if got.ChangedPixels != 0 || got.Change != 0 || got.HashDistance != 0 {
		t.Errorf("frame 1: changed %d, change %v, hash distance %d; want all 0",
			got.ChangedPixels, got.Change, got.HashDistance)
	}

	// A 2x2 white patch changes 4 of 16 pixels in 3 of 4 channels. It is
	// cleared afterwards.
	f := frame(2, image.Rect(1, 1, 3, 3), pal, 1)
	f.Disposal = gif.DisposalBackground
	got = e.Add(f)
	if want := 4.0 * 3 / (16 * 4); got.ChangedPixels != 4 || got.Change != want {
		t.Errorf("frame 2: changed %d, change %v; want 4, %v", got.ChangedPixels, got.Change, want)
	}

	// A frame elsewhere that repaints what is already there still shows
	// the clearing of the patch: 4 pixels from white to transparent.
	got = e.Add(frame(3, image.Rect(0, 0, 1, 1), pal, 0))
	if want := 4.0 * 4 / (16 * 4); got.ChangedPixels != 4 || got.Change != want {
		t.Errorf("frame 3: changed %d, change %v; want 4, %v", got.ChangedPixels, got.Change, want)
	}
	if a := e.Shown().RGBAAt(1, 1).A; a != 0 {
		t.Errorf("Shown() has alpha %d at (1,1), want the cleared patch", a)
	}
	if c := e.Shown().RGBAAt(0, 0); c != black {
		t.Errorf("Shown() at (0,0) = %v, want black", c)
	}
}

func TestHash(t *testing.T) {
	pal := color.Palette{black, white}
	// The edge between the halves falls between two cells of the grid.
	full := image.Rect(0, 0, 72, 64)
	left, right := image.Rect(0, 0, 40, 64), image.Rect(40, 0, 72, 64)

	e := NewExtractor(header(72, 64))
	e.Add(frame(0, full, pal, 0))
	bright := e.Add(frame(1, left, pal, 1)) // white | black
	e.Add(frame(2, full, pal, 0))
	dark := e.Add(frame(3, right, pal, 1)) // black | white

	// Each row has one step down in brightness, or none.
	if n := bright.Hash.Distance(0); n != 8 {
		t.Errorf("white|black hash %v has %d bits set, want 8", bright.Hash, n)
	}
	if dark.Hash != 0 {
		t.Errorf("black|white hash = %v, want 0", dark.Hash)
	}
	if dark.HashDistanceFirst != 0 || dark.HashDistance != 0 {
		t.Errorf("black|white is %d from the first frame and %d from the previous, want 0 and 0: both are flat or rising",
			dark.HashDistanceFirst, dark.HashDistance)
	}
	if got := bright.Hash.String(); len(got) != 16 {
		t.Errorf("String() = %q, want 16 digits", got)
	}

	// The hash does not depend on the size of the picture.
	small := NewExtractor(header(18, 8))
	small.Add(frame(0, image.Rect(0, 0, 18, 8), pal, 0))
	if got := small.Add(frame(1, image.Rect(0, 0, 10, 8), pal, 1)); got.Hash != bright.Hash {
		t.Errorf("18x8 hash = %v, 72x64 hash = %v, want the same", got.Hash, bright.Hash)
	}

	// A picture smaller than the grid is hashed as if stretched to fit:
	// two pixels, white and black, still give one step down in each row.
	tiny := NewExtractor(header(2, 1))
	tiny.Add(frame(0, image.Rect(0, 0, 2, 1), pal, 0))
	if got := tiny.Add(frame(1, image.Rect(0, 0, 1, 1), pal, 1)); got.Hash.Distance(0) != 8 {
		t.Errorf("2x1 white|black hash = %v, want 8 bits set", got.Hash)
	}
}

// referenceHash hashes a picture from scratch, the slow way.
func referenceHash(img *image.RGBA) Hash {
	b := img.Bounds()
	// cells returns, for each of n cells, which of size pixels fall in
	// it. A cell with none borrows those of the cell before it.
	cells := func(n, size int) [][]int {
		out := make([][]int, n)
		for p := range size {
			out[p*n/size] = append(out[p*n/size], p)
		}
		for i := 1; i < n; i++ {
			if len(out[i]) == 0 {
				out[i] = out[i-1]
			}
		}
		return out
	}
	cols, rows := cells(hashCols, b.Dx()), cells(hashRows, b.Dy())
	var h Hash
	for _, ys := range rows {
		var mean [hashCols]float64
		for c, xs := range cols {
			for _, y := range ys {
				for _, x := range xs {
					p := img.RGBAAt(x, y)
					mean[c] += 299*float64(p.R) + 587*float64(p.G) + 114*float64(p.B)
				}
			}
			mean[c] /= float64(len(xs) * len(ys))
		}
		for c := range hashCols - 1 {
			h <<= 1
			if mean[c] > mean[c+1] {
				h |= 1
			}
		}
	}
	return h
}

// The features that are kept up to date incrementally must match what a
// fresh look at the whole canvas gives, whatever the frames do.
func TestIncrementalMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for _, size := range []image.Point{{40, 30}, {5, 3}, {1, 1}, {13, 200}} {
		pal := make(color.Palette, 8)
		for i := range pal {
			pal[i] = color.RGBA{R: uint8(rng.UintN(256)), G: uint8(rng.UintN(256)), B: uint8(rng.UintN(256)), A: 0xFF}
		}
		e := NewExtractor(header(size.X, size.Y))
		canvas := gif.NewCanvas(header(size.X, size.Y))
		before := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))

		for i := range 60 {
			// Rectangles may reach past the canvas on the right and bottom.
			x0, y0 := rng.IntN(size.X), rng.IntN(size.Y)
			r := image.Rect(x0, y0, x0+1+rng.IntN(size.X), y0+1+rng.IntN(size.Y))
			f := frame(i, r, pal, 0)
			for j := range f.Pixels {
				f.Pixels[j] = byte(rng.IntN(10)) // 8 and 9 have no colour
			}
			f.Transparent = rng.IntN(9) - 1
			f.Disposal = gif.Disposal(rng.IntN(5))

			got := e.Add(f)
			want := canvas.Draw(f)
			if !bytes.Equal(e.Shown().Pix, want.Pix) {
				t.Fatalf("%v frame %d: Shown() differs from the canvas", size, i)
			}
			if h := referenceHash(want); got.Hash != h {
				t.Fatalf("%v frame %d: hash %v, want %v", size, i, got.Hash, h)
			}
			var changed, diff int64
			for p := 0; p < len(want.Pix); p += 4 {
				if !bytes.Equal(want.Pix[p:p+4], before.Pix[p:p+4]) {
					changed++
				}
				for c := range 4 {
					diff += absDiff(want.Pix[p+c], before.Pix[p+c])
				}
			}
			if got.ChangedPixels != changed {
				t.Fatalf("%v frame %d: changed %d, want %d", size, i, got.ChangedPixels, changed)
			}
			if wantChange := float64(diff) / float64(255*len(want.Pix)); math.Abs(got.Change-wantChange) > 1e-12 {
				t.Fatalf("%v frame %d: change %v, want %v", size, i, got.Change, wantChange)
			}
			copy(before.Pix, want.Pix)
		}
	}
}

// A canvas with no area has nothing to measure, and must not divide by zero.
func TestEmptyCanvas(t *testing.T) {
	e := NewExtractor(header(0, 0))
	got := e.Add(frame(0, image.Rect(0, 0, 2, 2), color.Palette{black, white}, 1))
	if got.StoredPixels != 4 || got.OutsidePixels != 4 || got.DrawnPixels != 0 || got.Change != 0 || got.Hash != 0 {
		t.Errorf("features = %+v", got)
	}
}

// animation encodes three 8x6 frames: white, white with a red 3x2 patch,
// and black.
func animation(t *testing.T) []byte {
	t.Helper()
	pal := color.Palette{black, white, red, color.RGBA{B: 0xFF, A: 0xFF}}
	g := &stdgif.GIF{LoopCount: 3}
	for i, fr := range []struct {
		r    image.Rectangle
		fill uint8
	}{{image.Rect(0, 0, 8, 6), 1}, {image.Rect(2, 1, 5, 3), 2}, {image.Rect(0, 0, 8, 6), 0}} {
		img := image.NewPaletted(fr.r, pal)
		for j := range img.Pix {
			img.Pix[j] = fr.fill
		}
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, 10*i)
		g.Disposal = append(g.Disposal, stdgif.DisposalNone)
	}
	var buf bytes.Buffer
	if err := stdgif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtract(t *testing.T) {
	data := append(animation(t), "trailing"...)
	d, err := gif.NewDecoder(bytes.NewReader(data), gif.Options{})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Extract(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Frames) != 3 {
		t.Fatalf("%d frames, want 3", len(rep.Frames))
	}
	if f := rep.Frames[1]; f.Index != 1 || f.Delay != 100*time.Millisecond || f.StoredPixels != 6 || f.ChangedPixels != 6 {
		t.Errorf("frame 1 = %+v", f)
	}

	s := rep.Summary
	if s.Width != 8 || s.Height != 6 || s.Frames != 3 {
		t.Errorf("summary is of a %dx%d animation of %d frames", s.Width, s.Height, s.Frames)
	}
	if s.Duration != 300*time.Millisecond || s.MinDelay != 0 || s.MaxDelay != 200*time.Millisecond || s.ZeroDelayFrames != 1 {
		t.Errorf("duration %v, delays %v to %v, %d zero", s.Duration, s.MinDelay, s.MaxDelay, s.ZeroDelayFrames)
	}
	if !s.HasLoopCount || s.LoopCount != 3 {
		t.Errorf("loop count %d (set: %v), want 3", s.LoopCount, s.HasLoopCount)
	}
	// White to black is the largest change, and changes all three colour
	// channels of every pixel outside the red patch.
	if s.MaxChangeFrame != 2 || s.MaxChange != rep.Frames[2].Change || s.MaxChange < 0.6 {
		t.Errorf("largest change %v at frame %d", s.MaxChange, s.MaxChangeFrame)
	}
	if s.StoredPixels != 48+6+48 || s.DrawnPixels != s.StoredPixels || s.OutsidePixels != 0 {
		t.Errorf("stored %d, drawn %d, outside %d", s.StoredPixels, s.DrawnPixels, s.OutsidePixels)
	}
	if s.Applications != 1 || s.ApplicationBytes == 0 || s.TrailingBytes != 8 || s.MissingTrailer || s.Truncated {
		t.Errorf("container: %d applications of %d bytes, %d trailing bytes, missing trailer %v, truncated %v",
			s.Applications, s.ApplicationBytes, s.TrailingBytes, s.MissingTrailer, s.Truncated)
	}
	// Flat white and flat black hash alike; the red patch does not.
	if rep.Frames[0].Hash != 0 || s.DistinctHashes != 2 || s.MaxHashDistanceFirstFrame != 1 || s.MaxHashDistanceFirst == 0 {
		t.Errorf("%d distinct hashes, furthest from the first is frame %d at %d",
			s.DistinctHashes, s.MaxHashDistanceFirstFrame, s.MaxHashDistanceFirst)
	}
}

// A file that stops part-way is measured as far as it goes.
func TestExtractTruncated(t *testing.T) {
	data := animation(t)
	d, err := gif.NewDecoder(bytes.NewReader(data[:len(data)-12]), gif.Options{})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Extract(d)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("err = %v, want one wrapping io.ErrUnexpectedEOF", err)
	}
	if n := len(rep.Frames); n < 2 || rep.Summary.Frames != n || !rep.Summary.HasLoopCount {
		t.Errorf("%d frames, summary %+v", n, rep.Summary)
	}
}

// The irregularities the decoder reports on frames are counted.
func TestSummaryIrregularities(t *testing.T) {
	pal := color.Palette{black, white}
	e := NewExtractor(header(4, 4))
	e.Add(frame(0, image.Rect(0, 0, 4, 4), pal, 0))
	f := frame(1, image.Rect(2, 2, 6, 6), pal, 1)
	f.LocalPalette, f.OutOfCanvas, f.SurplusData, f.IndexOutOfRange = true, true, true, true
	e.Add(f)
	f = frame(2, image.Rect(0, 0, 4, 4), pal, 1)
	f.MissingPixels, f.Truncated = 5, true
	e.Add(f)

	s := e.Summary(gif.Metadata{
		Comments:          []gif.Comment{{Size: 10}, {Size: 5}},
		UnknownExtensions: []gif.Extension{{Size: 7}},
		DroppedExtensions: 2,
		MissingTrailer:    true,
	})
	if s.LocalPaletteFrames != 1 || s.OutOfCanvasFrames != 1 || s.SurplusDataFrames != 1 ||
		s.IndexOutOfRangeFrames != 1 || s.MissingPixelsFrames != 1 || !s.Truncated {
		t.Errorf("frame counts: %+v", s)
	}
	if s.OutsidePixels != 12 || s.HasLoopCount {
		t.Errorf("outside %d, loop count set %v", s.OutsidePixels, s.HasLoopCount)
	}
	if s.Comments != 2 || s.CommentBytes != 15 || s.UnknownExtensions != 1 || s.UnknownExtensionBytes != 7 ||
		s.DroppedExtensions != 2 || !s.MissingTrailer {
		t.Errorf("container: %+v", s)
	}
}

func TestSummaryOfNothing(t *testing.T) {
	s := NewExtractor(header(3, 3)).Summary(gif.Metadata{})
	if s.Frames != 0 || s.MinDelay != 0 || s.DistinctHashes != 0 || s.MaxChangeFrame != 0 {
		t.Errorf("summary = %+v", s)
	}
}
