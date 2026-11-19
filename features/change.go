package features

import (
	"bytes"
	"image"
)

// sync brings prev in line with the canvas inside area. It returns how many
// pixels had changed and the sum of their differences over the four
// channels, and moves each changed pixel's brightness to its new value in
// the grid.
func (e *Extractor) sync(shown *image.RGBA, area image.Rectangle) (changed, diff int64) {
	n := 4 * area.Dx()
	for y := area.Min.Y; y < area.Max.Y; y++ {
		i := shown.PixOffset(area.Min.X, y)
		cur, old := shown.Pix[i:i+n], e.prev[i:i+n]
		if bytes.Equal(cur, old) {
			continue
		}
		cells := e.luma[int(e.row[y])*hashCols:]
		for x := 0; x < n; x += 4 {
			c, o := cur[x:x+4:x+4], old[x:x+4:x+4]
			if c[0] == o[0] && c[1] == o[1] && c[2] == o[2] && c[3] == o[3] {
				continue
			}
			changed++
			diff += absDiff(c[0], o[0]) + absDiff(c[1], o[1]) + absDiff(c[2], o[2]) + absDiff(c[3], o[3])
			cells[e.col[area.Min.X+x/4]] += brightness(c) - brightness(o)
			copy(o, c)
		}
	}
	return changed, diff
}

func absDiff(a, b byte) int64 {
	if a > b {
		return int64(a - b)
	}
	return int64(b - a)
}
