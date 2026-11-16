// Package gif decodes GIF animations one frame at a time.
//
// Unlike image/gif it never holds more than one frame in memory, and it is
// built for inspecting files rather than just displaying them: it accepts
// the irregular files that browsers render, and reports each irregularity
// instead of hiding or rejecting it.
//
//	d, err := gif.NewDecoder(r, gif.Options{})
//	if err != nil {
//		return err
//	}
//	for frame, err := range d.Frames() {
//		if err != nil {
//			return err
//		}
//		// use frame
//	}
package gif

import (
	"bufio"
	"compress/lzw"
	"fmt"
	"image"
	"image/color"
	"io"
	"iter"
	"math"
	"time"
)

// Block introducers.
const (
	blockExtension = 0x21
	blockImage     = 0x2C
	blockTrailer   = 0x3B
)

// Extension labels.
const (
	extGraphicControl = 0xF9
	extComment        = 0xFE
	extApplication    = 0xFF
)

// Options configures a Decoder. The zero value is ready to use.
type Options struct {
	// Limits bounds what a file may make the decoder allocate and
	// compute. Fields left at zero use DefaultLimits.
	Limits Limits
}

// Header is the fixed information at the start of the file.
type Header struct {
	// Version is "87a" or "89a".
	Version string
	// Width and Height are the size of the canvas the frames are drawn on.
	Width, Height int
	// GlobalPalette is the colour table shared by frames without their
	// own, or nil if the file has none.
	GlobalPalette color.Palette
	// BackgroundIndex is the declared background colour. Viewers ignore it.
	BackgroundIndex uint8
	// PixelAspect is the declared pixel aspect ratio byte, usually 0.
	PixelAspect uint8
}

// FormatError reports input that is not a GIF this package can decode.
type FormatError struct {
	// Offset is the position in the input where the problem was found.
	Offset int64
	Msg    string
}

func (e *FormatError) Error() string {
	return fmt.Sprintf("gif: %s at offset %d", e.Msg, e.Offset)
}

// graphicControl is the per-frame extension that precedes an image.
type graphicControl struct {
	disposal    Disposal
	userInput   bool
	transparent int
	delay       time.Duration
}

// Decoder reads a GIF frame by frame. It buffers its input, so it may read
// past the end of the GIF in the underlying reader.
type Decoder struct {
	r        reader
	limits   Limits
	header   Header
	frames   int
	decoded  int64 // pixels decoded so far
	meta     Metadata
	metaLeft int64           // unspent part of Limits.MaxMetadataBytes
	gc       *graphicControl // waiting for the next image
	lzw      *lzw.Reader
	pix      []byte // backs Frame.Pixels
	rows     []byte // stream-order pixels of an interlaced frame
	err      error  // sticky result of Next
}

// NewDecoder reads the header from r.
//
// Decoding fails in three ways, here and in Next: input that stops part-way
// through a block gives an error wrapping io.ErrUnexpectedEOF, input that
// exceeds opts.Limits a *LimitError, and anything else that cannot be
// decoded a *FormatError.
func NewDecoder(r io.Reader, opts Options) (*Decoder, error) {
	limits := opts.Limits.withDefaults()
	d := &Decoder{
		r:        reader{br: bufio.NewReader(r)},
		limits:   limits,
		metaLeft: limits.MaxMetadataBytes,
	}
	if err := d.readHeader(); err != nil {
		return nil, err
	}
	return d, nil
}

// Header returns the file header.
func (d *Decoder) Header() Header { return d.header }

func (d *Decoder) readHeader() error {
	var b [13]byte
	if err := d.r.readFull(b[:6]); err != nil {
		return err
	}
	if sig := string(b[:6]); sig != "GIF87a" && sig != "GIF89a" {
		return &FormatError{Offset: 0, Msg: "not a GIF: bad signature"}
	}
	if err := d.r.readFull(b[6:]); err != nil {
		return err
	}
	d.header = Header{
		Version:         string(b[3:6]),
		Width:           le16(b[6:]),
		Height:          le16(b[8:]),
		BackgroundIndex: b[11],
		PixelAspect:     b[12],
	}
	if err := d.checkPixels("canvas pixels", d.header.Width, d.header.Height); err != nil {
		return err
	}
	if packed := b[10]; packed&0x80 != 0 {
		pal, err := d.readPalette(1 << (packed&7 + 1))
		if err != nil {
			return err
		}
		d.header.GlobalPalette = pal
	}
	return nil
}

func (d *Decoder) readPalette(n int) (color.Palette, error) {
	var buf [3 * 256]byte
	if err := d.r.readFull(buf[:3*n]); err != nil {
		return nil, err
	}
	pal := make(color.Palette, n)
	for i := range pal {
		pal[i] = color.RGBA{R: buf[3*i], G: buf[3*i+1], B: buf[3*i+2], A: 0xFF}
	}
	return pal, nil
}

// Next returns the next frame, or io.EOF after the last one. Once it has
// returned an error it keeps returning the same one.
//
// The frame's Pixels are overwritten by the following call.
func (d *Decoder) Next() (*Frame, error) {
	if d.err != nil {
		return nil, d.err
	}
	f, err := d.next()
	d.err = err
	return f, err
}

