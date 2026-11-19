package features

import (
	"image"
	"image/color"
	"image/gif"
	"testing"
)

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
