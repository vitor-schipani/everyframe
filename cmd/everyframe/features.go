package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/vitor-schipani/everyframe/features"
	"github.com/vitor-schipani/everyframe/gif"
)

// featuresReport is what the features command prints, as text or, with
// -json, exactly as declared here.
type featuresReport struct {
	File string `json:"file"`
	// Error is why decoding stopped early, if it did. The frames read
	// before that point are still measured.
	Error   string          `json:"error,omitempty"`
	Summary *featureSummary `json:"summary,omitempty"`
	Frames  []frameFeatures `json:"frames"`
}

type featureSummary struct {
	Width      int   `json:"width"`
	Height     int   `json:"height"`
	Frames     int   `json:"frames"`
	DurationMS int64 `json:"duration_ms"`
	MinDelayMS int64 `json:"min_delay_ms"`
	MaxDelayMS int64 `json:"max_delay_ms"`
	// ZeroDelayFrames counts frames with no delay at all.
	ZeroDelayFrames int `json:"zero_delay_frames"`
	// LoopCount is null when the file has no loop extension; 0 means forever.
	LoopCount *int `json:"loop_count"`

	DistinctHashes            int     `json:"distinct_hashes"`
	MaxChange                 float64 `json:"max_change"`
	MaxChangeFrame            int     `json:"max_change_frame"`
	MaxHashDistanceFirst      int     `json:"max_hash_distance_first"`
	MaxHashDistanceFirstFrame int     `json:"max_hash_distance_first_frame"`

	StoredPixels      int64 `json:"stored_pixels"`
	OutsidePixels     int64 `json:"outside_pixels"`
	TransparentPixels int64 `json:"transparent_pixels"`
	DrawnPixels       int64 `json:"drawn_pixels"`

	LocalPaletteFrames    int  `json:"local_palette_frames"`
	OutOfCanvasFrames     int  `json:"out_of_canvas_frames"`
	MissingPixelsFrames   int  `json:"missing_pixels_frames"`
	SurplusDataFrames     int  `json:"surplus_data_frames"`
	IndexOutOfRangeFrames int  `json:"index_out_of_range_frames"`
	Truncated             bool `json:"truncated"`

	Comments              int   `json:"comments"`
	CommentBytes          int64 `json:"comment_bytes"`
	Applications          int   `json:"applications"`
	ApplicationBytes      int64 `json:"application_bytes"`
	UnknownExtensions     int   `json:"unknown_extensions"`
	UnknownExtensionBytes int64 `json:"unknown_extension_bytes"`
	DroppedExtensions     int   `json:"dropped_extensions"`
	MissingTrailer        bool  `json:"missing_trailer"`
	TrailingBytes         int64 `json:"trailing_bytes"`
}

type frameFeatures struct {
	Index             int     `json:"index"`
	DelayMS           int64   `json:"delay_ms"`
	StoredPixels      int64   `json:"stored_pixels"`
	OutsidePixels     int64   `json:"outside_pixels"`
	TransparentPixels int64   `json:"transparent_pixels"`
	DrawnPixels       int64   `json:"drawn_pixels"`
	Colours           int     `json:"colours"`
	ChangedPixels     int64   `json:"changed_pixels"`
	Change            float64 `json:"change"`
	// Hash is 16 hexadecimal digits.
	Hash              string `json:"hash"`
	HashDistance      int    `json:"hash_distance"`
	HashDistanceFirst int    `json:"hash_distance_first"`
}

