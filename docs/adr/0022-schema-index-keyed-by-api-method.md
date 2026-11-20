# Schema index is keyed by the API method surface, not by gplay commands

## Status

accepted

amended (2026-11-20): implementation decisions moved from code comments; the command axis, deferred below, has since shipped as the `--command` lens (see the amendment at the end)

## Context

`gplay schema` (the `[experimental]` introspection command) exposes an
embedded, normalized **Schema index** (see GLOSSARY.md) derived from the Android
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

## Implementation notes

- **The index has two sections.** Methods are keyed by method id; schemas are
  a dictionary keyed by type name. A method points at its request and
  response schema by name and a nested property points by `$ref` name into the
  same dictionary, so the dictionary is the depth mechanism: no duplication,
  no arbitrary truncation.
- **The index package is compiled into the binary and imports only the
  standard library** (no `net/http`, no Google SDK). Embedding inert reference
  data does not contradict [ADR-0007](./0007-raw-http-not-google-go-sdk.md);
  the Discovery fetching and normalising tooling is never compiled in.
- **Every committed snapshot feeds one index.** The leading segment of a
  method id discriminates the service, so the services merge with no
  collision.
- **A method's path is base-relative, and the base is recorded per service.**
  Google prepends a constant prefix to every `flatPath`
  (`androidpublisher/v3/`); stripping it gives the fragment users know from
  the API reference. The prefix is not uniform across Google's documents
  (`gamesConfiguration` uses `games/v1configuration/`, `playdeveloperreporting`
  uses `v1beta1/`), so it is stripped only when every method of a document
  carries it, lest the path mean two things in one document, and the prefix
  actually stripped is stored with the service's root URL. Root URL, base path
  and method path rebuild the absolute URL template byte for byte, which is
  what `apiregistry.Resolve` hands to call sites
  ([ADR-0065](./0065-api-registry-and-discovery-snapshot.md)).

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

## Amendment: the `--command` lens

The cross-link layer the Consequences anticipate exists: `gplay schema
--command "<command>"` lists the methods a gplay command calls and renders
each one as a method-id query would. The index key does not change; the lens
sits on top of it, as foreseen.

- **The lens inverts the API registry, read-only.** Every request a shipped
  command sends resolves through `internal/apiregistry`, whose entries name
  the commands calling each method
  ([ADR-0065](./0065-api-registry-and-discovery-snapshot.md)). The Discovery
  snapshot cannot say which methods a command calls; the registry can, and
  it stays the only command-to-method mapping, so the lens cannot drift from
  what the binary actually calls. No second table is kept.
- **The Edit lifecycle calls are left out on purpose.** The registry
  attributes `edits.insert`, `edits.commit` and `edits.delete` to the `edits`
  commands only. Every Edit-scoped command shares them, and which ones run
  depends on the Edit mode (implicit or explicit) and on whether the command
  mutates, so listing them on each command would be wrong half the time. A
  note on stderr says so when they apply.
- **Finding nothing is not a failure.** A query that matches nothing, and a
  `--command` whose command calls no API method (an offline command such as
  `auth list`, or `schema` itself), print a note on stderr and exit 0 with an
  empty result: a script asking "what is there?" gets an answer, not an
  error.
