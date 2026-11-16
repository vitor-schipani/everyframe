package gif

import (
	"errors"
	"fmt"
)

// Limits bounds the resources a single file can make the decoder use. A GIF
// of a few kilobytes can declare a canvas of four billion pixels or carry
// frames that expand a thousandfold, so a decoder for untrusted input needs
// them. A field left at zero takes its value from DefaultLimits.
type Limits struct {
	// MaxPixels caps the area of the canvas and of each frame. A frame
	// costs one byte per pixel, a Canvas four.
	MaxPixels int64
	// MaxFrames caps the number of frames.
	MaxFrames int
	// MaxDecodedBytes caps the pixels decoded over the whole file, which
	// bounds the time spent on it.
	MaxDecodedBytes int64
	// MaxMetadataBytes caps the memory spent on Metadata. Reaching it is
	// not an error: extension data past the cap is cut short or dropped,
	// and Metadata says so.
	MaxMetadataBytes int64
}

// DefaultLimits returns the limits used when none are set: a 64-megapixel
// canvas, 10,000 frames, 4 GiB of decoded pixels and 1 MiB of metadata.
func DefaultLimits() Limits {
	return Limits{
		MaxPixels:        64 << 20,
		MaxFrames:        10_000,
		MaxDecodedBytes:  4 << 30,
		MaxMetadataBytes: 1 << 20,
	}
}

func (l Limits) withDefaults() Limits {
	def := DefaultLimits()
	if l.MaxPixels == 0 {
		l.MaxPixels = def.MaxPixels
	}
	if l.MaxFrames == 0 {
		l.MaxFrames = def.MaxFrames
	}
	if l.MaxDecodedBytes == 0 {
		l.MaxDecodedBytes = def.MaxDecodedBytes
	}
	if l.MaxMetadataBytes == 0 {
		l.MaxMetadataBytes = def.MaxMetadataBytes
	}
	return l
}

// ErrLimit matches, through errors.Is, every error caused by a Limits bound.
var ErrLimit = errors.New("gif: resource limit exceeded")

// LimitError reports which bound of Limits a file exceeded.
type LimitError struct {
	// Limit names what was bounded: "canvas pixels", "frame pixels",
	// "frames" or "decoded bytes".
	Limit string
	// Value is what the file asked for and Max what was allowed.
	Value, Max int64
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("gif: %s limit exceeded: %d, at most %d allowed", e.Limit, e.Value, e.Max)
}

// Is makes errors.Is(err, ErrLimit) true for every LimitError.
func (e *LimitError) Is(target error) bool { return target == ErrLimit }

// extensionCost is what recording one extension is charged against
// MaxMetadataBytes on top of its payload, so that a file made of millions of
// empty extensions cannot grow Metadata without bound.
const extensionCost = 64

// loopDataLen is the payload of a loop-count extension, which is read even
// when the metadata budget is spent.
const loopDataLen = 3

// readPayload reads an extension's payload, keeping as much as the metadata
// budget allows. It reports keep as false when there is no room to record
// the extension at all, in which case it has been counted as dropped.
func (d *Decoder) readPayload(br *blockReader) (data []byte, size int64, keep bool, err error) {
	limit := int64(loopDataLen)
	if keep = d.metaLeft >= extensionCost; keep {
		d.metaLeft -= extensionCost
		limit = max(limit, d.metaLeft)
	}
	data, size, err = br.readAll(limit)
	if err != nil {
		return nil, 0, false, err
	}
	if keep {
		d.metaLeft = max(0, d.metaLeft-int64(len(data)))
	} else {
		d.meta.DroppedExtensions++
	}
	return data, size, keep, nil
}
