# Build image for Anthropic's OSS Scanner (github.com/anthropics/oss-scanner).
# The scanner builds this file online with the checkout as the context, then
# scans the finished image with no network: everything the build, the tests
# and the fuzz targets need is fetched here. Not the release image (that is
# the Dockerfile at the root, built by GoReleaser).
FROM golang:1.27-bookworm

ENV DEBIAN_FRONTEND=noninteractive \
    GOTOOLCHAIN=local \
    GOFLAGS=-mod=readonly \
    CGO_ENABLED=1

# Node 22, as in CI, runs the gplay.sh Worker tests (deploy/gplay.sh): the
# Debian package is Node 18, too old to load worker.js as ESM. gcc comes with
# the base image and backs `go test -race`.
COPY --from=node:22-bookworm-slim /usr/local/bin/node /usr/local/bin/node

# Delve, to step through a reproducer offline.
RUN go install github.com/go-delve/delve/cmd/dlv@latest

# scanner contract: the checkout lives inside the image, at /src
COPY . /src
WORKDIR /src

# Every module the build and the tests import, so the offline scan never
# reaches proxy.golang.org.
RUN go mod download all && go mod verify

# The binary with full debug info (no -s -w, no inlining), on PATH as `gplay`.
RUN go build -gcflags=all="-N -l" -o /usr/local/bin/gplay ./cmd/gplay \
    && gplay version

# Warm the build and test caches, race build included. The tests are offline
# by design (internal/testkit fakes the Play transport). A failing test does
# not fail the image.
RUN go vet ./... \
    && (go test -race -count=1 ./... || echo "WARNING: go test reported failures") \
    && (node --test deploy/gplay.sh/worker.test.mjs deploy/gplay.sh/install.test.mjs \
        || echo "WARNING: worker tests reported failures")
