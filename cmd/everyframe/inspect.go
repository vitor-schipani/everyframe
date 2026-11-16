package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/vitor-schipani/everyframe/gif"
)

// report is everything inspect knows about a file. It is printed as text or,
// with -json, exactly as declared here.
type report struct {
	File string `json:"file"`
	// Error is why decoding stopped early, if it did. Whatever was read
	// before that point is still reported.
	Error      string         `json:"error,omitempty"`
	Header     *headerReport  `json:"header,omitempty"`
	DurationMS int64          `json:"duration_ms"`
	Frames     []frameReport  `json:"frames"`
	Metadata   metadataReport `json:"metadata"`
}

type headerReport struct {
	Version           string `json:"version"`
	Width             int    `json:"width"`
	Height            int    `json:"height"`
	GlobalPaletteSize int    `json:"global_palette_size"`
	BackgroundIndex   uint8  `json:"background_index"`
	PixelAspect       uint8  `json:"pixel_aspect"`
}

type frameReport struct {
	Index            int    `json:"index"`
	Offset           int64  `json:"offset"`
	X                int    `json:"x"`
	Y                int    `json:"y"`
	Width            int    `json:"width"`
	Height           int    `json:"height"`
	DelayMS          int64  `json:"delay_ms"`
	Disposal         string `json:"disposal"`
	TransparentIndex *int   `json:"transparent_index"`
	LocalPalette     bool   `json:"local_palette"`
	PaletteSize      int    `json:"palette_size"`
	Interlaced       bool   `json:"interlaced"`
	UserInput        bool   `json:"user_input"`
	MissingPixels    int    `json:"missing_pixels"`
	// Flags names each irregularity of the frame; see frameFlags.
	Flags []string `json:"flags"`
}

type metadataReport struct {
	// LoopCount is null when the file has no loop extension; 0 means forever.
	LoopCount         *int                `json:"loop_count"`
	Comments          []commentReport     `json:"comments"`
	Applications      []applicationReport `json:"applications"`
	UnknownExtensions []extensionReport   `json:"unknown_extensions"`
	DroppedExtensions int                 `json:"dropped_extensions"`
	MissingTrailer    bool                `json:"missing_trailer"`
	TrailingBytes     int64               `json:"trailing_bytes"`
	// TrailingPrefix is the start of the trailing data, base64-encoded.
	TrailingPrefix []byte `json:"trailing_prefix,omitempty"`
}

type commentReport struct {
	Offset      int64  `json:"offset"`
	BeforeFrame int    `json:"before_frame"`
	Size        int64  `json:"size"`
	Text        string `json:"text"`
}

type applicationReport struct {
	Offset      int64  `json:"offset"`
	BeforeFrame int    `json:"before_frame"`
	Identifier  string `json:"identifier"`
	Size        int64  `json:"size"`
}

type extensionReport struct {
	Label       byte  `json:"label"`
	Offset      int64 `json:"offset"`
	BeforeFrame int   `json:"before_frame"`
	Size        int64 `json:"size"`
}

func runInspect(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := newCommand("inspect", "Describe a GIF: its header, every frame, the metadata, and anything irregular.", stderr)
	asJSON := cmd.Bool("json", false, "print the report as JSON")
	name, err := cmd.parse(args)
	if err != nil {
		return err
	}
	in, err := open(name, stdin)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	rep, decodeErr := inspect(in, name, cmd.limits)
	out := bufio.NewWriter(stdout)
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
	} else {
		rep.print(out)
	}
	if err := out.Flush(); err != nil {
		return err
	}
	// The report already says why decoding stopped; the error still
	// decides the exit code.
	return decodeErr
}

// inspect decodes the whole file. When decoding fails part-way, the report
// covers what came before and the error is returned as well.
func inspect(r io.Reader, name string, limits gif.Limits) (*report, error) {
	rep := &report{
		File:   name,
		Frames: []frameReport{},
		Metadata: metadataReport{
			Comments:          []commentReport{},
			Applications:      []applicationReport{},
			UnknownExtensions: []extensionReport{},
		},
	}
	d, err := gif.NewDecoder(r, gif.Options{Limits: limits})
	if err != nil {
		rep.Error = err.Error()
		return rep, err
	}
	h := d.Header()
	rep.Header = &headerReport{
		Version:           h.Version,
		Width:             h.Width,
		Height:            h.Height,
		GlobalPaletteSize: len(h.GlobalPalette),
		BackgroundIndex:   h.BackgroundIndex,
		PixelAspect:       h.PixelAspect,
	}
	for f, ferr := range d.Frames() {
		if ferr != nil {
			rep.Error, err = ferr.Error(), ferr
			break
		}
		fr := frameReport{
			Index:         f.Index,
			Offset:        f.Offset,
			X:             f.Rect.Min.X,
			Y:             f.Rect.Min.Y,
			Width:         f.Rect.Dx(),
			Height:        f.Rect.Dy(),
			DelayMS:       f.Delay.Milliseconds(),
			Disposal:      f.Disposal.String(),
			LocalPalette:  f.LocalPalette,
			PaletteSize:   len(f.Palette),
			Interlaced:    f.Interlaced,
			UserInput:     f.UserInput,
			MissingPixels: f.MissingPixels,
			Flags:         frameFlags(f),
		}
		if f.Transparent >= 0 {
			fr.TransparentIndex = &f.Transparent
		}
		rep.Frames = append(rep.Frames, fr)
		rep.DurationMS += fr.DelayMS
	}

	m := d.Metadata()
	md := &rep.Metadata
	if m.HasLoopCount {
		md.LoopCount = &m.LoopCount
	}
	for _, c := range m.Comments {
		md.Comments = append(md.Comments, commentReport{c.Offset, c.BeforeFrame, c.Size, string(c.Data)})
	}
	for _, a := range m.Applications {
		md.Applications = append(md.Applications, applicationReport{a.Offset, a.BeforeFrame, a.Identifier, a.Size})
	}
	for _, e := range m.UnknownExtensions {
		md.UnknownExtensions = append(md.UnknownExtensions, extensionReport{e.Label, e.Offset, e.BeforeFrame, e.Size})
	}
	md.DroppedExtensions = m.DroppedExtensions
	md.MissingTrailer = m.MissingTrailer
	md.TrailingBytes = m.TrailingBytes
	md.TrailingPrefix = m.TrailingPrefix
	return rep, err
}

