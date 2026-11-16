# everyframe
Go library and CLI that unpacks animated images frame by frame and extracts detection features, so fraud and abuse can't hide between frames.

## Test corpus

End-to-end tests run against a pinned set of about 100 real, freely licensed GIFs (roughly 250 MB) from Wikimedia Commons. The files are not stored in the repository; `testdata/corpus/manifest.json` lists each one with its URL, size, SHA-1, licence and author.

```sh
make corpus    # download into testdata/corpus/files/ and verify
```

`make corpus` skips files that are already present and valid, so it is safe to re-run. Set `EVERYFRAME_CORPUS_DIR` to keep the files somewhere else.

Maintainers can re-pick the files with `make corpus-refresh`, which rewrites the manifest from Wikimedia Commons and then fetches and verifies the new selection. Commit the manifest change so everyone tests against the same files.

Both targets wrap `go run ./internal/tools/gifcorpus`, which can also be run directly for a single step or extra flags:

```sh
go run ./internal/tools/gifcorpus fetch -limit 10   # a quick partial corpus
go run ./internal/tools/gifcorpus verify            # check the local copy, offline
go run ./internal/tools/gifcorpus help              # all commands
```
