package features

import (
	"image"
	"image/color"
	"testing"
)

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
