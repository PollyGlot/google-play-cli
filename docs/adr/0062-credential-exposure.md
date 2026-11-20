# ADR-0062: Where a credential could leak, and what stops it

## Status

accepted (2026-11-20)

## Context

gplay holds a service account's private key and the OAuth2 tokens minted from
it, and runs in CI, where logs are pasted into issues. A secret can leave the
process through a log line, through output, or through a request to a host
that is not Google's API. These decisions lived in code comments; the comment
purge (the `scripts/lint/comments.py` ratchet) gathers them here.

This ADR is a catalogue, one section per exit route. A new route, or a new
guard on an existing one, gets its entry under the route it concerns.

## Decision

### stderr

- **Redaction is a property of the run context, not of a call site.** The
  binary wraps stderr in main, but tests and any caller assembling the kernel
  by hand reach the same stream, so the kernel wraps it again when it builds
  the run context. The redacting writer is idempotent (the two wrappings do
  not stack) and turns a nil writer into a discard.

### stdout

- **stdout is never redacted.** It mirrors API responses byte for byte
  ([ADR-0003](./0003-json-passthrough.md)), and masking it would corrupt
  payloads a machine parses; the API never returns gplay's own credentials.

### Requests to hosts outside the API

- **A URL the API hands back is fetched without credentials.** A Store
  image's preview `url` is downloaded with a client that carries no token, so
  the Account's token never reaches that host or any host it redirects to.
  That client keeps the authed client's deadline and `--retry`.
- **That client refuses anything but https**, for the first URL and every
  redirect.
- **The https guard sits innermost**, under the retry middleware: the retry
  middleware follows redirects with a client of its own, so a guard wrapped
  around it would never see the hops.

## Consequences

- No log line has to remember to mask: a new stderr write is covered by
  construction.
- A new command that downloads from a URL returned by the API uses the
  credential-free client; using the authed one would send the token to that
  host.
