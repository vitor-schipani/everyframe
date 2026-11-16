# everyframe
Go library and CLI that unpacks animated images frame by frame and extracts detection features, so fraud and abuse can't hide between frames.

## Decoding GIFs

Package `gif` reads an animation one frame at a time, so memory use does not grow with the number of frames. Each frame is available as stored (its rectangle of palette indices, palette, delay and disposal method) and, through a `Canvas`, as the picture a viewer would show.

```go
import "github.com/vitor-schipani/everyframe/gif"

d, err := gif.NewDecoder(r, gif.Options{})
if err != nil {
	return err
}
canvas := gif.NewCanvas(d.Header())
for frame, err := range d.Frames() {
	if err != nil {
		return err
	}
	shown := canvas.Draw(frame) // *image.RGBA, reused on the next call
	// inspect frame and shown
}
meta := d.Metadata() // loop count, comments, extensions, trailing data
```

The decoder accepts the irregular files that browsers render and reports each irregularity instead of rejecting the file: frames that reach outside the canvas, missing or surplus pixel data, palette indices with no colour, a missing trailer, and data appended after the end of the GIF. A file cut short still yields every frame up to the cut, followed by an error wrapping `io.ErrUnexpectedEOF`.

Input is treated as hostile. `Options.Limits` caps the canvas and frame area, the number of frames, the total pixels decoded and the metadata kept; the defaults (64 megapixels, 10,000 frames, 4 GiB, 1 MiB) suit ordinary files, and exceeding one returns a `*gif.LimitError`.

## Test corpus

End-to-end tests run against a pinned set of about 100 real, freely licensed GIFs (roughly 250 MB) from Wikimedia Commons. The files are not stored in the repository; `testdata/corpus/manifest.json` lists each one with its URL, size, SHA-1, licence and author.

```sh
make corpus    # download into testdata/corpus/files/ and verify
```

`make corpus` skips files that are already present and valid, so it is safe to re-run. Set `EVERYFRAME_CORPUS_DIR` to keep the files somewhere else.

Once the corpus is present, `go test ./...` decodes every file in it and compares the result with the standard library's decoder. Without the corpus, or with `-short`, that test is skipped.

Maintainers can re-pick the files with `make corpus-refresh`, which rewrites the manifest from Wikimedia Commons and then fetches and verifies the new selection. Commit the manifest change so everyone tests against the same files.

Both targets wrap `go run ./internal/tools/gifcorpus`, which can also be run directly for a single step or extra flags:

```sh
go run ./internal/tools/gifcorpus fetch -limit 10   # a quick partial corpus
go run ./internal/tools/gifcorpus verify            # check the local copy, offline
go run ./internal/tools/gifcorpus help              # all commands
```
