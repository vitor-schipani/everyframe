package features

import (
	"time"

	"github.com/vitor-schipani/everyframe/gif"
)

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
