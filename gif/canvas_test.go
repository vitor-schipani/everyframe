package gif

import (
	"bytes"
	"image"
	"image/color"
	"strings"
	"testing"
)

// testColours is the palette used by the canvas tests, with the letter show
// prints for each entry.
var testColours = color.Palette{
	color.RGBA{R: 0xFF, A: 0xFF},                   // 0: R
	color.RGBA{G: 0xFF, A: 0xFF},                   // 1: G
	color.RGBA{B: 0xFF, A: 0xFF},                   // 2: B
	color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}, // 3: W
}

// show renders a canvas as rows of letters separated by slashes, with a dot
// for a transparent pixel.
func show(img *image.RGBA) string {
	var sb strings.Builder
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		if y > b.Min.Y {
			sb.WriteByte('/')
		}
		for x := b.Min.X; x < b.Max.X; x++ {
			switch img.RGBAAt(x, y) {
			case color.RGBA{}:
				sb.WriteByte('.')
			case testColours[0]:
				sb.WriteByte('R')
			case testColours[1]:
				sb.WriteByte('G')
			case testColours[2]:
				sb.WriteByte('B')
			case testColours[3]:
				sb.WriteByte('W')
			default:
				sb.WriteByte('?')
			}
		}
	}
	return sb.String()
}

// fill returns a frame covering r with a single palette index.
func fill(r image.Rectangle, index byte, disposal Disposal) *Frame {
	return &Frame{
		Rect:        r,
		Pixels:      bytes.Repeat([]byte{index}, r.Dx()*r.Dy()),
		Palette:     testColours,
		Transparent: -1,
		Disposal:    disposal,
	}
}

func TestCanvas(t *testing.T) {
	full := image.Rect(0, 0, 4, 3)
	corner := image.Rect(0, 0, 2, 2)
	dot := image.Rect(3, 2, 4, 3)

	tests := []struct {
		name   string
		frames []*Frame
		want   []string // the canvas after each frame
	}{
		{
			name: "transparent pixels show the frame below",
			frames: []*Frame{
				fill(full, 0, DisposalKeep),
				{
					Rect:        image.Rect(1, 0, 3, 2),
					Pixels:      []byte{1, 3, 3, 1},
					Palette:     testColours,
					Transparent: 3,
				},
			},
			want: []string{"RRRR/RRRR/RRRR", "RGRR/RRGR/RRRR"},
		},
		{
			name: "unspecified disposal keeps the frame",
			frames: []*Frame{
				fill(corner, 2, DisposalUnspecified),
				fill(dot, 1, DisposalUnspecified),
			},
			want: []string{"BB../BB../....", "BB../BB../...G"},
		},
		{
			name: "background disposal clears the frame's area",
			frames: []*Frame{
				fill(full, 0, DisposalKeep),
				fill(corner, 2, DisposalBackground),
				fill(dot, 1, DisposalKeep),
			},
			want: []string{"RRRR/RRRR/RRRR", "BBRR/BBRR/RRRR", "..RR/..RR/RRRG"},
		},
		{
			name: "previous disposal restores what was there",
			frames: []*Frame{
				fill(full, 0, DisposalKeep),
				fill(corner, 2, DisposalPrevious),
				fill(dot, 1, DisposalPrevious),
				fill(corner, 3, DisposalKeep),
			},
			want: []string{"RRRR/RRRR/RRRR", "BBRR/BBRR/RRRR", "RRRR/RRRR/RRRG", "WWRR/WWRR/RRRR"},
		},
		{
			name: "reserved disposal 4 acts as previous",
			frames: []*Frame{
				fill(full, 0, DisposalKeep),
				fill(corner, 2, 4),
				fill(dot, 1, DisposalKeep),
			},
			want: []string{"RRRR/RRRR/RRRR", "BBRR/BBRR/RRRR", "RRRR/RRRR/RRRG"},
		},
		{
			name: "previous disposal on the first frame restores an empty canvas",
			frames: []*Frame{
				fill(corner, 2, DisposalPrevious),
				fill(dot, 1, DisposalKeep),
			},
			want: []string{"BB../BB../....", "..../..../...G"},
		},
		{
			name: "frames are clipped to the canvas",
			frames: []*Frame{
				fill(image.Rect(2, 1, 6, 5), 1, DisposalBackground),
				fill(image.Rect(-1, -1, 1, 1), 2, DisposalPrevious),
				fill(image.Rect(10, 10, 12, 12), 3, DisposalKeep),
			},
			want: []string{"..../..GG/..GG", "B.../..../....", "..../..../...."},
		},
		{
			name: "indices without a palette entry are not drawn",
			frames: []*Frame{
				{Rect: image.Rect(0, 0, 4, 1), Pixels: []byte{0, 1, 4, 200}, Palette: testColours, Transparent: -1},
				{Rect: image.Rect(0, 1, 4, 2), Pixels: []byte{0, 1, 2, 3}, Transparent: -1},
			},
			want: []string{"RG../..../....", "RG../..../...."},
		},
		{
			name: "a frame with too few pixels draws the rows it has",
			frames: []*Frame{
				{Rect: full, Pixels: []byte{0, 0, 0, 0, 0}, Palette: testColours, Transparent: -1},
			},
			want: []string{"RRRR/..../...."},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCanvas(Header{Width: 4, Height: 3})
			for i, f := range tt.frames {
				if got := show(c.Draw(f)); got != tt.want[i] {
					t.Errorf("after frame %d: %s, want %s", i, got, tt.want[i])
				}
			}
		})
	}
}

func TestCanvasFromDecoder(t *testing.T) {
	b := newBuilder(3, 2, testColours)
	b.control(DisposalBackground, 10, -1)
	b.image(image.Rect(0, 0, 3, 2), nil, false, []byte{0, 0, 0, 0, 0, 0})
	b.control(DisposalKeep, 10, 0)
	b.image(image.Rect(1, 0, 3, 2), nil, true, []byte{2, 0, 0, 2})
	d, err := NewDecoder(bytes.NewReader(b.trailer().bytes()), Options{})
	if err != nil {
		t.Fatal(err)
	}
	c := NewCanvas(d.Header())
	var got []string
	for f, err := range d.Frames() {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, show(c.Draw(f)))
	}
	if want := "RRR/RRR,.B./..B"; strings.Join(got, ",") != want {
		t.Errorf("canvas sequence = %s, want %s", strings.Join(got, ","), want)
	}
}

func TestEmptyCanvas(t *testing.T) {
	c := NewCanvas(Header{})
	if img := c.Draw(fill(image.Rect(0, 0, 2, 2), 1, DisposalPrevious)); !img.Bounds().Empty() {
		t.Errorf("canvas bounds = %v, want empty", img.Bounds())
	}
	c.Draw(fill(image.Rect(0, 0, 2, 2), 1, DisposalBackground))
	c.Draw(fill(image.Rect(0, 0, 2, 2), 1, DisposalKeep))
}
