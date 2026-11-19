// Package features measures an animation frame by frame, producing the
// numbers a detection system needs to tell whether something is hidden in
// it: how long each frame is shown, how much of what it stores becomes
// visible, how far the picture moves from one frame to the next, and a
// perceptual hash of every picture shown.
//
// It reports measurements, not verdicts. Which values are suspicious is a
// matter of policy that belongs to the caller.
//
//	d, err := gif.NewDecoder(r, gif.Options{})
//	if err != nil {
//		return err
//	}
//	report, err := features.Extract(d)
//
// Use an Extractor instead of Extract to handle each frame as it is decoded.
package features

import (
	"image"
	"time"

	"github.com/vitor-schipani/everyframe/gif"
)

// Frame holds the features of one frame.
type Frame struct {
	// Index counts frames from zero.
	Index int
	// Delay is how long the frame is shown.
	Delay time.Duration

	// What the frame stores.

	// StoredPixels is the area of the frame's rectangle.
	StoredPixels int64
	// OutsidePixels is how many of them lie outside the canvas, where
	// they are never shown.
	OutsidePixels int64
	// TransparentPixels is how many of them, inside the canvas or not,
	// have the transparent index.
	TransparentPixels int64
	// DrawnPixels is how many of them were painted on the canvas: those
	// inside it that are not transparent and have a colour in the palette.
	DrawnPixels int64
	// Colours is how many different palette indices the frame uses, not
	// counting the transparent one.
	Colours int

	// What the viewer sees.

	// ChangedPixels is how many pixels of the canvas differ from the
	// picture shown for the previous frame. The first frame is compared
	// with the empty canvas.
	ChangedPixels int64
	// Change is the size of that difference, from 0 for an identical
	// picture to 1 for the largest possible one: the mean difference per
	// colour channel, alpha included, over the whole canvas.
	Change float64
	// Hash is the perceptual hash of the picture shown.
	Hash Hash
	// HashDistance is the distance from the previous frame's hash, and 0
	// for the first frame.
	HashDistance int
	// HashDistanceFirst is the distance from the first frame's hash.
	HashDistanceFirst int
}

// Report holds the features of an animation and of each of its frames.
type Report struct {
	Summary Summary
	Frames  []Frame
}

// Extract measures every remaining frame of d. When decoding fails part-way
// it returns the error together with a report on the frames before it.
func Extract(d *gif.Decoder) (*Report, error) {
	e := NewExtractor(d.Header())
	rep := &Report{}
	var err error
	for f, ferr := range d.Frames() {
		if ferr != nil {
			err = ferr
			break
		}
		rep.Frames = append(rep.Frames, e.Add(f))
	}
	rep.Summary = e.Summary(d.Metadata())
	return rep, err
}

// Extractor measures an animation one frame at a time. It composites the
// frames itself, so it needs every frame, in order:
//
//	e := features.NewExtractor(d.Header())
//	for frame, err := range d.Frames() {
//		...
//		ff := e.Add(frame)
//	}
//	summary := e.Summary(d.Metadata())
//
// The time it spends on a frame depends on the size of the frame, not of the
// canvas, so a file cannot make it work harder than the decoder's limits
// allow.
type Extractor struct {
	canvas *gif.Canvas
	bounds image.Rectangle
	// prev is the picture shown for the previous frame, laid out like
	// the canvas.
	prev []byte
	// prevArea is the part of the canvas the previous frame covered, the
	// only part its disposal can have altered since.
	prevArea image.Rectangle

	// The brightness grid behind the hash, kept up to date as pixels
	// change: the cell of each column and row of the canvas, and the
	// total brightness and the number of pixels of each cell.
	col, row []uint8
	luma     [hashRows * hashCols]int64
	count    [hashRows * hashCols]int64

	first, last Hash
	hashes      map[Hash]struct{}
	sum         Summary

	onStage func(Stage)
}

// NewExtractor returns an Extractor for an animation with the given header.
// It allocates eight bytes per pixel of the canvas, and four more once a
// frame uses gif.DisposalPrevious.
func NewExtractor(h gif.Header) *Extractor {
	e := &Extractor{
		canvas: gif.NewCanvas(h),
		bounds: image.Rect(0, 0, h.Width, h.Height),
		prev:   make([]byte, 4*h.Width*h.Height),
		col:    make([]uint8, h.Width),
		row:    make([]uint8, h.Height),
		hashes: map[Hash]struct{}{},
		sum:    Summary{Width: h.Width, Height: h.Height},
	}
	for x := range e.col {
		e.col[x] = uint8(x * hashCols / h.Width)
	}
	for y := range e.row {
		e.row[y] = uint8(y * hashRows / h.Height)
	}
	for _, r := range e.row {
		for _, c := range e.col {
			e.count[int(r)*hashCols+int(c)]++
		}
	}
	return e
}

// Shown returns the picture a viewer displays for the last frame added. It
// belongs to the Extractor and changes on the next call to Add, so copy it
// to keep it, and do not modify it.
func (e *Extractor) Shown() *image.RGBA {
	// Drawing nothing would still apply the last frame's disposal, so
	// the canvas is not asked: prev already equals the picture shown.
	return &image.RGBA{Pix: e.prev, Stride: 4 * e.bounds.Dx(), Rect: e.bounds}
}

// Add measures the next frame of the animation.
func (e *Extractor) Add(f *gif.Frame) Frame {
	out := Frame{
		Index:        f.Index,
		Delay:        f.Delay,
		StoredPixels: int64(f.Rect.Dx()) * int64(f.Rect.Dy()),
	}
	area := f.Rect.Intersect(e.bounds)
	out.OutsidePixels = out.StoredPixels - int64(area.Dx())*int64(area.Dy())
	e.measureStored(f, area, &out)
	e.done(StageStored)

	// The canvas can only differ from the previous picture where the
	// previous frame was disposed of and where this one was drawn.
	shown := e.canvas.Draw(f)
	e.done(StageComposite)
	var diff int64
	for _, r := range [2]image.Rectangle{e.prevArea, area} {
		changed, d := e.sync(shown, r)
		out.ChangedPixels += changed
		diff += d
	}
	e.prevArea = area
	if n := len(e.prev); n > 0 {
		out.Change = float64(diff) / (255 * float64(n))
	}
	e.done(StageChange)

	out.Hash = e.hash()
	if e.sum.Frames == 0 {
		e.first = out.Hash
	} else {
		out.HashDistance = out.Hash.Distance(e.last)
	}
	out.HashDistanceFirst = out.Hash.Distance(e.first)
	e.last = out.Hash
	e.hashes[out.Hash] = struct{}{}
	e.done(StageHash)

	e.summarise(f, &out)
	e.done(StageSummary)
	return out
}
