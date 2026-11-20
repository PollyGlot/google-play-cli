# Speak the Google Play Developer API over raw HTTP, not via the official Go SDK

## Status

accepted; amended (2026-11-20): implementation decisions moved from code comments

## Decision

gplay calls the Google Play Developer API directly over HTTP from `internal/play/api/` rather than depending on `google.golang.org/api/androidpublisher/v3` (the official auto-generated Go client).

This is the inverse of the "use Google's SDK when possible" default, so it deserves an explicit record.

## Why

1. **Binary size and surface.** The auto-generated SDK ships type wrappers for every `androidpublisher/v3` endpoint, including ones gplay will never call (closed-track creation, listings images at every density, etc.). Linking it pulls megabytes of unused code into the static binary, against the "one small binary" pillar of the project.

2. **JSON pass-through is trivial with raw HTTP.** [ADR-0003](0003-json-passthrough.md) commits gplay to surfacing the API response verbatim under `--output json`. With raw HTTP we can write the response body straight to stdout. With the SDK we would marshal into generated structs, then re-marshal back to JSON — a round trip that risks losing or renaming fields and that exists only to satisfy the SDK's type system.

3. **Cold start.** Generated SDKs typically initialize service registries and discovery documents on import. gplay's CLI cold start is in the critical path for every CI invocation; we keep imports minimal on principle.

4. **API stability.** The `androidpublisher/v3` REST surface is publicly documented and changes slowly. The SDK's release cadence is a separate moving target governed by `googleapis/google-api-go-client`'s codegen pipeline. Pinning to the REST contract directly removes a dependency vector.

5. **Idiomatic Go in gplay's own style.** The generated SDK is not idiomatic Go — it is generated Go, which is verbose and shaped by the codegen tool's constraints. Hand-rolled HTTP keeps `internal/play/api/` consistent with the rest of the codebase.

## What we lose

- **No auto-update when Google adds endpoints.** We have to write each new call ourselves. Acceptable: gplay's surface is small and stable (auth, releases, apps, tracks, reviews — see [ROADMAP.md](../ROADMAP.md)), and the cost of adding a new call is a small structured PR, not a foundational rewrite.
- **No type safety from the SDK.** We maintain our own request/response types in `internal/play/api/`, scoped to what gplay actually sends and receives. This is more code than `import google.golang.org/api/androidpublisher/v3`, but it stays small and reviewable.

## What about auth?

Auth is the one place where we **do** lean on Google's libraries: `golang.org/x/oauth2/google` for the service account → JWT → access token exchange. That library is small, focused, and has no equivalent we'd want to hand-roll. The "raw HTTP not SDK" decision is specifically about the API surface (`androidpublisher/v3`), not about every Google-authored Go package.

Update (#646): gplay now imports only `golang.org/x/oauth2/jwt`, building its config from the already-parsed key file. `golang.org/x/oauth2/google` served a single call (`JWTConfigFromJSON`) and linked the GCE metadata client and `log/slog` into the binary for a server a key file never talks to. The JWT itself (claims, `kid`, token URI) is unchanged, pinned by a test in `internal/auth/token`.

## Considered Options

- **Use the official SDK (`google.golang.org/api/androidpublisher/v3`)** — rejected for the reasons above. Best fit for backend services in Go that need broad API coverage and type safety; wrong fit for a small CLI committed to JSON pass-through and minimal binary size.
- **Generate our own typed client from Google's OpenAPI / Discovery doc** — rejected for now. Would solve the type-safety regret but reintroduces the binary-bloat and cold-start concerns, plus a codegen pipeline gplay would have to maintain. Worth reconsidering if `internal/play/api/` grows beyond ~30 endpoints.
- **Use a third-party Go wrapper around `androidpublisher/v3`** — rejected. No mature option exists, and we would inherit someone else's roadmap.

## How this shows up to users

It does not. End users (CI engineers, devs invoking `gplay` from a script, AI agents) never see `internal/play/api/`. This is an internal architecture decision. It supports the user-facing pillars in [POSITIONING.md](../marketing/POSITIONING.md) — particularly the "one small binary" and "JSON pass-through" claims — but it is not itself a feature to market.

## Implementation notes

- **One executor.** Every `internal/play/*` module sends through
  `api.Do` (or `api.DoJSON`): a module declares its methods once from the
  registry and describes each call with an `api.Call` (path params, query,
  body, target). The verb and URL come from the Discovery-derived registry,
  never from a literal ([#513](https://github.com/PollyGlot/google-play-cli/issues/513)).
  The executor turns a non-2xx into an `*api.Error` carrying the status, the
  message and Google's `errors[].reason` values, and enforces the body cap
  ([ADR-0057](./0057-bounded-responses-and-pagination.md)).
- **The executor does not decide whether a request may be replayed.** That is
  the method's `Idempotent` bit in the registry, read by the `--retry`
  transport from the request itself, so a module gets replay right by
  construction (DESIGN §8, Opt-in retry).
- **A body of bytes is sent as is.** `[]byte` and `json.RawMessage` are never
  re-encoded, so a catalog file reaches the API byte for byte
  ([ADR-0041](./0041-declarative-monetization-catalog.md)); any other value is
  JSON-encoded; a stream is reopened on each attempt.
- **A call states what it addresses**: the package, or a typed resource on
  another axis, so the error envelope never labels a developer account or a
  bucket as a package ([ADR-0023](./0023-json-error-envelope.md)).
- **An absolute URL is requested only when an API response handed it back**
  (the Store image `url` of `images.list`, say). Such a call bypasses the
  registry template, so it must name its operation itself; a URL built by
  hand is forbidden (the archgate test).
- **Two siblings cover what `Do` refuses**: `api.Download` streams a media
  payload with no cap, and `api.Paginate` walks a continuation token
  (ADR-0057). Resumable uploads go through `api.ResumableUpload`
  ([ADR-0056](./0056-media-upload-protocol.md)).
