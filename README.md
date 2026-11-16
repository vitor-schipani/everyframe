# everyframe
Go library and CLI that unpacks animated images frame by frame and extracts detection features, so fraud and abuse can't hide between frames.

## Command line

The `everyframe` command looks inside a GIF without writing any code. Run it from a clone with `go run ./cmd/everyframe`, or build it with `go build ./cmd/everyframe`.

```sh
everyframe inspect file.gif            # header, metadata, and a table of every frame
everyframe inspect -json file.gif      # the same report as JSON
everyframe frames -o out/ file.gif     # every frame as PNG files
everyframe features file.gif           # detection features of the file and of every frame
everyframe features -json file.gif     # the same report as JSON
```

`inspect` lists each frame's offset, size, position, delay, disposal and palette, and flags anything irregular: `out-of-canvas`, `missing-pixels`, `surplus-data`, `no-palette`, `index-out-of-range`, `truncated`. It also reports comments, application extensions, a missing trailer, and data appended after the end of the GIF.

`frames` writes two pictures per frame: `00000-stored.png`, the frame's own rectangle as it is in the file, and `00000-shown.png`, the whole canvas as a viewer displays it at that point. `-view stored` or `-view shown` writes only one of them.

`features` prints the measurements described under [Detection features](#detection-features): a summary of the animation, then one row per frame.

From a clone, four make targets wrap the command:

```sh
make build                              # build bin/everyframe
make inspect FILE=file.gif              # add ARGS=-json for JSON
make frames FILE=file.gif OUT=out/      # OUT defaults to frames/
make features FILE=file.gif             # add ARGS=-json for JSON
```

All three commands read standard input when the file is `-`, and take `-max-pixels`, `-max-frames` and `-max-decoded-bytes` to adjust the decoding limits. They exit with 0 on success, 1 when the file could not be fully decoded (what was decoded is still reported or written), and 2 for a mistake in the command line.

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

## Detection features

Package `features` turns the frames into numbers a detection system can act on. It reports measurements, not verdicts: which values are suspicious is policy, and stays with the caller.

```go
import "github.com/vitor-schipani/everyframe/features"

d, err := gif.NewDecoder(r, gif.Options{})
if err != nil {
	return err
}
report, err := features.Extract(d) // report.Summary and report.Frames
```

For each frame:

| Feature | What it tells |
| --- | --- |
| `Delay` | How long the frame is shown. |
| `StoredPixels`, `OutsidePixels`, `TransparentPixels`, `DrawnPixels` | How much the frame stores, and how much of that lies outside the canvas, is transparent, or was actually painted. |
| `Colours` | How many palette indices the frame uses. |
| `ChangedPixels`, `Change` | How many pixels of the picture differ from the previous frame's, and by how much, from 0 (identical) to 1. A frame unlike its neighbours stands out here. |
| `Hash` | A 64-bit perceptual hash of the picture shown. Pictures that look alike differ in few bits, so it can be matched against hashes of known content with `Hash.Distance`. |
| `HashDistance`, `HashDistanceFirst` | The distance from the previous frame's hash, and from the first frame's, which is the picture a thumbnail shows. |

For the animation, `Summary` adds the total duration and the shortest and longest delays, the loop count, the number of distinct hashes, the frame with the largest change and the one furthest from the first, pixel totals, counts of irregular frames, and the size of comments, extensions and data appended after the GIF.

`Extract` returns the report on the frames it could read together with the error when a file is cut short. To handle frames as they are decoded, use an `Extractor`: `Add` measures one frame and `Shown` returns the picture displayed for it. The time spent on a frame depends on the size of the frame, not of the canvas; memory is eight bytes per canvas pixel, twelve once a frame restores the previous picture.

## Test corpus

End-to-end tests run against a pinned set of about 100 real, freely licensed GIFs (roughly 250 MB) from Wikimedia Commons. The files are not stored in the repository; `testdata/corpus/manifest.json` lists each one with its URL, size, SHA-1, licence and author.

```sh
make corpus    # download into testdata/corpus/files/ and verify
```

`make corpus` skips files that are already present and valid, so it is safe to re-run. Set `EVERYFRAME_CORPUS_DIR` to keep the files somewhere else.

Once the corpus is present, `go test ./...` decodes every file in it, compares the result with the standard library's decoder, and extracts the features of every frame. Without the corpus, or with `-short`, those tests are skipped.

Maintainers can re-pick the files with `make corpus-refresh`, which rewrites the manifest from Wikimedia Commons and then fetches and verifies the new selection. Commit the manifest change so everyone tests against the same files.

Both targets wrap `go run ./internal/tools/gifcorpus`, which can also be run directly for a single step or extra flags:

```sh
go run ./internal/tools/gifcorpus fetch -limit 10   # a quick partial corpus
go run ./internal/tools/gifcorpus verify            # check the local copy, offline
go run ./internal/tools/gifcorpus help              # all commands
```
