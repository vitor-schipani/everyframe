package gif

import (
	"bytes"
	"errors"
	"image"
	"io"
	"testing"
)

// fuzzLimits are small enough that the fuzzer cannot spend its time, or the
// machine's memory, on a single input.
var fuzzLimits = Limits{
	MaxPixels:        1 << 16,
	MaxFrames:        64,
	MaxDecodedBytes:  1 << 20,
	MaxMetadataBytes: 1 << 12,
}

// FuzzDecode checks that no input makes the decoder or the canvas panic,
// exceed the limits, or fail with an error outside the documented three.
func FuzzDecode(f *testing.F) {
	b := newBuilder(8, 8, greys(4))
	b.extension(extApplication, []byte("NETSCAPE2.0"), []byte{1, 0, 0})
	b.extension(extComment, []byte("seed"))
	b.control(DisposalPrevious, 5, 3)
	b.image(image.Rect(0, 0, 8, 8), nil, true, bytes.Repeat([]byte{0, 1, 2, 3}, 16))
	b.control(DisposalBackground, 5, -1)
	b.image(image.Rect(2, 2, 12, 6), greys(2), false, bytes.Repeat([]byte{1, 0}, 20))
	b.extension(0x01, []byte("plain text"))
	f.Add(append(b.trailer().bytes(), "trailing"...))
	f.Add(newBuilder(1, 1, nil).bytes())
	f.Add([]byte("GIF89a"))

	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := NewDecoder(bytes.NewReader(data), Options{Limits: fuzzLimits})
		if err != nil {
			checkFuzzError(t, err)
			return
		}
		h := d.Header()
		if int64(h.Width)*int64(h.Height) > fuzzLimits.MaxPixels {
			t.Fatalf("canvas of %dx%d accepted", h.Width, h.Height)
		}
		canvas := NewCanvas(h)
		frames, decoded := 0, 0
		for {
			fr, err := d.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				checkFuzzError(t, err)
				break
			}
			if len(fr.Pixels) != fr.Rect.Dx()*fr.Rect.Dy() {
				t.Fatalf("frame %d: %d pixels for %v", fr.Index, len(fr.Pixels), fr.Rect)
			}
			if fr.MissingPixels < 0 || fr.MissingPixels > len(fr.Pixels) {
				t.Fatalf("frame %d: %d of %d pixels missing", fr.Index, fr.MissingPixels, len(fr.Pixels))
			}
			if fr.Index != frames {
				t.Fatalf("frame %d has Index %d", frames, fr.Index)
			}
			frames++
			decoded += len(fr.Pixels)
			canvas.Draw(fr)
			img := fr.Image()
			if !img.Rect.Empty() {
				img.At(img.Rect.Min.X, img.Rect.Min.Y) // must not panic on any index
			}
		}
		if frames > fuzzLimits.MaxFrames || int64(decoded) > fuzzLimits.MaxDecodedBytes {
			t.Fatalf("decoded %d frames and %d pixels, past the limits", frames, decoded)
		}
		m := d.Metadata()
		kept := 0
		for _, c := range m.Comments {
			kept += len(c.Data)
		}
		for _, a := range m.Applications {
			kept += len(a.Data)
		}
		for _, e := range m.UnknownExtensions {
			kept += len(e.Data)
		}
		if int64(kept) > fuzzLimits.MaxMetadataBytes || len(m.TrailingPrefix) > trailingPrefixLen {
			t.Fatalf("kept %d bytes of metadata and %d of trailing data", kept, len(m.TrailingPrefix))
		}
		if m.TrailingBytes > int64(len(data)) {
			t.Fatalf("%d trailing bytes in %d bytes of input", m.TrailingBytes, len(data))
		}
	})
}

// checkFuzzError fails unless err is one of the kinds decoding is documented
// to return.
func checkFuzzError(t *testing.T, err error) {
	t.Helper()
	var ferr *FormatError
	if !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, ErrLimit) && !errors.As(err, &ferr) {
		t.Fatalf("unexpected kind of error: %v", err)
	}
}
