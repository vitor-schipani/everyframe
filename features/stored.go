package features

import (
	"image"

	"github.com/vitor-schipani/everyframe/gif"
)

// measureStored counts the kinds of pixel the frame stores. area is the part
// of the frame inside the canvas.
func (e *Extractor) measureStored(f *gif.Frame, area image.Rectangle, out *Frame) {
	var used [256]int64
	for _, p := range f.Pixels {
		used[p]++
	}
	for i, n := range used {
		switch {
		case i == f.Transparent:
			out.TransparentPixels = n
		case n > 0:
			out.Colours++
		}
	}

	// The same rule as gif.Canvas: a pixel is drawn if it is inside the
	// canvas, not transparent, and its index has a colour.
	var drawn [256]bool
	for i := range min(len(f.Palette), 256) {
		drawn[i] = i != f.Transparent
	}
	w := f.Rect.Dx()
	for y := area.Min.Y; y < area.Max.Y; y++ {
		start := (y-f.Rect.Min.Y)*w + area.Min.X - f.Rect.Min.X
		if start+area.Dx() > len(f.Pixels) {
			break // a frame built with fewer pixels than its rectangle
		}
		for _, p := range f.Pixels[start : start+area.Dx()] {
			if drawn[p] {
				out.DrawnPixels++
			}
		}
	}
}
