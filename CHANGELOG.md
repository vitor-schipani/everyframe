# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
