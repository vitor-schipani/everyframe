package gif

import (
	"image"
	"image/color"
	"time"
)

// Disposal says what happens to a frame's area before the next frame is
// drawn. Values above DisposalPrevious are reserved by the format and are
// reported as found.
type Disposal uint8

const (
	// DisposalUnspecified leaves the choice to the viewer, which keeps the frame.
	DisposalUnspecified Disposal = iota
	// DisposalKeep leaves the frame in place.
	DisposalKeep
	// DisposalBackground clears the frame's area.
	DisposalBackground
	// DisposalPrevious restores what was there before the frame was drawn.
	DisposalPrevious
)

func (d Disposal) String() string {
	switch d {
	case DisposalUnspecified:
		return "unspecified"
	case DisposalKeep:
		return "keep"
	case DisposalBackground:
		return "background"
	case DisposalPrevious:
		return "previous"
	}
	return "reserved"
}

// Frame is one image of the animation exactly as stored: a rectangle of
// palette indices plus the instructions for showing it. Use a Canvas to get
// the picture a viewer would display.
type Frame struct {
	// Index counts frames from zero.
	Index int
	// Offset is the position in the input of the frame's image descriptor.
	Offset int64
	// Rect is the area the frame covers, as declared. It may extend past
	// the canvas; see OutOfCanvas.
	Rect image.Rectangle
	// Pixels holds one palette index per pixel of Rect, row by row from the
	// top, with interlacing already undone. The slice is reused: it is
	// valid only until the next call to Decoder.Next.
	Pixels []byte
	// Palette is the frame's own colour table if it has one, else the
	// global table, else nil.
	Palette color.Palette
	// LocalPalette reports whether Palette is the frame's own.
	LocalPalette bool
	// Interlaced reports whether the rows were stored in interlaced order.
	Interlaced bool
	// Transparent is the palette index that is not drawn, or -1.
	Transparent int
	// Disposal is applied after the frame has been shown.
	Disposal Disposal
	// Delay is how long the frame is shown. The format counts hundredths
	// of a second.
	Delay time.Duration
	// UserInput is the format's rarely honoured "wait for user input" flag.
	UserInput bool

	// The remaining fields flag irregularities that viewers tolerate.

	// MissingPixels is how many pixels the compressed data fell short by.
	// They are set to index 0.
	MissingPixels int
	// Truncated reports that the pixels are missing because the input
	// ended inside this frame. It is then the last frame, and the next
	// call to Decoder.Next returns the error.
	Truncated bool
	// SurplusData reports compressed data left over after the last pixel.
	SurplusData bool
	// OutOfCanvas reports that Rect is not contained in the canvas.
	OutOfCanvas bool
	// IndexOutOfRange reports a pixel, other than a transparent one, whose
	// index is past the end of Palette. That includes every pixel when
	// there is no palette at all.
	IndexOutOfRange bool
}

// Image returns the frame as a standalone image. The pixels are copied, so
// the result stays valid. Its palette is a copy in which the transparent
// index, and any index past the end of the frame's palette, is fully
// transparent.
func (f *Frame) Image() *image.Paletted {
	size := len(f.Palette)
	for _, p := range f.Pixels {
		size = max(size, int(p)+1)
	}
	pal := make(color.Palette, size)
	copy(pal, f.Palette)
	for i := len(f.Palette); i < size; i++ {
		pal[i] = color.RGBA{}
	}
	if f.Transparent >= 0 && f.Transparent < size {
		pal[f.Transparent] = color.RGBA{}
	}
	return &image.Paletted{
		Pix:     append([]byte(nil), f.Pixels...),
		Stride:  f.Rect.Dx(),
		Rect:    f.Rect,
		Palette: pal,
	}
}

// indexOutOfRange reports whether any drawn pixel has no palette entry.
func indexOutOfRange(f *Frame) bool {
	n := len(f.Palette)
	if n >= 256 {
		return false
	}
	for _, p := range f.Pixels {
		if int(p) >= n && int(p) != f.Transparent {
			return true
		}
	}
	return false
}