func runFeatures(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := newCommand("features", `Measure a GIF for detection: timing, how much of each frame is shown, how far
the picture moves between frames, and a perceptual hash of every picture.`, stderr)
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

	rep, decodeErr := measure(in, name, cmd.limits)
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

// measure extracts the features of the whole file. When decoding fails
// part-way, the report covers what came before and the error is returned as
// well.
func measure(r io.Reader, name string, limits gif.Limits) (*featuresReport, error) {
	rep := &featuresReport{File: name, Frames: []frameFeatures{}}
	d, err := gif.NewDecoder(r, gif.Options{Limits: limits})
	if err != nil {
		rep.Error = err.Error()
		return rep, err
	}
	res, err := features.Extract(d)
	if err != nil {
		rep.Error = err.Error()
	}
	for _, f := range res.Frames {
		rep.Frames = append(rep.Frames, frameFeatures{
			Index:             f.Index,
			DelayMS:           f.Delay.Milliseconds(),
			StoredPixels:      f.StoredPixels,
			OutsidePixels:     f.OutsidePixels,
			TransparentPixels: f.TransparentPixels,
			DrawnPixels:       f.DrawnPixels,
			Colours:           f.Colours,
			ChangedPixels:     f.ChangedPixels,
			Change:            f.Change,
			Hash:              f.Hash.String(),
			HashDistance:      f.HashDistance,
			HashDistanceFirst: f.HashDistanceFirst,
		})
	}
	s := res.Summary
	rep.Summary = &featureSummary{
		Width:                     s.Width,
		Height:                    s.Height,
		Frames:                    s.Frames,
		DurationMS:                s.Duration.Milliseconds(),
		MinDelayMS:                s.MinDelay.Milliseconds(),
		MaxDelayMS:                s.MaxDelay.Milliseconds(),
		ZeroDelayFrames:           s.ZeroDelayFrames,
		DistinctHashes:            s.DistinctHashes,
		MaxChange:                 s.MaxChange,
		MaxChangeFrame:            s.MaxChangeFrame,
		MaxHashDistanceFirst:      s.MaxHashDistanceFirst,
		MaxHashDistanceFirstFrame: s.MaxHashDistanceFirstFrame,
		StoredPixels:              s.StoredPixels,
		OutsidePixels:             s.OutsidePixels,
		TransparentPixels:         s.TransparentPixels,
		DrawnPixels:               s.DrawnPixels,
		LocalPaletteFrames:        s.LocalPaletteFrames,
		OutOfCanvasFrames:         s.OutOfCanvasFrames,
		MissingPixelsFrames:       s.MissingPixelsFrames,
		SurplusDataFrames:         s.SurplusDataFrames,
		IndexOutOfRangeFrames:     s.IndexOutOfRangeFrames,
		Truncated:                 s.Truncated,
		Comments:                  s.Comments,
		CommentBytes:              s.CommentBytes,
		Applications:              s.Applications,
		ApplicationBytes:          s.ApplicationBytes,
		UnknownExtensions:         s.UnknownExtensions,
		UnknownExtensionBytes:     s.UnknownExtensionBytes,
		DroppedExtensions:         s.DroppedExtensions,
		MissingTrailer:            s.MissingTrailer,
		TrailingBytes:             s.TrailingBytes,
	}
	if s.HasLoopCount {
		rep.Summary.LoopCount = &s.LoopCount
	}
	return rep, err
}

// print writes the report as text.
func (r *featuresReport) print(w io.Writer) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	row := func(label, format string, args ...any) {
		_, _ = fmt.Fprintf(tw, "%s\t%s\n", label, fmt.Sprintf(format, args...))
	}
	ms := func(n int64) time.Duration { return time.Duration(n) * time.Millisecond }
	row("file", "%s", r.File)
	if s := r.Summary; s != nil {
		row("canvas", "%dx%d", s.Width, s.Height)
		row("frames", "%d", s.Frames)
		row("duration", "%s", ms(s.DurationMS))
		row("delays", "%s to %s, %d frames with none", ms(s.MinDelayMS), ms(s.MaxDelayMS), s.ZeroDelayFrames)
		switch {
		case s.LoopCount == nil:
			row("loop count", "not set")
		case *s.LoopCount == 0:
			row("loop count", "0 (forever)")
		default:
			row("loop count", "%d", *s.LoopCount)
		}
		row("distinct hashes", "%d", s.DistinctHashes)
		row("largest change", "%.4f at frame %d", s.MaxChange, s.MaxChangeFrame)
		row("furthest from first", "hash distance %d at frame %d", s.MaxHashDistanceFirst, s.MaxHashDistanceFirstFrame)
		row("pixels", "%d stored, %d outside the canvas, %d transparent, %d drawn",
			s.StoredPixels, s.OutsidePixels, s.TransparentPixels, s.DrawnPixels)
		row("local palettes", "%d frames", s.LocalPaletteFrames)

		var irregular []string
		add := func(n int, name string) {
			if n > 0 {
				irregular = append(irregular, fmt.Sprintf("%d %s", n, name))
			}
		}
		add(s.OutOfCanvasFrames, "out-of-canvas")
		add(s.MissingPixelsFrames, "missing-pixels")
		add(s.SurplusDataFrames, "surplus-data")
		add(s.IndexOutOfRangeFrames, "index-out-of-range")
		if s.Truncated {
			irregular = append(irregular, "last frame truncated")
		}
		if len(irregular) == 0 {
			irregular = []string{"none"}
		}
		row("irregular frames", "%s", strings.Join(irregular, ", "))

		row("comments", "%d, %d bytes", s.Comments, s.CommentBytes)
		row("applications", "%d, %d bytes", s.Applications, s.ApplicationBytes)
		row("unknown extensions", "%d, %d bytes", s.UnknownExtensions, s.UnknownExtensionBytes)
		if s.DroppedExtensions > 0 {
			row("dropped extensions", "%d (metadata limit reached)", s.DroppedExtensions)
		}
		switch {
		case r.Error != "":
			row("trailer", "not reached")
		case s.MissingTrailer:
			row("trailer", "missing")
		default:
			row("trailer", "present")
		}
		row("trailing data", "%d bytes", s.TrailingBytes)
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
	_, _ = fmt.Fprintln(tw, "frame\tdelay\tstored\toutside\ttransparent\tdrawn\tcolours\tchanged\tchange\thash\tdistance\tfrom first")
	for _, f := range r.Frames {
		_, _ = fmt.Fprintf(tw, "%d\t%dms\t%d\t%d\t%d\t%d\t%d\t%d\t%.4f\t%s\t%d\t%d\n",
			f.Index, f.DelayMS, f.StoredPixels, f.OutsidePixels, f.TransparentPixels, f.DrawnPixels,
			f.Colours, f.ChangedPixels, f.Change, f.Hash, f.HashDistance, f.HashDistanceFirst)
	}
	_ = tw.Flush()
}
