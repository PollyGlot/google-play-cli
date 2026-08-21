# Schema index is keyed by the API method surface, not by gplay commands

## Status

accepted

## Context

`gplay schema` (the `[experimental]` introspection command) exposes an
embedded, normalized **Schema index** (see CONTEXT.md) derived from the Android
Publisher v3 **Discovery snapshot** (#52). Every index entry needs a primary
key — the thing a caller queries by and the canonical address shown back.
Three axes were on the table:

- **HTTP `METHOD path`** (`GET /…/tracks/{track}`) — the key an
  OpenAPI-sourced index is forced into, OpenAPI being path-keyed.
- **RPC method id** (`androidpublisher.edits.tracks.update`) — Google's native
  dotted id, present verbatim in the Discovery doc as `method.id`.
- **gplay command** (`tracks set`, `metadata apply`) — the thing users actually
  run, and on its face the most agent-useful ("what fields can I send to
  `tracks set`?").

## Decision

The Schema index is keyed by the **RPC method id**; `gplay schema` introspects
the **API method surface**. Keying by gplay command is explicitly **out of
scope** and deferred to the backlog as a possible future cross-link layer.

Rationale:

- **Native, zero-synthesis key.** Google hands us `method.id` directly; an
  OpenAPI source gives none, forcing dot-notation to be *manufactured* from
  REST paths.
  Keying off the native id also yields the service discriminator for free (the
  id's leading segment — `androidpublisher.*` vs a future
  `playdeveloperreporting.*`), so the index is multi-service-ready (vitals,
  #49) with no schema change.
- **One vocabulary with the Discovery snapshot.** #52's snapshot and its
  `paths.txt` are already keyed by method id. Keying the index the same way
  makes `gplay schema` honestly "the snapshot, made queryable in-binary," with
  no impedance mismatch.
- **The gplay-command axis is a different, non-derivable product.** The
  command→API mapping lives in gplay's own code, not the Discovery doc, and is
  lossy/N:1 (`metadata apply` = N `listings.patch`; `apps view` =
  `details.get` + `listings.get`). Building and keeping that map fresh is its
  own feature with its own drift surface. The "what can I send to `tracks set`"
  need is already served by `gplay tracks set --help` + the companion skills;
  `gplay schema` fills the orthogonal gap — the underlying **API** shape that
  `--help` never shows.

## Consequences

- The query surface keys dot-notation off the native `method.id` — no
  `pathToDotNotation` synthesis and no `post:` prefix disambiguation, which a
  path-keyed index needs (Google's ids already encode the action and are
  self-disambiguating).
- Switching to gplay-command keying later would be a near-total rewrite
  (different index, generator, and mapping source). The `[experimental]` label
  keeps that option open without promising the current shape.
- A future "what API calls does `gplay <cmd>` make" surface, if ever built,
  layers *on top* of this index by cross-linking commands to method ids — it
  does not replace the key.

## Considered options

- **HTTP `METHOD path`.** Rejected: a synthesized key that discards Google's
  free native id for a worse one. Path is demoted to a query projection, not
  the identity.
- **gplay command.** Rejected as the *primary* key for the derivability and
  scope reasons above; preserved as a backlog cross-link layer.
