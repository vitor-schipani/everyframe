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
	"bytes"
	"fmt"
	"image"
	"math/bits"
	"time"

	"github.com/vitor-schipani/everyframe/gif"
)

// Hash is a 64-bit perceptual hash of a picture. Pictures that look alike
// have hashes that differ in few bits, whatever their size.
//
// It is a difference hash: the picture is reduced to 8 rows of 9 brightness
// averages, and each bit says whether one average is brighter than the one
// to its right. Transparent pixels count as black.
type Hash uint64

// String returns the hash as 16 hexadecimal digits.
func (h Hash) String() string { return fmt.Sprintf("%016x", uint64(h)) }

// Distance returns how many of the 64 bits differ between h and other: 0 for
// pictures that hash alike, around 32 for unrelated ones.
func (h Hash) Distance(other Hash) int { return bits.OnesCount64(uint64(h ^ other)) }

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

// Summary holds the features of the animation as a whole.
type Summary struct {
	// Width and Height are the size of the canvas.
	Width, Height int
	// Frames is the number of frames measured.
	Frames int

	// Duration is the sum of the frame delays: one pass of the animation.
	Duration time.Duration
	// MinDelay and MaxDelay are the shortest and longest frame delays.
	MinDelay, MaxDelay time.Duration
	// ZeroDelayFrames counts frames with no delay at all.
	ZeroDelayFrames int
	// LoopCount is how many times the animation repeats, with 0 meaning
	// forever. Without a loop extension, HasLoopCount is false and the
	// animation plays once, ending on its last frame.
	LoopCount    int
	HasLoopCount bool

	// DistinctHashes is how many different hashes the pictures shown
	// have: roughly, how many different pictures the animation has.
	DistinctHashes int
	// MaxChange is the largest Change of any frame after the first, and
	// MaxChangeFrame the frame it belongs to. Both are 0 when there are
	// fewer than two frames.
	MaxChange      float64
	MaxChangeFrame int
	// MaxHashDistanceFirst is the largest HashDistanceFirst of any frame,
	// and MaxHashDistanceFirstFrame the frame it belongs to: how far the
	// animation gets from the first frame, the one a thumbnail shows.
	MaxHashDistanceFirst      int
	MaxHashDistanceFirstFrame int

	// StoredPixels, OutsidePixels, TransparentPixels and DrawnPixels are
	// the totals of the per-frame counts of the same names.
	StoredPixels      int64
	OutsidePixels     int64
	TransparentPixels int64
	DrawnPixels       int64

	// The next counts are of frames with a property the gif package
	// reports on each frame.
	LocalPaletteFrames    int
	OutOfCanvasFrames     int
	MissingPixelsFrames   int
	SurplusDataFrames     int
	IndexOutOfRangeFrames int
	// Truncated reports that the input ended inside the last frame.
	Truncated bool

	// The rest describes the container: the number of extensions of each
	// kind with the size of their payloads, and what surrounds the GIF.
	Comments              int
	CommentBytes          int64
	Applications          int
	ApplicationBytes      int64
	UnknownExtensions     int
	UnknownExtensionBytes int64
	// DroppedExtensions counts extensions the decoder did not keep, which
	// are missing from the counts and sizes above.
	DroppedExtensions int
	MissingTrailer    bool
	// TrailingBytes is how much data follows the end of the GIF.
	TrailingBytes int64
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

// The picture is hashed from a grid of hashCols x hashRows brightness
// averages.
const (
	hashCols = 9
	hashRows = 8
)

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

	// The canvas can only differ from the previous picture where the
	// previous frame was disposed of and where this one was drawn.
	shown := e.canvas.Draw(f)
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

	out.Hash = e.hash()
	if e.sum.Frames == 0 {
		e.first = out.Hash
	} else {
		out.HashDistance = out.Hash.Distance(e.last)
	}
	out.HashDistanceFirst = out.Hash.Distance(e.first)
	e.last = out.Hash
	e.hashes[out.Hash] = struct{}{}

	e.summarise(f, &out)
	return out
}

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