// Frames returns an iterator over the remaining frames. It stops after the
// last frame, or after yielding the first error.
func (d *Decoder) Frames() iter.Seq2[*Frame, error] {
	return func(yield func(*Frame, error) bool) {
		for {
			f, err := d.Next()
			if err == io.EOF {
				return
			}
			if !yield(f, err) || err != nil {
				return
			}
		}
	}
}

func (d *Decoder) next() (*Frame, error) {
	for {
		off := d.r.off
		c, err := d.r.ReadByte()
		if err == io.EOF {
			d.meta.MissingTrailer = true
			return nil, io.EOF
		}
		if err != nil {
			return nil, err
		}
		switch c {
		case blockExtension:
			if err := d.readExtension(off); err != nil {
				return nil, err
			}
		case blockImage:
			return d.readFrame(off)
		case blockTrailer:
			if err := d.scanTrailing(); err != nil {
				return nil, err
			}
			return nil, io.EOF
		default:
			// Not the start of any block. Like browsers, stop here and
			// show what came before; the rest is trailing data.
			d.meta.MissingTrailer = true
			if err := d.scanTrailing(c); err != nil {
				return nil, err
			}
			return nil, io.EOF
		}
	}
}

func (d *Decoder) readExtension(off int64) error {
	label, err := d.r.mustByte()
	if err != nil {
		return err
	}
	br := &blockReader{r: &d.r}
	switch label {
	case extGraphicControl:
		return d.readGraphicControl(br)
	case extComment:
		return d.readComment(br, off)
	case extApplication:
		return d.readApplication(br, off)
	}
	return d.readUnknown(br, label, off)
}

func (d *Decoder) readGraphicControl(br *blockReader) error {
	var b [4]byte
	n, _ := io.ReadFull(br, b[:])
	if br.err != nil {
		return br.err
	}
	// A block too short to hold the fields is ignored.
	if n == len(b) {
		gc := &graphicControl{
			disposal:    Disposal(b[0] >> 2 & 7),
			userInput:   b[0]&2 != 0,
			transparent: -1,
			delay:       time.Duration(le16(b[1:])) * 10 * time.Millisecond,
		}
		if b[0]&1 != 0 {
			gc.transparent = int(b[3])
		}
		d.gc = gc
	}
	_, err := br.drain()
	return err
}

// checkPixels applies Limits.MaxPixels to a w by h area. It also refuses an
// area whose size does not fit in an int, whatever the limit says.
func (d *Decoder) checkPixels(what string, w, h int) error {
	if n := int64(w) * int64(h); n > d.limits.MaxPixels || n > math.MaxInt {
		return &LimitError{Limit: what, Value: n, Max: d.limits.MaxPixels}
	}
	return nil
}

func (d *Decoder) readFrame(off int64) (*Frame, error) {
	if d.frames >= d.limits.MaxFrames {
		return nil, &LimitError{Limit: "frames", Value: int64(d.frames) + 1, Max: int64(d.limits.MaxFrames)}
	}
	var b [9]byte
	if err := d.r.readFull(b[:]); err != nil {
		return nil, err
	}
	x, y, w, h := le16(b[0:]), le16(b[2:]), le16(b[4:]), le16(b[6:])
	packed := b[8]
	f := &Frame{
		Index:       d.frames,
		Offset:      off,
		Rect:        image.Rect(x, y, x+w, y+h),
		Palette:     d.header.GlobalPalette,
		Interlaced:  packed&0x40 != 0,
		Transparent: -1,
	}
	f.OutOfCanvas = !f.Rect.In(image.Rect(0, 0, d.header.Width, d.header.Height))
	if packed&0x80 != 0 {
		pal, err := d.readPalette(1 << (packed&7 + 1))
		if err != nil {
			return nil, err
		}
		f.Palette, f.LocalPalette = pal, true
	}
	if gc := d.gc; gc != nil {
		f.Disposal, f.UserInput, f.Transparent, f.Delay = gc.disposal, gc.userInput, gc.transparent, gc.delay
		d.gc = nil
	}

	// The limits are checked before anything is allocated for the frame.
	if err := d.checkPixels("frame pixels", w, h); err != nil {
		return nil, err
	}
	n := w * h
	if total := d.decoded + int64(n); total > d.limits.MaxDecodedBytes {
		return nil, &LimitError{Limit: "decoded bytes", Value: total, Max: d.limits.MaxDecodedBytes}
	}
	d.decoded += int64(n)
	d.pix = grow(d.pix, n)
	f.Pixels = d.pix
	dst := f.Pixels
	if f.Interlaced {
		d.rows = grow(d.rows, n)
		dst = d.rows
	}
	missing, surplus, err := d.readPixels(dst)
	if err != nil {
		return nil, err
	}
	if f.Interlaced {
		deinterlace(f.Pixels, dst, w, h)
	}
	f.MissingPixels, f.SurplusData = missing, surplus
	f.IndexOutOfRange = indexOutOfRange(f)
	d.frames++
	return f, nil
}

// grow returns buf resized to n bytes, reallocating only when it is too small.
func grow(buf []byte, n int) []byte {
	if cap(buf) < n {
		return make([]byte, n)
	}
	return buf[:n]
}
