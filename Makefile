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

.PHONY: corpus-fetch
corpus-fetch: ## Download the GIF test corpus (ARGS="-limit 10" for a partial one)
	$(GIFCORPUS) fetch $(ARGS)

.PHONY: corpus-verify
corpus-verify: ## Check the downloaded corpus against the manifest, offline
	$(GIFCORPUS) verify $(ARGS)

.PHONY: corpus-discover
corpus-discover: ## Regenerate the corpus manifest from Wikimedia Commons (maintainers)
	$(GIFCORPUS) discover $(ARGS)
