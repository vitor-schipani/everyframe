GIFCORPUS := go run ./internal/tools/gifcorpus
GIFBENCH := go run ./internal/tools/gifbench
EVERYFRAME := go run ./cmd/everyframe

.DEFAULT_GOAL := help

.PHONY: help
help: ## List the available targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  %-24s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: test
test: ## Run the unit tests
	go test ./...

.PHONY: test-extended
test-extended: ## Run the end-to-end tests against the extended corpus too
	EVERYFRAME_CORPUS_EXTENDED=1 go test -timeout 60m ./gif ./features

.PHONY: build
build: ## Build the everyframe command into bin/everyframe
	go build -o bin/everyframe ./cmd/everyframe

.PHONY: inspect
inspect: require-file ## Describe a GIF: make inspect FILE=path.gif [ARGS=-json]
	@$(EVERYFRAME) inspect $(ARGS) "$(FILE)"

.PHONY: frames
frames: require-file ## Write a GIF's frames as PNGs: make frames FILE=path.gif [OUT=frames]
	@$(EVERYFRAME) frames -o "$(or $(OUT),frames)" $(ARGS) "$(FILE)"

.PHONY: features
features: require-file ## Measure a GIF for detection: make features FILE=path.gif [ARGS=-json]
	@$(EVERYFRAME) features $(ARGS) "$(FILE)"

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

.PHONY: corpus-extended
corpus-extended: ## Download the extended GIF corpus, about 1000 more files, and verify it
	@$(GIFCORPUS) fetch -extended
	@$(GIFCORPUS) verify -extended

.PHONY: corpus-extended-refresh
corpus-extended-refresh: ## Re-pick the extended corpus, then fetch and verify (maintainers)
	@$(GIFCORPUS) discover -extended
	@$(GIFCORPUS) fetch -extended
	@$(GIFCORPUS) verify -extended

.PHONY: bench
bench: ## Report time and memory per stage on the corpus: make bench [ARGS="-runs 3"]
	@$(GIFBENCH) $(ARGS)

.PHONY: bench-extended
bench-extended: ## The same report on the extended corpus
	@$(GIFBENCH) -extended $(ARGS)
