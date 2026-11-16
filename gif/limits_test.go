package gif

import (
	"bytes"
	"errors"
	"image"
	"runtime"
	"testing"
)

// wantLimit checks that err is a LimitError for the named limit.
func wantLimit(t *testing.T, err error, limit string) {
	t.Helper()
	var lerr *LimitError
	if !errors.As(err, &lerr) || !errors.Is(err, ErrLimit) {
		t.Fatalf("err = %v, want a *LimitError matching ErrLimit", err)
	}
	if lerr.Limit != limit || lerr.Value <= lerr.Max {
		t.Errorf("LimitError = %+v, want limit %q with Value over Max", lerr, limit)
	}
}

// allocated reports how many bytes fn allocates.
func allocated(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func TestCanvasLimit(t *testing.T) {
	data := newBuilder(65535, 65535, nil).trailer().bytes()
	var err error
	if n := allocated(func() { _, err = NewDecoder(bytes.NewReader(data), Options{}) }); n > 1<<20 {
		t.Errorf("allocated %d bytes before refusing the canvas", n)
	}
	wantLimit(t, err, "canvas pixels")

	// The same file is accepted once the limit is raised.
	if _, err := NewDecoder(bytes.NewReader(data), Options{Limits: Limits{MaxPixels: 1 << 32}}); err != nil {
		t.Errorf("with MaxPixels raised: %v", err)
	}
}

func TestFramePixelLimit(t *testing.T) {
	// A small canvas does not make an oversized frame any cheaper.
	b := newBuilder(10, 10, greys(2))
	b.image(image.Rect(0, 0, 60000, 60000), nil, false, []byte{1})
	data := b.trailer().bytes()
	var err error
	if n := allocated(func() { err = decodeErr(data) }); n > 1<<20 {
		t.Errorf("allocated %d bytes before refusing the frame", n)
	}
	wantLimit(t, err, "frame pixels")
}

func TestFrameCountLimit(t *testing.T) {
	b := newBuilder(1, 1, greys(2))
	for range 3 {
		b.image(onePixel, nil, false, []byte{1})
	}
	d, err := NewDecoder(bytes.NewReader(b.trailer().bytes()), Options{Limits: Limits{MaxFrames: 2}})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if _, err := d.Next(); err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
	}
	_, err = d.Next()
	wantLimit(t, err, "frames")
	if _, again := d.Next(); again != err {
		t.Errorf("Next after the limit = %v, want the same error", again)
	}
}

func TestDecodedBytesLimit(t *testing.T) {
	b := newBuilder(2, 2, greys(2))
	for range 3 {
		b.image(image.Rect(0, 0, 2, 2), nil, false, []byte{1, 1, 1, 1})
	}
	d, err := NewDecoder(bytes.NewReader(b.trailer().bytes()), Options{Limits: Limits{MaxDecodedBytes: 10}})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if _, err := d.Next(); err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
	}
	_, err = d.Next()
	wantLimit(t, err, "decoded bytes")
}

// Compressed data that expands far beyond the frame's rectangle is skipped,
// not decoded into memory.
func TestCompressionBombIsNotExpanded(t *testing.T) {
	b := newBuilder(1, 1, greys(2))
	b.image(onePixel, nil, false, make([]byte, 50<<20))
	data := b.trailer().bytes()
	if len(data) > 1<<20 {
		t.Fatalf("test file is %d bytes; it is meant to be small", len(data))
	}
	var frames []*Frame
	if n := allocated(func() { _, frames = decodeAll(t, data, Options{}) }); n > 1<<20 {
		t.Errorf("allocated %d bytes decoding a %d-byte file", n, len(data))
	}
	if len(frames) != 1 || !frames[0].SurplusData {
		t.Errorf("want one frame flagged with SurplusData, got %+v", frames)
	}
}

func TestMetadataLimit(t *testing.T) {
	b := newBuilder(1, 1, nil)
	b.buf.Write([]byte{blockExtension, extComment})
	b.blocks(bytes.Repeat([]byte("x"), 500))
	for range 10 {
		b.extension(extComment, []byte("dropped"))
		b.extension(0x42, []byte("dropped"))
	}
	b.extension(extApplication, []byte("NETSCAPE2.0"), []byte{1, 7, 0})
	d, _ := decodeAll(t, b.trailer().bytes(), Options{Limits: Limits{MaxMetadataBytes: 100}})
	m := d.Metadata()

	if len(m.Comments) != 1 {
		t.Fatalf("kept %d comments, want 1", len(m.Comments))
	}
	if c := m.Comments[0]; c.Size != 500 || len(c.Data) != 100-extensionCost {
		t.Errorf("comment kept %d of %d bytes, want %d of 500", len(c.Data), c.Size, 100-extensionCost)
	}
	if len(m.UnknownExtensions) != 0 || len(m.Applications) != 0 || m.DroppedExtensions != 21 {
		t.Errorf("kept %d unknown and %d application extensions, dropped %d; want 0, 0, 21",
			len(m.UnknownExtensions), len(m.Applications), m.DroppedExtensions)
	}
	// The loop count is read even from an extension that is not recorded.
	if !m.HasLoopCount || m.LoopCount != 7 {
		t.Errorf("loop count = %d (present %v), want 7", m.LoopCount, m.HasLoopCount)
	}
}

// A file made of nothing but empty extensions cannot grow Metadata past the
// budget.
func TestExtensionFloodIsBounded(t *testing.T) {
	b := newBuilder(1, 1, nil)
	for range 200_000 {
		b.extension(extComment)
	}
	d, _ := decodeAll(t, b.trailer().bytes(), Options{})
	m := d.Metadata()
	if max := int(DefaultLimits().MaxMetadataBytes / extensionCost); len(m.Comments) > max {
		t.Errorf("kept %d comments, want at most %d", len(m.Comments), max)
	}
	if len(m.Comments)+m.DroppedExtensions != 200_000 {
		t.Errorf("kept %d and dropped %d, which does not add up to 200000", len(m.Comments), m.DroppedExtensions)
	}
}
