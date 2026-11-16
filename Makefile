GIFCORPUS := go run ./internal/tools/gifcorpus
EVERYFRAME := go run ./cmd/everyframe

.DEFAULT_GOAL := help

.PHONY: help
help: ## List the available targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  %-17s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: test
test: ## Run the unit tests
	go test ./...

.PHONY: build
build: ## Build the everyframe command into bin/everyframe
	go build -o bin/everyframe ./cmd/everyframe

.PHONY: inspect
inspect: require-file ## Describe a GIF: make inspect FILE=path.gif [ARGS=-json]
	@$(EVERYFRAME) inspect $(ARGS) "$(FILE)"

.PHONY: frames
frames: require-file ## Write a GIF's frames as PNGs: make frames FILE=path.gif [OUT=frames]
	@$(EVERYFRAME) frames -o "$(or $(OUT),frames)" $(ARGS) "$(FILE)"

.PHONY: require-file
require-file:
	@test -n "$(FILE)" || { echo "set FILE, for example: make $(MAKECMDGOALS) FILE=path.gif" >&2; exit 2; }

.PHONY: corpus
corpus: ## Download the GIF test corpus and verify it (fetch + verify)
	@$(GIFCORPUS) fetch
	@$(GIFCORPUS) verify

.PHONY: corpus-refresh
corpus-refresh: ## Re-pick the corpus from Wikimedia Commons, then fetch and verify (maintainers)
	@$(GIFCORPUS) discover
	@$(GIFCORPUS) fetch
	@$(GIFCORPUS) verify
