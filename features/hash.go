package features

import (
	"fmt"
	"math/bits"
)

// Hash is a 64-bit perceptual hash of a picture. Pictures that look alike
// have hashes that differ in few bits, whatever their size.
//
// It is a difference hash: the picture is reduced to 8 rows of 9 brightness
// averages, and each bit says whether one average is brighter than the one
// to its right. Transparent pixels count as black.
type Hash uint64

// String returns the hash as 16 hexadecimal digits.
func (h Hash) String() string { return fmt.Sprintf("%016x", uint64(h)) }

// Distance returns how many of the 64 bits differ between h and other: 0 for
// pictures that hash alike, around 32 for unrelated ones.
func (h Hash) Distance(other Hash) int { return bits.OnesCount64(uint64(h ^ other)) }

// The picture is hashed from a grid of hashCols x hashRows brightness
// averages.
const (
	hashCols = 9
	hashRows = 8
)

// brightness is the luma of an RGBA pixel, in thousandths of a level. A
// transparent pixel is all zeros, so it counts as black.
func brightness(p []byte) int64 {
	return 299*int64(p[0]) + 587*int64(p[1]) + 114*int64(p[2])
}

// hash computes the hash of the current picture from the brightness grid.
func (e *Extractor) hash() Hash {
	// A canvas narrower than the grid leaves columns of cells without
	// pixels, and a shorter one rows. Such a cell takes the value of the
	// nearest one before it that has pixels, as if the picture had been
	// stretched to fit.
	var mean [hashRows * hashCols]float64
	for i, n := range e.count {
		switch {
		case n > 0:
			mean[i] = float64(e.luma[i]) / float64(n)
		case e.count[i-i%hashCols] > 0: // the row has pixels, this column none
			mean[i] = mean[i-1]
		case i >= hashCols:
			mean[i] = mean[i-hashCols]
		}
	}
	var h Hash
	for r := range hashRows {
		for c := range hashCols - 1 {
			h <<= 1
			if i := r*hashCols + c; mean[i] > mean[i+1] {
				h |= 1
			}
		}
	}
	return h
}
