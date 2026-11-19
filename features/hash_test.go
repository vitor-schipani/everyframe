package features

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/vitor-schipani/everyframe/gif"
)

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
