.PHONY: help build test lint verb-gate format install-hooks tidy clean release-snapshot

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

lint: ## Run golangci-lint
	golangci-lint run ./...

verb-gate: ## Fail if a pre-rename verb name (ADR-0019) reappears
	@bash scripts/verb-gate.sh

format: ## Run gofmt + goimports on the whole tree
	gofmt -w .

tidy: ## Tidy go.mod / go.sum
	go mod tidy

install-hooks: ## Install local pre-commit hook
	@mkdir -p .git/hooks
	@cp -f .githooks/pre-commit .git/hooks/pre-commit 2>/dev/null || true
	@chmod +x .git/hooks/pre-commit 2>/dev/null || true
	@echo "hooks installed (no-op if .githooks/pre-commit does not exist yet)"

release-snapshot: ## Local GoReleaser snapshot (no publish) — sanity-check the config
	goreleaser release --snapshot --clean --skip=publish,sign,sbom

clean: ## Remove build artifacts
	rm -rf bin dist
