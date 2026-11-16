package gif

import (
	"bytes"
	"compress/lzw"
	"image"
	"image/color"
	"math/bits"
	"testing"
)

// builder assembles a GIF block by block, so tests can produce files that no
// encoder would write.
type builder struct{ buf bytes.Buffer }

// newBuilder starts a GIF89a file. A nil global palette is left out.
func newBuilder(w, h int, global color.Palette) *builder {
	b := &builder{}
	b.buf.WriteString("GIF89a")
	b.le16(w)
	b.le16(h)
	b.buf.WriteByte(paletteFlags(global))
	b.buf.WriteByte(0) // background index
	b.buf.WriteByte(0) // pixel aspect
	b.palette(global)
	return b
}

func (b *builder) le16(v int) { b.buf.Write([]byte{byte(v), byte(v >> 8)}) }

// paletteFlags is the "table present" bit plus the table size field.
func paletteFlags(p color.Palette) byte {
	if p == nil {
		return 0
	}
	return 0x80 | byte(bits.Len(uint(len(p)))-2)
}

func (b *builder) palette(p color.Palette) {
	for _, c := range p {
		r, g, bl, _ := c.RGBA()
		b.buf.Write([]byte{byte(r >> 8), byte(g >> 8), byte(bl >> 8)})
	}
}

// control writes a graphic control extension for the next image. A negative
// transparent index means none.
func (b *builder) control(disposal Disposal, delayCS, transparent int) {
	packed := byte(disposal) << 2
	if transparent >= 0 {
		packed |= 1
	} else {
		transparent = 0
	}
	b.extension(extGraphicControl, []byte{packed, byte(delayCS), byte(delayCS >> 8), byte(transparent)})
}

// image writes a frame whose pixels are given in stream order.
func (b *builder) image(r image.Rectangle, local color.Palette, interlaced bool, pixels []byte) {
	b.imageData(r, local, interlaced, 8, compress(pixels))
}

// imageData writes a frame from already compressed data.
func (b *builder) imageData(r image.Rectangle, local color.Palette, interlaced bool, litWidth byte, data []byte) {
	b.buf.WriteByte(blockImage)
	b.le16(r.Min.X)
	b.le16(r.Min.Y)
	b.le16(r.Dx())
	b.le16(r.Dy())
	packed := paletteFlags(local)
	if interlaced {
		packed |= 0x40
	}
	b.buf.WriteByte(packed)
	b.palette(local)
	b.buf.WriteByte(litWidth)
	b.blocks(data)
}

// extension writes an extension whose sub-blocks are given one by one.
func (b *builder) extension(label byte, blocks ...[]byte) {
	b.buf.Write([]byte{blockExtension, label})
	for _, blk := range blocks {
		b.buf.WriteByte(byte(len(blk)))
		b.buf.Write(blk)
	}
	b.buf.WriteByte(0)
}

// blocks writes data as a chain of sub-blocks with its terminator.
func (b *builder) blocks(data []byte) {
	for len(data) > 0 {
		n := min(len(data), 255)
		b.buf.WriteByte(byte(n))
		b.buf.Write(data[:n])
		data = data[n:]
	}
	b.buf.WriteByte(0)
}

func (b *builder) trailer() *builder {
	b.buf.WriteByte(blockTrailer)
	return b
}

func (b *builder) bytes() []byte { return b.buf.Bytes() }

// compress LZW-encodes pixels with 8-bit literals.
func compress(pixels []byte) []byte {
	var buf bytes.Buffer
	w := lzw.NewWriter(&buf, lzw.LSB, 8)
	if _, err := w.Write(pixels); err != nil {
		panic(err)
	}
	if err := w.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// greys returns an n-colour palette; n must be a power of two from 2 to 256.
func greys(n int) color.Palette {
	p := make(color.Palette, n)
	for i := range p {
		v := uint8(i * 255 / (n - 1))
		p[i] = color.RGBA{R: v, G: v, B: v, A: 0xFF}
	}
	return p
}

// decodeAll decodes data to the end, copying each frame's pixels.
func decodeAll(t *testing.T, data []byte, opts Options) (*Decoder, []*Frame) {
	t.Helper()
	d, err := NewDecoder(bytes.NewReader(data), opts)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	var frames []*Frame
	for f, err := range d.Frames() {
		if err != nil {
			t.Fatalf("frame %d: %v", len(frames), err)
		}
		c := *f
		c.Pixels = bytes.Clone(f.Pixels)
		frames = append(frames, &c)
	}
	return d, frames
}
