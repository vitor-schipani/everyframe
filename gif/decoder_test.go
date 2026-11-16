package gif

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	stdgif "image/gif"
	"io"
	"reflect"
	"testing"
	"time"
)

func TestHeader(t *testing.T) {
	b := newBuilder(300, 200, greys(4))
	d, frames := decodeAll(t, b.trailer().bytes(), Options{})
	want := Header{Version: "89a", Width: 300, Height: 200, GlobalPalette: greys(4)}
	if got := d.Header(); !reflect.DeepEqual(got, want) {
		t.Errorf("Header() = %+v, want %+v", got, want)
	}
	if len(frames) != 0 {
		t.Errorf("got %d frames, want 0", len(frames))
	}
}

func TestFrames(t *testing.T) {
	local := color.Palette{color.RGBA{R: 0xFF, A: 0xFF}, color.RGBA{B: 0xFF, A: 0xFF}}
	b := newBuilder(4, 3, greys(4))
	b.image(image.Rect(0, 0, 4, 3), nil, false, []byte{0, 1, 2, 3, 3, 2, 1, 0, 0, 0, 3, 3})
	b.control(DisposalBackground, 25, 1)
	b.image(image.Rect(1, 1, 3, 3), local, false, []byte{0, 1, 1, 0})
	d, frames := decodeAll(t, b.trailer().bytes(), Options{})
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want 2", len(frames))
	}

	want0 := &Frame{
		Index:       0,
		Offset:      25, // 13-byte header + 4-colour palette
		Rect:        image.Rect(0, 0, 4, 3),
		Pixels:      []byte{0, 1, 2, 3, 3, 2, 1, 0, 0, 0, 3, 3},
		Palette:     d.Header().GlobalPalette,
		Transparent: -1,
	}
	if !reflect.DeepEqual(frames[0], want0) {
		t.Errorf("frame 0 = %+v\nwant      %+v", frames[0], want0)
	}

	f := frames[1]
	f.Offset = 0 // depends on the compressed size of frame 0
	want1 := &Frame{
		Index:        1,
		Rect:         image.Rect(1, 1, 3, 3),
		Pixels:       []byte{0, 1, 1, 0},
		Palette:      local,
		LocalPalette: true,
		Transparent:  1,
		Disposal:     DisposalBackground,
		Delay:        250 * time.Millisecond,
	}
	if !reflect.DeepEqual(f, want1) {
		t.Errorf("frame 1 = %+v\nwant      %+v", f, want1)
	}
}

// A graphic control extension applies to one image only.
func TestGraphicControlDoesNotCarryOver(t *testing.T) {
	b := newBuilder(1, 1, greys(2))
	b.control(DisposalPrevious, 7, 0)
	b.image(image.Rect(0, 0, 1, 1), nil, false, []byte{1})
	b.image(image.Rect(0, 0, 1, 1), nil, false, []byte{1})
	_, frames := decodeAll(t, b.trailer().bytes(), Options{})
	if f := frames[1]; f.Disposal != DisposalUnspecified || f.Delay != 0 || f.Transparent != -1 {
		t.Errorf("frame 1 inherited the graphic control: %+v", f)
	}
}

func TestInterlaced(t *testing.T) {
	// Ten rows of one pixel, each holding its own row number. The stream
	// carries them in pass order.
	stream := []byte{0, 8, 4, 2, 6, 1, 3, 5, 7, 9}
	b := newBuilder(1, 10, greys(16))
	b.image(image.Rect(0, 0, 1, 10), nil, true, stream)
	_, frames := decodeAll(t, b.trailer().bytes(), Options{})
	want := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	if f := frames[0]; !f.Interlaced || !bytes.Equal(f.Pixels, want) {
		t.Errorf("Interlaced = %v, Pixels = %v, want true, %v", f.Interlaced, f.Pixels, want)
	}
}

