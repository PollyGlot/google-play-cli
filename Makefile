.PHONY: help build test check lint verb-gate dash-gate install-test format install-hooks tidy clean release-snapshot discovery-update schema-index-update coverage-update stats \
	lint-version fmt-check vet shellcheck required-files build-check test-race worker-test

# Project metadata
BINARY := gplay
PKG    := github.com/PollyGlot/google-play-cli

# The golangci-lint version CI pins, read from the workflow so there is one
# source of truth. `make check` warns when the local binary differs.
GOLANGCI_LINT_VERSION := $(shell sed -n 's/^ *version: *\(v[0-9][0-9.]*\) *$$/\1/p' .github/workflows/ci.yml)

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?##' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "} {printf "  %-18s %s\n", $$1, $$2}'

build: ## Build the gplay binary into ./bin/
	@mkdir -p bin
	go build -o bin/$(BINARY) ./cmd/$(BINARY)

test: ## Run tests (fast, no race detector; `make check` adds -race)
	go test ./...

# The single pre-PR gate: every step of the two required CI checks ("Build,
# lint, test" and "Docs sanity" in .github/workflows/ci.yml), cheapest first so
# a failure surfaces early. Generated-file freshness is asserted by Go tests, so
# `test-race` covers it. Keep this list in step with ci.yml.
check: lint-version fmt-check verb-gate dash-gate shellcheck install-test worker-test required-files vet lint build-check test-race ## Run every required CI check locally (the pre-PR gate)
	@echo "check: OK"

lint-version:
	@v="$$(golangci-lint version 2>/dev/null | sed -n 's/.*version \([0-9][0-9.]*\).*/v\1/p')"; \
	if [ "$$v" != "$(GOLANGCI_LINT_VERSION)" ]; then \
		echo "warning: golangci-lint $${v:-not found}, CI pins $(GOLANGCI_LINT_VERSION)" >&2; \
	fi

fmt-check:
	@files="$$(gofmt -l .)"; \
	if [ -n "$$files" ]; then echo "$$files" >&2; echo 'fmt-check: run "make format"' >&2; exit 1; fi

vet:
	go vet ./...

# Same file list as the "shellcheck install.sh" step of ci.yml.
shellcheck:
	shellcheck install.sh scripts/install-test.sh

required-files:
	@bash scripts/required-files.sh

build-check:
	go build -o /dev/null ./cmd/$(BINARY)

test-race:
	go test -race ./...

lint: dash-gate ## Run golangci-lint, the go.mod tidiness check and the em dash gate
	golangci-lint run ./...
	go mod tidy -diff

verb-gate: ## Fail if a pre-rename verb name (ADR-0019) reappears
	@bash scripts/verb-gate.sh

dash-gate: ## Fail if an em dash reappears in Go source (help text and errors reach users)
	@bash scripts/dash-gate.sh

install-test: ## Exercise install.sh offline (fail-closed sha256 gate)
	@bash scripts/install-test.sh

worker-test: ## Test the gplay.sh Worker's /install route offline (latest release tag, never main)
	node --test deploy/gplay.sh/worker.test.mjs

format: ## Run gofmt + goimports on the whole tree (the formatters lint enforces)
	golangci-lint fmt

tidy: ## Tidy go.mod / go.sum
	go mod tidy

install-hooks: ## Opt in to the pre-push hook that runs `make check` (works in worktrees)
	@bash scripts/install-hooks.sh

discovery-update: ## Regenerate offline Discovery snapshots under docs/discovery/ (network, run on demand)
	go run ./internal/discovery/cmd/discovery-update

schema-index-update: ## Derive the embedded Schema index from the committed Discovery snapshot (offline)
	go run ./internal/discovery/cmd/schema-index-update

coverage-update: ## Render docs/COVERAGE.md from the Discovery index and the API method registry (offline)
	go run ./internal/discovery/cmd/coverage-update

.PHONY: contract-update
contract-update: ## Regenerate cmd/gplay/testdata/surface.golden (every leaf, flag and exit code) from the cobra tree (offline)
	go test ./cmd/gplay -run '^TestSurfaceGolden_isFresh$$' -count=1 -update-contract

release-snapshot: ## Local GoReleaser snapshot (no publish): sanity-check the config
	goreleaser release --snapshot --clean --skip=publish,sign,sbom

clean: ## Remove build artifacts
	rm -rf bin dist

stats: ## Show download stats from GitHub Releases (read-only; needs gh)
	@bash scripts/stats.sh
