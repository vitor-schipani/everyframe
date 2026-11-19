# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Extended GIF corpus: an opt-in second tier of about 1000 more files from 17 Wikimedia Commons categories, including single-frame and malformed GIFs, with its own manifest. `make corpus-extended` downloads it and `make test-extended` runs the end-to-end tests against it.
- `gifcorpus` takes `-extended` on every command, and `discover` can sample several categories (`-category`, repeatable), accept GIFs that are not animated (`-still`) and leave out the files of another manifest (`-exclude`).
- `Extractor.OnStage` in package `features`, which reports the end of each stage of `Add` so that the cost of each feature can be measured.
- `gifbench` tool (`make bench`, `make bench-extended`) that reports the time and memory spent on a corpus or on given files, end to end and per stage, as text or JSON.

### Changed

- The corpus manifest lists its source categories under `categories` instead of a single `category`.

## [0.3.0] - 2026-11-19

### Added

- Package `features`: detection features for each frame (delay, stored, hidden and drawn pixels, change from the previous picture, and a perceptual hash with its distance from the previous and the first frame) and for the animation as a whole, including container signals such as extension sizes and data after the end of the GIF.
- `everyframe features` command, which prints those measurements as text or JSON, and a `make features` target.

## [0.2.0] - 2026-11-16

### Added

- Package `gif`: a streaming GIF decoder that yields one frame at a time, as stored and (through `Canvas`) as displayed, together with the container metadata: loop count, comments, application and unknown extensions, and data after the end of the GIF.
- Tolerance for the irregular GIFs that browsers render, with each irregularity reported on the frame or in the metadata, and delivery of the partial last frame of a truncated file.
- Resource limits (`gif.Limits`) on canvas and frame area, frame count, decoded pixels and retained metadata, enforced before allocation.
- `everyframe` command with `inspect`, which describes a GIF's header, frames, metadata and irregularities as text or JSON, and `frames`, which writes every frame as PNG files, both as stored and as displayed.
- End-to-end test that decodes the whole GIF corpus and cross-checks it against `image/gif`, plus a fuzz test for the decoder.

## [0.1.0] - 2026-11-16

### Added

- `gifcorpus` tool (`make corpus`, and `make corpus-refresh` for maintainers) that downloads and verifies a pinned corpus of about 100 freely licensed GIFs from Wikimedia Commons for end-to-end tests, plus the manifest listing them.
