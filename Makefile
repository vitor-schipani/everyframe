GIFCORPUS := go run ./internal/tools/gifcorpus

.DEFAULT_GOAL := help

.PHONY: help
help: ## List the available targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  %-17s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: test
test: ## Run the unit tests
	go test ./...

.PHONY: corpus
corpus: ## Download the GIF test corpus and verify it (fetch + verify)
	$(GIFCORPUS) fetch
	$(GIFCORPUS) verify

.PHONY: corpus-refresh
corpus-refresh: ## Re-pick the corpus from Wikimedia Commons, then fetch and verify (maintainers)
	$(GIFCORPUS) discover
	$(GIFCORPUS) fetch
	$(GIFCORPUS) verify

# Deprecated targets. They still work but print a warning, and are hidden
# from `make help`. Use `make corpus` or `make corpus-refresh` instead.

.PHONY: corpus-fetch
corpus-fetch:
	@echo "warning: 'make corpus-fetch' is deprecated; use 'make corpus'" >&2
	$(GIFCORPUS) fetch $(ARGS)

.PHONY: corpus-verify
corpus-verify:
	@echo "warning: 'make corpus-verify' is deprecated; use 'make corpus'" >&2
	$(GIFCORPUS) verify $(ARGS)

.PHONY: corpus-discover
corpus-discover:
	@echo "warning: 'make corpus-discover' is deprecated; use 'make corpus-refresh'" >&2
	$(GIFCORPUS) discover $(ARGS)