// frameFlags names the irregularities of a frame. The result is never nil,
// so it prints as an empty JSON list.
func frameFlags(f *gif.Frame) []string {
	flags := []string{}
	add := func(set bool, name string) {
		if set {
			flags = append(flags, name)
		}
	}
	add(f.OutOfCanvas, "out-of-canvas")
	add(f.Truncated, "truncated")
	add(f.MissingPixels > 0 && !f.Truncated, "missing-pixels")
	add(f.SurplusData, "surplus-data")
	add(f.Palette == nil, "no-palette")
	add(f.IndexOutOfRange && f.Palette != nil, "index-out-of-range")
	return flags
}

// print writes the report as text.
func (r *report) print(w io.Writer) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	row := func(label, format string, args ...any) {
		_, _ = fmt.Fprintf(tw, "%s\t%s\n", label, fmt.Sprintf(format, args...))
	}
	row("file", "%s", r.File)
	if h := r.Header; h != nil {
		row("format", "GIF%s", h.Version)
		row("canvas", "%dx%d", h.Width, h.Height)
		if h.GlobalPaletteSize > 0 {
			row("global palette", "%d colours", h.GlobalPaletteSize)
		} else {
			row("global palette", "none")
		}
		row("frames", "%d", len(r.Frames))
		row("duration", "%s", time.Duration(r.DurationMS)*time.Millisecond)

		m := r.Metadata
		switch {
		case m.LoopCount == nil:
			row("loop count", "not set")
		case *m.LoopCount == 0:
			row("loop count", "0 (forever)")
		default:
			row("loop count", "%d", *m.LoopCount)
		}
		row("comments", "%d", len(m.Comments))
		for _, c := range m.Comments {
			row("", "before frame %d, %d bytes: %s", c.BeforeFrame, c.Size, excerpt(c.Text))
		}
		row("applications", "%d", len(m.Applications))
		for _, a := range m.Applications {
			row("", "before frame %d, %d bytes: %q", a.BeforeFrame, a.Size, a.Identifier)
		}
		if n := len(m.UnknownExtensions); n > 0 {
			row("unknown extensions", "%d", n)
			for _, e := range m.UnknownExtensions {
				row("", "before frame %d, %d bytes: label 0x%02X", e.BeforeFrame, e.Size, e.Label)
			}
		}
		if m.DroppedExtensions > 0 {
			row("dropped extensions", "%d (metadata limit reached)", m.DroppedExtensions)
		}
		switch {
		case r.Error != "":
			row("trailer", "not reached")
		case m.MissingTrailer:
			row("trailer", "missing")
		default:
			row("trailer", "present")
		}
		if m.TrailingBytes > 0 {
			row("trailing data", "%d bytes, starting %s", m.TrailingBytes, excerpt(string(m.TrailingPrefix)))
		} else {
			row("trailing data", "none")
		}
	}
	if r.Error != "" {
		row("error", "%s", r.Error)
	}
	_ = tw.Flush()

	if len(r.Frames) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w)
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "frame\toffset\tsize\tposition\tdelay\tdisposal\tpalette\ttransparent\tflags")
	for _, f := range r.Frames {
		palette := fmt.Sprintf("global %d", f.PaletteSize)
		switch {
		case f.LocalPalette:
			palette = fmt.Sprintf("local %d", f.PaletteSize)
		case f.PaletteSize == 0:
			palette = "none"
		}
		transparent := "-"
		if f.TransparentIndex != nil {
			transparent = fmt.Sprint(*f.TransparentIndex)
		}
		flags := "-"
		if len(f.Flags) > 0 {
			flags = strings.Join(f.Flags, ",")
		}
		_, _ = fmt.Fprintf(tw, "%d\t%d\t%dx%d\t%d,%d\t%dms\t%s\t%s\t%s\t%s\n",
			f.Index, f.Offset, f.Width, f.Height, f.X, f.Y, f.DelayMS, f.Disposal, palette, transparent, flags)
	}
	_ = tw.Flush()
}

// excerpt quotes the start of s, so that text of any length or content fits
// on one line of the report.
func excerpt(s string) string {
	const limit = 60
	if len(s) > limit {
		return fmt.Sprintf("%q...", s[:limit])
	}
	return fmt.Sprintf("%q", s)
}
