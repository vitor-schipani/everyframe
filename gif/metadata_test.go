package gif

import (
	"bytes"
	"image"
	"reflect"
	"strings"
	"testing"
)

var onePixel = image.Rect(0, 0, 1, 1)

func TestMetadataExtensions(t *testing.T) {
	long := strings.Repeat("0123456789", 60) // spans three sub-blocks
	b := newBuilder(1, 1, greys(2))
	b.extension(extComment, []byte("first"))
	b.extension(extApplication, []byte("NETSCAPE2.0"), []byte{1, 0x34, 0x12})
	b.image(onePixel, nil, false, []byte{0})
	b.buf.Write([]byte{blockExtension, extComment})
	b.blocks([]byte(long))
	b.extension(0x01, []byte("plain"), []byte("text"))
	b.extension(extApplication, []byte("XMP DataXMP"), []byte("<x/>"))
	b.image(onePixel, nil, false, []byte{1})
	b.extension(extComment)
	d, frames := decodeAll(t, b.trailer().bytes(), Options{})
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want 2", len(frames))
	}
	m := d.Metadata()

	if !m.HasLoopCount || m.LoopCount != 0x1234 {
		t.Errorf("loop count = %d (present %v), want %d", m.LoopCount, m.HasLoopCount, 0x1234)
	}
	if m.MissingTrailer || m.TrailingBytes != 0 || m.TrailingPrefix != nil {
		t.Errorf("clean file reported trailer problems: %+v", m)
	}

	type comment struct {
		before int
		data   string
	}
	var comments []comment
	for _, c := range m.Comments {
		comments = append(comments, comment{c.BeforeFrame, string(c.Data)})
		if c.Size != int64(len(c.Data)) {
			t.Errorf("comment %q has Size %d", c.Data, c.Size)
		}
	}
	if want := []comment{{0, "first"}, {1, long}, {2, ""}}; !reflect.DeepEqual(comments, want) {
		t.Errorf("comments = %v, want %v", comments, want)
	}
	if got := m.Comments[0].Offset; got != 19 { // 13-byte header + 2-colour palette
		t.Errorf("first comment at offset %d, want 19", got)
	}

	if len(m.Applications) != 2 {
		t.Fatalf("got %d application extensions, want 2", len(m.Applications))
	}
	if a := m.Applications[1]; a.Identifier != "XMP DataXMP" || string(a.Data) != "<x/>" || a.BeforeFrame != 1 {
		t.Errorf("second application extension = %+v", a)
	}

	if len(m.UnknownExtensions) != 1 {
		t.Fatalf("got %d unknown extensions, want 1", len(m.UnknownExtensions))
	}
	if e := m.UnknownExtensions[0]; e.Label != 0x01 || string(e.Data) != "plaintext" || e.BeforeFrame != 1 {
		t.Errorf("unknown extension = %+v", e)
	}
}

func TestLoopCount(t *testing.T) {
	tests := []struct {
		name   string
		blocks [][]byte
		want   int
		has    bool
	}{
		{"netscape", [][]byte{[]byte("NETSCAPE2.0"), {1, 5, 0}}, 5, true},
		{"animexts", [][]byte{[]byte("ANIMEXTS1.0"), {1, 0, 0}}, 0, true},
		{"other application", [][]byte{[]byte("SOMETHING1.0"), {1, 5, 0}}, 0, false},
		{"other sub-block", [][]byte{[]byte("NETSCAPE2.0"), {2, 5, 0, 0, 0}}, 0, false},
		{"too short", [][]byte{[]byte("NETSCAPE2.0"), {1, 5}}, 0, false},
		{"no data", [][]byte{[]byte("NETSCAPE2.0")}, 0, false},
		{"empty", nil, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBuilder(1, 1, nil)
			b.extension(extApplication, tt.blocks...)
			d, _ := decodeAll(t, b.trailer().bytes(), Options{})
			if m := d.Metadata(); m.LoopCount != tt.want || m.HasLoopCount != tt.has {
				t.Errorf("loop count = %d (present %v), want %d (present %v)",
					m.LoopCount, m.HasLoopCount, tt.want, tt.has)
			}
		})
	}
}

func TestTrailingData(t *testing.T) {
	frame := func() *builder {
		b := newBuilder(1, 1, greys(2))
		b.image(onePixel, nil, false, []byte{1})
		return b
	}
	big := bytes.Repeat([]byte("PK\x03\x04"), 25000) // 100 kB, larger than any buffer
	tests := []struct {
		name       string
		data       []byte
		missing    bool
		trailing   int64
		wantPrefix []byte
	}{
		{"clean", frame().trailer().bytes(), false, 0, nil},
		{"no trailer", frame().bytes(), true, 0, nil},
		{"appended file", append(frame().trailer().bytes(), big...), false, int64(len(big)), big[:trailingPrefixLen]},
		{"short tail", append(frame().trailer().bytes(), "tail"...), false, 4, []byte("tail")},
		{"junk instead of a block", append(frame().bytes(), 0x00, 0x2C, 0x3B), true, 3, []byte{0x00, 0x2C, 0x3B}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, frames := decodeAll(t, tt.data, Options{})
			if len(frames) != 1 {
				t.Errorf("got %d frames, want 1", len(frames))
			}
			m := d.Metadata()
			if m.MissingTrailer != tt.missing || m.TrailingBytes != tt.trailing {
				t.Errorf("MissingTrailer = %v, TrailingBytes = %d, want %v, %d",
					m.MissingTrailer, m.TrailingBytes, tt.missing, tt.trailing)
			}
			if !bytes.Equal(m.TrailingPrefix, tt.wantPrefix) {
				t.Errorf("TrailingPrefix = %d bytes %q..., want %d bytes",
					len(m.TrailingPrefix), m.TrailingPrefix[:min(8, len(m.TrailingPrefix))], len(tt.wantPrefix))
			}
		})
	}
}