// sync brings prev in line with the canvas inside area. It returns how many
// pixels had changed and the sum of their differences over the four
// channels, and moves each changed pixel's brightness to its new value in
// the grid.
func (e *Extractor) sync(shown *image.RGBA, area image.Rectangle) (changed, diff int64) {
	n := 4 * area.Dx()
	for y := area.Min.Y; y < area.Max.Y; y++ {
		i := shown.PixOffset(area.Min.X, y)
		cur, old := shown.Pix[i:i+n], e.prev[i:i+n]
		if bytes.Equal(cur, old) {
			continue
		}
		cells := e.luma[int(e.row[y])*hashCols:]
		for x := 0; x < n; x += 4 {
			c, o := cur[x:x+4:x+4], old[x:x+4:x+4]
			if c[0] == o[0] && c[1] == o[1] && c[2] == o[2] && c[3] == o[3] {
				continue
			}
			changed++
			diff += absDiff(c[0], o[0]) + absDiff(c[1], o[1]) + absDiff(c[2], o[2]) + absDiff(c[3], o[3])
			cells[e.col[area.Min.X+x/4]] += brightness(c) - brightness(o)
			copy(o, c)
		}
	}
	return changed, diff
}

func absDiff(a, b byte) int64 {
	if a > b {
		return int64(a - b)
	}
	return int64(b - a)
}

// brightness is the luma of an RGBA pixel, in thousandths of a level. A
// transparent pixel is all zeros, so it counts as black.
func brightness(p []byte) int64 {
	return 299*int64(p[0]) + 587*int64(p[1]) + 114*int64(p[2])
}

// hash computes the hash of the current picture from the brightness grid.
func (e *Extractor) hash() Hash {
	// A canvas narrower than the grid leaves columns of cells without
	// pixels, and a shorter one rows. Such a cell takes the value of the
	// nearest one before it that has pixels, as if the picture had been
	// stretched to fit.
	var mean [hashRows * hashCols]float64
	for i, n := range e.count {
		switch {
		case n > 0:
			mean[i] = float64(e.luma[i]) / float64(n)
		case e.count[i-i%hashCols] > 0: // the row has pixels, this column none
			mean[i] = mean[i-1]
		case i >= hashCols:
			mean[i] = mean[i-hashCols]
		}
	}
	var h Hash
	for r := range hashRows {
		for c := range hashCols - 1 {
			h <<= 1
			if i := r*hashCols + c; mean[i] > mean[i+1] {
				h |= 1
			}
		}
	}
	return h
}

// summarise adds a measured frame to the running summary.
func (e *Extractor) summarise(f *gif.Frame, ff *Frame) {
	s := &e.sum
	if s.Frames == 0 || ff.Delay < s.MinDelay {
		s.MinDelay = ff.Delay
	}
	s.MaxDelay = max(s.MaxDelay, ff.Delay)
	s.Duration += ff.Delay
	if ff.Delay == 0 {
		s.ZeroDelayFrames++
	}
	if s.Frames > 0 && ff.Change > s.MaxChange {
		s.MaxChange, s.MaxChangeFrame = ff.Change, ff.Index
	}
	if ff.HashDistanceFirst > s.MaxHashDistanceFirst {
		s.MaxHashDistanceFirst, s.MaxHashDistanceFirstFrame = ff.HashDistanceFirst, ff.Index
	}
	s.StoredPixels += ff.StoredPixels
	s.OutsidePixels += ff.OutsidePixels
	s.TransparentPixels += ff.TransparentPixels
	s.DrawnPixels += ff.DrawnPixels

	count := func(n *int, set bool) {
		if set {
			*n++
		}
	}
	count(&s.LocalPaletteFrames, f.LocalPalette)
	count(&s.OutOfCanvasFrames, f.OutOfCanvas)
	count(&s.MissingPixelsFrames, f.MissingPixels > 0)
	count(&s.SurplusDataFrames, f.SurplusData)
	count(&s.IndexOutOfRangeFrames, f.IndexOutOfRange)
	s.Truncated = s.Truncated || f.Truncated
	s.Frames++
}

// Summary returns the features of the animation as a whole, covering the
// frames added so far. m is the decoder's metadata, which is complete once
// the last frame has been read.
func (e *Extractor) Summary(m gif.Metadata) Summary {
	s := e.sum
	s.DistinctHashes = len(e.hashes)
	s.LoopCount, s.HasLoopCount = m.LoopCount, m.HasLoopCount
	s.Comments = len(m.Comments)
	for _, c := range m.Comments {
		s.CommentBytes += c.Size
	}
	s.Applications = len(m.Applications)
	for _, a := range m.Applications {
		s.ApplicationBytes += a.Size
	}
	s.UnknownExtensions = len(m.UnknownExtensions)
	for _, x := range m.UnknownExtensions {
		s.UnknownExtensionBytes += x.Size
	}
	s.DroppedExtensions = m.DroppedExtensions
	s.MissingTrailer = m.MissingTrailer
	s.TrailingBytes = m.TrailingBytes
	return s
}
