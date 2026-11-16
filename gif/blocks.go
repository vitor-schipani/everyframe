package gif

import (
	"bufio"
	"compress/lzw"
	"fmt"
	"io"
)

// reader is the decoder's input. It tracks the byte offset so errors and
// findings can say where in the file they are.
type reader struct {
	br  *bufio.Reader
	off int64
}

// ReadByte returns io.EOF only at the very end of the input, which is how
// the block loop notices a file that stops without a trailer.
func (r *reader) ReadByte() (byte, error) {
	c, err := r.br.ReadByte()
	if err == nil {
		r.off++
	}
	return c, err
}

// mustByte reads a byte the format requires to be present.
func (r *reader) mustByte() (byte, error) {
	c, err := r.ReadByte()
	if err == io.EOF {
		return 0, r.truncated()
	}
	return c, err
}

// readFull fills p, treating a short read as truncation.
func (r *reader) readFull(p []byte) error {
	n, err := io.ReadFull(r.br, p)
	r.off += int64(n)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return r.truncated()
	}
	return err
}

func (r *reader) truncated() error {
	return fmt.Errorf("gif: truncated at offset %d: %w", r.off, io.ErrUnexpectedEOF)
}

// blockReader reads the payload of a chain of data sub-blocks: each is a
// length byte followed by that many bytes, and a zero length ends the chain.
// It returns io.EOF at the terminator and never reads past it.
//
// It implements io.ByteReader so that compress/lzw reads from it directly
// instead of buffering, which would consume bytes belonging to later blocks.
type blockReader struct {
	r    *reader
	left int   // unread bytes of the current sub-block
	done bool  // the terminator has been read
	err  error // input failure, as opposed to the normal end of the chain
}

func (b *blockReader) ReadByte() (byte, error) {
	if b.err != nil {
		return 0, b.err
	}
	if b.left == 0 {
		if b.done {
			return 0, io.EOF
		}
		n, err := b.r.mustByte()
		if err != nil {
			b.err = err
			return 0, err
		}
		if n == 0 {
			b.done = true
			return 0, io.EOF
		}
		b.left = int(n)
	}
	c, err := b.r.mustByte()
	if err != nil {
		b.err = err
		return 0, err
	}
	b.left--
	return c, nil
}

func (b *blockReader) Read(p []byte) (int, error) {
	for i := range p {
		c, err := b.ReadByte()
		if err != nil {
			return i, err
		}
		p[i] = c
	}
	return len(p), nil
}

// drain skips to the end of the chain and returns how many payload bytes it
// passed over.
func (b *blockReader) drain() (int64, error) {
	var n int64
	for {
		_, err := b.ReadByte()
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return n, err
		}
		n++
	}
}

// readPixels decodes one frame's LZW data into dst, which is in stream order.
// It reports how many pixels the data fell short by, and whether anything was
// left over once dst was full. Missing pixels are set to zero.
func (d *Decoder) readPixels(dst []byte) (missing int, surplus bool, err error) {
	off := d.r.off
	litWidth, err := d.r.mustByte()
	if err != nil {
		return 0, false, err
	}
	if litWidth < 2 || litWidth > 8 {
		return 0, false, &FormatError{Offset: off, Msg: fmt.Sprintf("invalid LZW code size %d", litWidth)}
	}
	br := &blockReader{r: &d.r}
	if d.lzw == nil {
		d.lzw = lzw.NewReader(br, lzw.LSB, int(litWidth)).(*lzw.Reader)
	} else {
		d.lzw.Reset(br, lzw.LSB, int(litWidth))
	}

	n, err := io.ReadFull(d.lzw, dst)
	if br.err != nil {
		return 0, false, br.err
	}
	switch err {
	case nil:
		// The stream should end here; one more pixel means it does not.
		var one [1]byte
		if m, _ := d.lzw.Read(one[:]); m > 0 {
			surplus = true
		}
		if br.err != nil {
			return 0, false, br.err
		}
	case io.EOF, io.ErrUnexpectedEOF:
		// The data ended early, with or without an end code.
		missing = len(dst) - n
		clear(dst[n:])
	default:
		return 0, false, &FormatError{Offset: off, Msg: "invalid LZW data: " + err.Error()}
	}

	rest, err := br.drain()
	if err != nil {
		return 0, false, err
	}
	return missing, surplus || rest > 0, nil
}

// interlacePasses lists, in stream order, the first row and row spacing of
// the four passes of an interlaced frame.
var interlacePasses = [4]struct{ start, step int }{{0, 8}, {4, 8}, {2, 4}, {1, 2}}

// deinterlace copies the rows of src, in interlaced stream order, to their
// display positions in dst. Both hold w*h pixels.
func deinterlace(dst, src []byte, w, h int) {
	row := 0
	for _, p := range interlacePasses {
		for y := p.start; y < h; y += p.step {
			copy(dst[y*w:(y+1)*w], src[row*w:(row+1)*w])
			row++
		}
	}
}

func le16(b []byte) int { return int(b[0]) | int(b[1])<<8 }
