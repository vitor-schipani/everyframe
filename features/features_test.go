package features

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	stdgif "image/gif"
	"io"
	"testing"
	"time"

	"github.com/vitor-schipani/everyframe/gif"
)

var (
	black = color.RGBA{A: 0xFF}
	white = color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	red   = color.RGBA{R: 0xFF, A: 0xFF}
)

// frame builds a frame filled with one palette index.
func frame(index int, r image.Rectangle, pal color.Palette, fill byte) *gif.Frame {
	return &gif.Frame{
		Index:       index,
		Rect:        r,
		Pixels:      bytes.Repeat([]byte{fill}, r.Dx()*r.Dy()),
		Palette:     pal,
		Transparent: -1,
		Disposal:    gif.DisposalKeep,
	}
}

func header(w, h int) gif.Header { return gif.Header{Version: "89a", Width: w, Height: h} }

// A canvas with no area has nothing to measure, and must not divide by zero.
func TestEmptyCanvas(t *testing.T) {
	e := NewExtractor(header(0, 0))
	got := e.Add(frame(0, image.Rect(0, 0, 2, 2), color.Palette{black, white}, 1))
	if got.StoredPixels != 4 || got.OutsidePixels != 4 || got.DrawnPixels != 0 || got.Change != 0 || got.Hash != 0 {
		t.Errorf("features = %+v", got)
	}
}

// animation encodes three 8x6 frames: white, white with a red 3x2 patch,
// and black.
func animation(t *testing.T) []byte {
	t.Helper()
	pal := color.Palette{black, white, red, color.RGBA{B: 0xFF, A: 0xFF}}
	g := &stdgif.GIF{LoopCount: 3}
	for i, fr := range []struct {
		r    image.Rectangle
		fill uint8
	}{{image.Rect(0, 0, 8, 6), 1}, {image.Rect(2, 1, 5, 3), 2}, {image.Rect(0, 0, 8, 6), 0}} {
		img := image.NewPaletted(fr.r, pal)
		for j := range img.Pix {
			img.Pix[j] = fr.fill
		}
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, 10*i)
		g.Disposal = append(g.Disposal, stdgif.DisposalNone)
	}
	var buf bytes.Buffer
	if err := stdgif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtract(t *testing.T) {
	data := append(animation(t), "trailing"...)
	d, err := gif.NewDecoder(bytes.NewReader(data), gif.Options{})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Extract(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Frames) != 3 {
		t.Fatalf("%d frames, want 3", len(rep.Frames))
	}
	if f := rep.Frames[1]; f.Index != 1 || f.Delay != 100*time.Millisecond || f.StoredPixels != 6 || f.ChangedPixels != 6 {
		t.Errorf("frame 1 = %+v", f)
	}

	s := rep.Summary
	if s.Width != 8 || s.Height != 6 || s.Frames != 3 {
		t.Errorf("summary is of a %dx%d animation of %d frames", s.Width, s.Height, s.Frames)
	}
	if s.Duration != 300*time.Millisecond || s.MinDelay != 0 || s.MaxDelay != 200*time.Millisecond || s.ZeroDelayFrames != 1 {
		t.Errorf("duration %v, delays %v to %v, %d zero", s.Duration, s.MinDelay, s.MaxDelay, s.ZeroDelayFrames)
	}
	if !s.HasLoopCount || s.LoopCount != 3 {
		t.Errorf("loop count %d (set: %v), want 3", s.LoopCount, s.HasLoopCount)
	}
	// White to black is the largest change, and changes all three colour
	// channels of every pixel outside the red patch.
	if s.MaxChangeFrame != 2 || s.MaxChange != rep.Frames[2].Change || s.MaxChange < 0.6 {
		t.Errorf("largest change %v at frame %d", s.MaxChange, s.MaxChangeFrame)
	}
	if s.StoredPixels != 48+6+48 || s.DrawnPixels != s.StoredPixels || s.OutsidePixels != 0 {
		t.Errorf("stored %d, drawn %d, outside %d", s.StoredPixels, s.DrawnPixels, s.OutsidePixels)
	}
	if s.Applications != 1 || s.ApplicationBytes == 0 || s.TrailingBytes != 8 || s.MissingTrailer || s.Truncated {
		t.Errorf("container: %d applications of %d bytes, %d trailing bytes, missing trailer %v, truncated %v",
			s.Applications, s.ApplicationBytes, s.TrailingBytes, s.MissingTrailer, s.Truncated)
	}
	// Flat white and flat black hash alike; the red patch does not.
	if rep.Frames[0].Hash != 0 || s.DistinctHashes != 2 || s.MaxHashDistanceFirstFrame != 1 || s.MaxHashDistanceFirst == 0 {
		t.Errorf("%d distinct hashes, furthest from the first is frame %d at %d",
			s.DistinctHashes, s.MaxHashDistanceFirstFrame, s.MaxHashDistanceFirst)
	}
}

// A file that stops part-way is measured as far as it goes.
func TestExtractTruncated(t *testing.T) {
	data := animation(t)
	d, err := gif.NewDecoder(bytes.NewReader(data[:len(data)-12]), gif.Options{})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Extract(d)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("err = %v, want one wrapping io.ErrUnexpectedEOF", err)
	}
	if n := len(rep.Frames); n < 2 || rep.Summary.Frames != n || !rep.Summary.HasLoopCount {
		t.Errorf("%d frames, summary %+v", n, rep.Summary)
	}
}