func TestPixelsAreReused(t *testing.T) {
	b := newBuilder(2, 1, greys(2))
	b.image(image.Rect(0, 0, 2, 1), nil, false, []byte{0, 0})
	b.image(image.Rect(0, 0, 2, 1), nil, false, []byte{1, 1})
	d, err := NewDecoder(bytes.NewReader(b.trailer().bytes()), Options{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := d.Next()
	if err != nil {
		t.Fatal(err)
	}
	kept := first.Image()
	if _, err := d.Next(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(kept.Pix, []byte{0, 0}) {
		t.Errorf("Image() pixels changed to %v after Next", kept.Pix)
	}
	if _, err := d.Next(); err != io.EOF {
		t.Errorf("third Next = %v, want io.EOF", err)
	}
	if _, err := d.Next(); err != io.EOF {
		t.Errorf("Next after io.EOF = %v, want io.EOF again", err)
	}
}

func TestImage(t *testing.T) {
	b := newBuilder(2, 1, greys(2))
	b.control(DisposalKeep, 0, 0)
	b.image(image.Rect(0, 0, 2, 1), nil, false, []byte{0, 5})
	_, frames := decodeAll(t, b.trailer().bytes(), Options{})
	img := frames[0].Image()
	if len(img.Palette) != 6 {
		t.Fatalf("palette has %d entries, want it padded to 6", len(img.Palette))
	}
	for x := range 2 {
		if _, _, _, a := img.At(x, 0).RGBA(); a != 0 {
			t.Errorf("pixel %d has alpha %d, want transparent", x, a)
		}
	}
}

// Irregular files that browsers still render decode without error, and the
// irregularity is reported on the frame.
func TestToleratedFrames(t *testing.T) {
	full := image.Rect(0, 0, 2, 2)
	tests := []struct {
		name  string
		build func(b *builder)
		check func(t *testing.T, f *Frame)
	}{
		{
			name:  "outside the canvas",
			build: func(b *builder) { b.image(image.Rect(1, 1, 4, 3), nil, false, make([]byte, 6)) },
			check: func(t *testing.T, f *Frame) {
				if !f.OutOfCanvas || f.Rect != image.Rect(1, 1, 4, 3) {
					t.Errorf("OutOfCanvas = %v, Rect = %v", f.OutOfCanvas, f.Rect)
				}
			},
		},
		{
			name:  "too few pixels",
			build: func(b *builder) { b.image(full, nil, false, []byte{3, 3, 3}) },
			check: func(t *testing.T, f *Frame) {
				if f.MissingPixels != 1 || !bytes.Equal(f.Pixels, []byte{3, 3, 3, 0}) {
					t.Errorf("MissingPixels = %d, Pixels = %v", f.MissingPixels, f.Pixels)
				}
			},
		},
		{
			name:  "no pixel data",
			build: func(b *builder) { b.imageData(full, nil, false, 8, nil) },
			check: func(t *testing.T, f *Frame) {
				if f.MissingPixels != 4 {
					t.Errorf("MissingPixels = %d, want 4", f.MissingPixels)
				}
			},
		},
		{
			name:  "too many pixels",
			build: func(b *builder) { b.image(full, nil, false, []byte{1, 1, 1, 1, 2}) },
			check: func(t *testing.T, f *Frame) {
				if !f.SurplusData || !bytes.Equal(f.Pixels, []byte{1, 1, 1, 1}) {
					t.Errorf("SurplusData = %v, Pixels = %v", f.SurplusData, f.Pixels)
				}
			},
		},
		{
			name: "bytes after the end code",
			build: func(b *builder) {
				b.imageData(full, nil, false, 8, append(compress([]byte{1, 1, 1, 1}), 0xAA, 0xBB))
			},
			check: func(t *testing.T, f *Frame) {
				if !f.SurplusData {
					t.Error("SurplusData = false")
				}
			},
		},
		{
			name:  "index past the palette",
			build: func(b *builder) { b.image(full, nil, false, []byte{0, 1, 2, 4}) },
			check: func(t *testing.T, f *Frame) {
				if !f.IndexOutOfRange {
					t.Error("IndexOutOfRange = false")
				}
			},
		},
		{
			name: "transparent index past the palette",
			build: func(b *builder) {
				b.control(DisposalKeep, 0, 9)
				b.image(full, nil, false, []byte{0, 9, 9, 3})
			},
			check: func(t *testing.T, f *Frame) {
				if f.IndexOutOfRange {
					t.Error("IndexOutOfRange = true for a transparent pixel")
				}
			},
		},
		{
			name:  "empty rectangle",
			build: func(b *builder) { b.image(image.Rect(1, 1, 1, 1), nil, false, nil) },
			check: func(t *testing.T, f *Frame) {
				if len(f.Pixels) != 0 || f.OutOfCanvas {
					t.Errorf("Pixels = %v, OutOfCanvas = %v", f.Pixels, f.OutOfCanvas)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBuilder(2, 2, greys(4))
			tt.build(b)
			// A regular frame follows, to prove the stream stays in step.
			b.image(full, nil, false, []byte{0, 1, 2, 3})
			_, frames := decodeAll(t, b.trailer().bytes(), Options{})
			if len(frames) != 2 {
				t.Fatalf("got %d frames, want 2", len(frames))
			}
			tt.check(t, frames[0])
			if !bytes.Equal(frames[1].Pixels, []byte{0, 1, 2, 3}) {
				t.Errorf("following frame decoded as %v", frames[1].Pixels)
			}
		})
	}
}

func TestNoPalette(t *testing.T) {
	b := newBuilder(1, 1, nil)
	b.image(image.Rect(0, 0, 1, 1), nil, false, []byte{0})
	_, frames := decodeAll(t, b.trailer().bytes(), Options{})
	if f := frames[0]; f.Palette != nil || !f.IndexOutOfRange {
		t.Errorf("Palette = %v, IndexOutOfRange = %v, want nil, true", f.Palette, f.IndexOutOfRange)
	}
}

func TestRejected(t *testing.T) {
	px := image.Rect(0, 0, 1, 1)
	badCodeSize := newBuilder(1, 1, greys(2))
	badCodeSize.imageData(px, nil, false, 9, compress([]byte{0}))
	// With 2-bit literals the codes 4 and 5 are "clear" and "end", so a
	// first code of 7 refers to a table entry that does not exist.
	badData := newBuilder(1, 1, greys(2))
	badData.imageData(px, nil, false, 2, []byte{0xFF, 0xFF})

	tests := []struct {
		name string
		data []byte
	}{
		{"not a gif", []byte("JFIF, definitely not a GIF")},
		{"unknown version", []byte("GIF90a\x01\x00\x01\x00\x00\x00\x00;")},
		{"invalid code size", badCodeSize.trailer().bytes()},
		{"invalid lzw data", badData.trailer().bytes()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ferr *FormatError
			if err := decodeErr(tt.data); !errors.As(err, &ferr) {
				t.Errorf("err = %v, want a *FormatError", err)
			}
		})
	}
}

// decodeErr decodes data to the end and returns the first error, or nil.
func decodeErr(data []byte) error {
	d, err := NewDecoder(bytes.NewReader(data), Options{})
	if err != nil {
		return err
	}
	for _, err := range d.Frames() {
		if err != nil {
			return err
		}
	}
	return nil
}

// Cutting a file short anywhere is either a clean end between blocks or an
// io.ErrUnexpectedEOF; it is never a different error or a panic.
func TestTruncated(t *testing.T) {
	b := newBuilder(3, 3, greys(4))
	b.extension(0xFE, []byte("a comment"))
	b.control(DisposalKeep, 10, 2)
	b.image(image.Rect(0, 0, 3, 3), greys(2), true, []byte{0, 1, 0, 1, 0, 1, 0, 1, 0})
	b.image(image.Rect(1, 1, 2, 2), nil, false, []byte{3})
	data := b.trailer().bytes()
	clean := 0
	for n := range len(data) {
		switch err := decodeErr(data[:n]); {
		case err == nil:
			clean++
		case !errors.Is(err, io.ErrUnexpectedEOF):
			t.Errorf("cut at %d of %d: err = %v, want io.ErrUnexpectedEOF", n, len(data), err)
		}
	}
	// The block boundaries: after the header, the comment, the graphic
	// control and each image.
	if clean != 5 {
		t.Errorf("%d cut points decoded cleanly, want 5", clean)
	}
}

// A frame the input ends inside of is returned as far as it goes, and the
// truncation is reported after it.
func TestTruncatedFrameIsDelivered(t *testing.T) {
	pixels := make([]byte, 64*64)
	for i := range pixels {
		pixels[i] = byte(i*i>>3) | 1 // never 0, and hard to compress
	}
	b := newBuilder(64, 64, greys(256))
	b.image(image.Rect(0, 0, 64, 64), nil, false, []byte{1})
	b.image(image.Rect(0, 0, 64, 64), nil, false, pixels)
	b.image(image.Rect(0, 0, 64, 64), nil, false, pixels)
	data := b.trailer().bytes()

	d, err := NewDecoder(bytes.NewReader(data[:len(data)/3]), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if f, err := d.Next(); err != nil || f.Truncated {
		t.Fatalf("frame 0: Truncated = %v, err = %v", f != nil && f.Truncated, err)
	}
	f, err := d.Next()
	if err != nil {
		t.Fatalf("frame 1: %v, want the partial frame", err)
	}
	got := len(f.Pixels) - f.MissingPixels
	if !f.Truncated || got == 0 || f.MissingPixels == 0 {
		t.Fatalf("frame 1: Truncated = %v with %d pixels missing of %d", f.Truncated, f.MissingPixels, len(f.Pixels))
	}
	if !bytes.Equal(f.Pixels[:got], pixels[:got]) || !bytes.Equal(f.Pixels[got:], make([]byte, f.MissingPixels)) {
		t.Error("frame 1: decoded pixels are wrong, or the missing ones are not zero")
	}
	for range 2 {
		if _, err := d.Next(); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("Next after the cut frame = %v, want io.ErrUnexpectedEOF", err)
		}
	}
}

// Files written by the standard encoder decode to the same frames.
func TestMatchesStandardEncoder(t *testing.T) {
	pal := greys(8)
	src := &stdgif.GIF{LoopCount: 3}
	for i := range 4 {
		img := image.NewPaletted(image.Rect(i, i, 20, 16), pal)
		for j := range img.Pix {
			img.Pix[j] = uint8((j*7 + i) % len(pal))
		}
		src.Image = append(src.Image, img)
		src.Delay = append(src.Delay, 5*(i+1))
		src.Disposal = append(src.Disposal, byte(1+i%3))
	}
	var buf bytes.Buffer
	if err := stdgif.EncodeAll(&buf, src); err != nil {
		t.Fatal(err)
	}
	d, frames := decodeAll(t, buf.Bytes(), Options{})
	if h := d.Header(); h.Width != 20 || h.Height != 16 {
		t.Errorf("canvas is %dx%d, want 20x16", h.Width, h.Height)
	}
	if len(frames) != len(src.Image) {
		t.Fatalf("got %d frames, want %d", len(frames), len(src.Image))
	}
	for i, f := range frames {
		want := src.Image[i]
		if f.Rect != want.Rect || !bytes.Equal(f.Pixels, want.Pix) {
			t.Errorf("frame %d: rect %v, pixels differ = %v; want rect %v",
				i, f.Rect, !bytes.Equal(f.Pixels, want.Pix), want.Rect)
		}
		if f.Delay != time.Duration(src.Delay[i])*10*time.Millisecond || byte(f.Disposal) != src.Disposal[i] {
			t.Errorf("frame %d: delay %v, disposal %v", i, f.Delay, f.Disposal)
		}
		if f.MissingPixels != 0 || f.Truncated || f.SurplusData || f.OutOfCanvas || f.IndexOutOfRange {
			t.Errorf("frame %d of a regular file is flagged: %+v", i, f)
		}
	}
}
