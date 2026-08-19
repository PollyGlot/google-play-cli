.PHONY: help build test lint verb-gate dash-gate format install-hooks tidy clean release-snapshot discovery-update schema-index-update stats

# Project metadata
BINARY := gplay
PKG    := github.com/PollyGlot/google-play-cli

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?##' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "} {printf "  %-18s %s\n", $$1, $$2}'

build: ## Build the gplay binary into ./bin/
	@mkdir -p bin
	go build -o bin/$(BINARY) ./cmd/$(BINARY)

test: ## Run tests
	go test ./...

lint: ## Run golangci-lint + the prose gates
	golangci-lint run ./...
	@bash scripts/dash-gate.sh

verb-gate: ## Fail if a pre-rename verb name (ADR-0019) reappears
	@bash scripts/verb-gate.sh

dash-gate: ## Fail if an em dash reappears in Go source (help text and errors reach users)
	@bash scripts/dash-gate.sh

format: ## Run gofmt + goimports on the whole tree
	gofmt -w .

tidy: ## Tidy go.mod / go.sum
	go mod tidy

install-hooks: ## Install local pre-commit hook
	@mkdir -p .git/hooks
	@cp -f .githooks/pre-commit .git/hooks/pre-commit 2>/dev/null || true
	@chmod +x .git/hooks/pre-commit 2>/dev/null || true
	@echo "hooks installed (no-op if .githooks/pre-commit does not exist yet)"

discovery-update: ## Regenerate offline Discovery snapshots under docs/discovery/ (network, run on demand)
	go run ./internal/discovery/cmd/discovery-update

schema-index-update: ## Derive the embedded Schema index from the committed Discovery snapshot (offline)
	go run ./internal/discovery/cmd/schema-index-update

release-snapshot: ## Local GoReleaser snapshot (no publish) — sanity-check the config
	goreleaser release --snapshot --clean --skip=publish,sign,sbom

clean: ## Remove build artifacts
	rm -rf bin dist

stats: ## Show download stats from GitHub Releases (read-only; needs gh)
	@bash scripts/stats.sh
