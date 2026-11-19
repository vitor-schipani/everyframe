package features

import (
	"image"
	"image/color"
	"testing"

	"github.com/vitor-schipani/everyframe/gif"
)

// The irregularities the decoder reports on frames are counted.
func TestSummaryIrregularities(t *testing.T) {
	pal := color.Palette{black, white}
	e := NewExtractor(header(4, 4))
	e.Add(frame(0, image.Rect(0, 0, 4, 4), pal, 0))
	f := frame(1, image.Rect(2, 2, 6, 6), pal, 1)
	f.LocalPalette, f.OutOfCanvas, f.SurplusData, f.IndexOutOfRange = true, true, true, true
	e.Add(f)
	f = frame(2, image.Rect(0, 0, 4, 4), pal, 1)
	f.MissingPixels, f.Truncated = 5, true
	e.Add(f)

	s := e.Summary(gif.Metadata{
		Comments:          []gif.Comment{{Size: 10}, {Size: 5}},
		UnknownExtensions: []gif.Extension{{Size: 7}},
		DroppedExtensions: 2,
		MissingTrailer:    true,
	})
	if s.LocalPaletteFrames != 1 || s.OutOfCanvasFrames != 1 || s.SurplusDataFrames != 1 ||
		s.IndexOutOfRangeFrames != 1 || s.MissingPixelsFrames != 1 || !s.Truncated {
		t.Errorf("frame counts: %+v", s)
	}
	if s.OutsidePixels != 12 || s.HasLoopCount {
		t.Errorf("outside %d, loop count set %v", s.OutsidePixels, s.HasLoopCount)
	}
	if s.Comments != 2 || s.CommentBytes != 15 || s.UnknownExtensions != 1 || s.UnknownExtensionBytes != 7 ||
		s.DroppedExtensions != 2 || !s.MissingTrailer {
		t.Errorf("container: %+v", s)
	}
}

func TestSummaryOfNothing(t *testing.T) {
	s := NewExtractor(header(3, 3)).Summary(gif.Metadata{})
	if s.Frames != 0 || s.MinDelay != 0 || s.DistinctHashes != 0 || s.MaxChangeFrame != 0 {
		t.Errorf("summary = %+v", s)
	}
}
