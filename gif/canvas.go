package gif

import (
	"image"
	"image/color"
)

// Canvas turns the stored frames into the pictures a viewer shows, by
// drawing each frame over what earlier frames left behind. Feed it every
// frame in order:
//
//	canvas := gif.NewCanvas(d.Header())
//	for frame, err := range d.Frames() {
//		...
//		shown := canvas.Draw(frame)
//	}
//
// It follows what browsers do where they differ from the specification: the
// canvas starts out transparent, and the background colour is never used.
type Canvas struct {
	img *image.RGBA

	// How to undo the last frame drawn, before drawing the next one.
	disposal Disposal
	area     image.Rectangle // the part of the canvas that frame covered
	saved    []byte          // the contents of area before it, for DisposalPrevious
}

// NewCanvas returns an empty, fully transparent canvas of the size given in
// the header. It allocates four bytes per pixel, and as much again once a
// frame uses DisposalPrevious.
func NewCanvas(h Header) *Canvas {
	return &Canvas{img: image.NewRGBA(image.Rect(0, 0, h.Width, h.Height))}
}

// Draw adds f to the canvas and returns the result, which is what a viewer
// displays for the duration of f. The returned image is the canvas itself:
// it changes on the next call to Draw, so copy it to keep it.
//
// The part of a frame that lies outside the canvas is not drawn, and neither
// are transparent pixels or pixels whose index has no palette entry.
func (c *Canvas) Draw(f *Frame) *image.RGBA {
	c.dispose()

	area := f.Rect.Intersect(c.img.Bounds())
	// Browsers treat the reserved value 4 as DisposalPrevious too.
	disposal := f.Disposal
	if disposal == 4 {
		disposal = DisposalPrevious
	}
	if disposal == DisposalPrevious {
		c.save(area)
	}
	c.disposal, c.area = disposal, area

	var pal [256][4]byte
	var opaque [256]bool
	for i, col := range f.Palette[:min(len(f.Palette), 256)] {
		if i == f.Transparent {
			continue
		}
		rgba := color.RGBAModel.Convert(col).(color.RGBA)
		pal[i], opaque[i] = [4]byte{rgba.R, rgba.G, rgba.B, rgba.A}, true
	}

	w := f.Rect.Dx()
	for y := area.Min.Y; y < area.Max.Y; y++ {
		start := (y-f.Rect.Min.Y)*w + area.Min.X - f.Rect.Min.X
		if start+area.Dx() > len(f.Pixels) {
			break // a frame built with fewer pixels than its rectangle
		}
		src := f.Pixels[start : start+area.Dx()]
		dst := c.row(area, y)
		for x, idx := range src {
			if opaque[idx] {
				copy(dst[4*x:4*x+4], pal[idx][:])
			}
		}
	}
	return c.img
}

// row returns the pixels of line y of the canvas that lie inside area.
func (c *Canvas) row(area image.Rectangle, y int) []byte {
	i := c.img.PixOffset(area.Min.X, y)
	return c.img.Pix[i : i+4*area.Dx()]
}

// dispose undoes the previous frame as its disposal method asks.
func (c *Canvas) dispose() {
	switch c.disposal {
	case DisposalBackground:
		for y := c.area.Min.Y; y < c.area.Max.Y; y++ {
			clear(c.row(c.area, y))
		}
	case DisposalPrevious:
		n := 4 * c.area.Dx()
		for i, y := 0, c.area.Min.Y; y < c.area.Max.Y; i, y = i+n, y+1 {
			copy(c.row(c.area, y), c.saved[i:i+n])
		}
	}
}

// save remembers the current contents of area.
func (c *Canvas) save(area image.Rectangle) {
	c.saved = c.saved[:0]
	for y := area.Min.Y; y < area.Max.Y; y++ {
		c.saved = append(c.saved, c.row(area, y)...)
	}
}
