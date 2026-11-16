package gif

import (
	"io"
)

// trailingPrefixLen is how much of the data after the end of the GIF is kept.
const trailingPrefixLen = 512

// Metadata is everything in the file that is not a frame. It is filled in as
// decoding proceeds and is complete once Decoder.Next has returned io.EOF.
type Metadata struct {
	// LoopCount is how many times the animation repeats, with 0 meaning
	// forever. It is only meaningful when HasLoopCount is set; without a
	// loop extension viewers play the animation once.
	LoopCount    int
	HasLoopCount bool

	// Comments are the comment extensions, in file order.
	Comments []Comment
	// Applications are the application extensions, in file order,
	// including the one that carries the loop count.
	Applications []Application
	// UnknownExtensions are extensions of any other kind, apart from the
	// graphic control extensions that are reported on each Frame.
	UnknownExtensions []Extension
	// DroppedExtensions counts the comments and extensions left out of
	// the three lists above because Limits.MaxMetadataBytes was reached.
	DroppedExtensions int

	// MissingTrailer reports that the GIF was not closed by a trailer
	// block: the input ended, or continued with something that is not a
	// GIF block.
	MissingTrailer bool
	// TrailingBytes is how much input followed the end of the GIF.
	TrailingBytes int64
	// TrailingPrefix is the start of that input, at most 512 bytes of it.
	TrailingPrefix []byte
}

// Comment is a comment extension.
type Comment struct {
	// Offset is the position of the extension in the input.
	Offset int64
	// BeforeFrame is the index of the frame that follows the extension,
	// which equals the frame count if none does.
	BeforeFrame int
	// Data is the comment, shorter than Size if it was cut to stay
	// within Limits.MaxMetadataBytes.
	Data []byte
	// Size is the comment's full length in the file.
	Size int64
}

// Application is an application extension.
type Application struct {
	Offset      int64
	BeforeFrame int
	// Identifier is the application identifier and authentication code,
	// such as "NETSCAPE2.0" or "XMP DataXMP".
	Identifier string
	// Data is the payload after the identifier, shorter than Size if it
	// was cut.
	Data []byte
	Size int64
}

// Extension is an extension this package does not interpret.
type Extension struct {
	// Label is the byte that identifies the kind of extension.
	Label       byte
	Offset      int64
	BeforeFrame int
	Data        []byte
	Size        int64
}

// Metadata returns what has been read so far. The slices it holds belong to
// the decoder and must not be modified.
func (d *Decoder) Metadata() Metadata { return d.meta }

// readAll reads to the end of the chain. It returns the payload's size and
// its first limit bytes.
func (b *blockReader) readAll(limit int64) (data []byte, size int64, err error) {
	for {
		c, err := b.ReadByte()
		if err == io.EOF {
			return data, size, nil
		}
		if err != nil {
			return nil, 0, err
		}
		if size < limit {
			data = append(data, c)
		}
		size++
	}
}

// block reads the next sub-block whole. It returns nil at the terminator.
func (b *blockReader) block() ([]byte, error) {
	first, err := b.ReadByte()
	if err == io.EOF {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	blk := make([]byte, 1+b.left)
	blk[0] = first
	if _, err := io.ReadFull(b, blk[1:]); err != nil {
		return nil, err
	}
	return blk, nil
}

func (d *Decoder) readComment(br *blockReader, off int64) error {
	data, size, keep, err := d.readPayload(br)
	if err != nil || !keep {
		return err
	}
	d.meta.Comments = append(d.meta.Comments, Comment{
		Offset: off, BeforeFrame: d.frames, Data: data, Size: size,
	})
	return nil
}

func (d *Decoder) readApplication(br *blockReader, off int64) error {
	id, err := br.block()
	if err != nil {
		return err
	}
	data, size, keep, err := d.readPayload(br)
	if err != nil {
		return err
	}
	if keep {
		d.meta.Applications = append(d.meta.Applications, Application{
			Offset: off, BeforeFrame: d.frames, Identifier: string(id), Data: data, Size: size,
		})
	}
	// The loop count is sub-block 1 of the Netscape extension, which some
	// encoders write under the ANIMEXTS name. As in browsers, the last
	// one in the file wins.
	if s := string(id); (s == "NETSCAPE2.0" || s == "ANIMEXTS1.0") && len(data) >= 3 && data[0] == 1 {
		d.meta.LoopCount, d.meta.HasLoopCount = le16(data[1:]), true
	}
	return nil
}

func (d *Decoder) readUnknown(br *blockReader, label byte, off int64) error {
	data, size, keep, err := d.readPayload(br)
	if err != nil || !keep {
		return err
	}
	d.meta.UnknownExtensions = append(d.meta.UnknownExtensions, Extension{
		Label: label, Offset: off, BeforeFrame: d.frames, Data: data, Size: size,
	})
	return nil
}

// scanTrailing consumes the rest of the input, which is not part of the GIF,
// recording its size and its first bytes. already holds any of it that has
// been read.
func (d *Decoder) scanTrailing(already ...byte) error {
	m := &d.meta
	m.TrailingPrefix = append(m.TrailingPrefix, already...)
	m.TrailingBytes = int64(len(already))
	buf := make([]byte, 32<<10)
	for {
		n, err := d.r.br.Read(buf)
		d.r.off += int64(n)
		m.TrailingBytes += int64(n)
		if room := trailingPrefixLen - len(m.TrailingPrefix); room > 0 {
			m.TrailingPrefix = append(m.TrailingPrefix, buf[:min(n, room)]...)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
