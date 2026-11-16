GIFCORPUS := go run ./internal/tools/gifcorpus

.DEFAULT_GOAL := help

.PHONY: help
help: ## List the available targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  %-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: test
test: ## Run the unit tests
	go test ./...

.PHONY: corpus-fetch
corpus-fetch: ## Download the GIF test corpus (ARGS="-limit 10" for a partial one)
	$(GIFCORPUS) fetch $(ARGS)

.PHONY: corpus-verify
corpus-verify: ## Check the downloaded corpus against the manifest, offline
	$(GIFCORPUS) verify $(ARGS)

.PHONY: corpus-discover
corpus-discover: ## Regenerate the corpus manifest from Wikimedia Commons (maintainers)
	$(GIFCORPUS) discover $(ARGS)
