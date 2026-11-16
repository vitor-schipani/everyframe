package main

import (
	"bufio"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"

	"github.com/vitor-schipani/everyframe/gif"
)

// The pictures the frames command can write for each frame.
const (
	viewStored = "stored" // the frame's own rectangle, as it is in the file
	viewShown  = "shown"  // the whole canvas, as a viewer displays it
	viewBoth   = "both"
)

func runFrames(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := newCommand("frames", `Write every frame of a GIF as PNG files, named by frame number:
  00000-stored.png   the frame's own rectangle, as it is in the file
  00000-shown.png    the whole canvas, as a viewer displays it at that frame
Existing files with those names are overwritten.`, stderr)
	dir := cmd.String("o", "", "directory to write into, created if needed (required)")
	view := cmd.String("view", viewBoth, "which pictures to write: stored, shown or both")
	name, err := cmd.parse(args)
	if err != nil {
		return err
	}
	if *dir == "" {
		_, _ = fmt.Fprintln(stderr, "everyframe frames: -o is required")
		cmd.Usage()
		return errUsage
	}
	if *view != viewStored && *view != viewShown && *view != viewBoth {
		_, _ = fmt.Fprintf(stderr, "everyframe frames: -view must be stored, shown or both, not %q\n", *view)
		return errUsage
	}

	in, err := open(name, stdin)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	d, err := gif.NewDecoder(in, gif.Options{Limits: cmd.limits})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		return err
	}

	canvas := gif.NewCanvas(d.Header())
	frames, files := 0, 0
	var decodeErr error
	for f, err := range d.Frames() {
		if err != nil {
			decodeErr = err
			break
		}
		frames++
		// Every frame is drawn, even if only stored ones are wanted, so
		// that the canvas stays in step.
		shown := canvas.Draw(f)
		pictures := map[string]image.Image{viewStored: f.Image(), viewShown: shown}
		for _, v := range []string{viewStored, viewShown} {
			if *view != v && *view != viewBoth {
				continue
			}
			img := pictures[v]
			if img.Bounds().Empty() {
				_, _ = fmt.Fprintf(stderr, "everyframe frames: frame %d: the %s picture is empty, not written\n", f.Index, v)
				continue
			}
			path := filepath.Join(*dir, fmt.Sprintf("%05d-%s.png", f.Index, v))
			if err := writePNG(path, img); err != nil {
				return err
			}
			files++
		}
	}
	if _, err := fmt.Fprintf(stdout, "wrote %d PNG files for %d frames to %s\n", files, frames, *dir); err != nil {
		return err
	}
	return decodeErr
}

// pngEncoder favours speed: an animation can have thousands of frames.
var pngEncoder = png.Encoder{CompressionLevel: png.BestSpeed}

func writePNG(path string, img image.Image) (err error) {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := file.Close(); err == nil {
			err = cerr
		}
	}()
	w := bufio.NewWriter(file)
	if err := pngEncoder.Encode(w, img); err != nil {
		return err
	}
	return w.Flush()
}
