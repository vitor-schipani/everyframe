# everyframe
Go library and CLI that unpacks animated images frame by frame and extracts detection features, so fraud and abuse can't hide between frames.

## Test corpus

End-to-end tests run against a pinned set of about 100 real, freely licensed GIFs (roughly 250 MB) from Wikimedia Commons. The files are not stored in the repository; `testdata/corpus/manifest.json` lists each one with its URL, size, SHA-1, licence and author.

```sh
make corpus-fetch                     # download into testdata/corpus/files/
make corpus-fetch ARGS="-limit 10"    # a quick partial corpus
make corpus-verify                    # check the local copy, offline
```

`corpus-fetch` skips files that are already present and valid, so it is safe to re-run. Set `EVERYFRAME_CORPUS_DIR` or pass `ARGS="-dir <path>"` to keep the files somewhere else.

Maintainers can regenerate the manifest with `make corpus-discover`. Run `make help` to list all targets; each one wraps `go run ./internal/tools/gifcorpus`.
