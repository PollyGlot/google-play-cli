# Release image for gplay, built by GoReleaser (`dockers_v2` in
# .goreleaser.yaml), never by hand: GoReleaser hands this file a context that
# already holds the cross-compiled static binaries, one per platform under
# $TARGETPLATFORM/, so there is no build stage and nothing compiles here.
#
# distroless/static: CA certificates and tzdata (gplay talks TLS to Google),
# no shell, no package manager. The `nonroot` tag runs as uid 65532.
# The tag is not pinned by digest on purpose: each release picks up the
# current CA bundle; the published image itself is pinned, signed and attested.
FROM gcr.io/distroless/static-debian13:nonroot
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/gplay /usr/bin/gplay
ENTRYPOINT ["/usr/bin/gplay"]
