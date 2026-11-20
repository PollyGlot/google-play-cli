# ADR-0065: API registry and Discovery snapshot

## Status

accepted (2026-11-20)

## Context

gplay hand-rolls every API call (ADR-0007), so nothing in the code answered
"which API methods does gplay depend on?". A Discovery refresh could remove
or deprecate a method under a shipped command and CI stayed green, and
`docs/COVERAGE.md`, maintained by hand, claimed methods the CLI never called.
`internal/apiregistry`, the committed Discovery snapshots under
`docs/discovery/` and the tests between them close both gaps. The Schema
index they build on is [ADR-0022](./0022-schema-index-keyed-by-api-method.md);
the coverage states are [ADR-0026](./0026-maximal-admin-api-coverage.md) and
[ADR-0047](./0047-coverage-dispositions-redundant-and-parked.md). These
decisions lived in code comments; the comment purge (the
`scripts/lint/comments.py` ratchet) gathers them here.

## Decision

### The registry

- **One declarative line per method a shipped command calls**, mapped to that
  command. Adding a command that calls a new method means adding its line.
- **Every entry is anchored to the offline Discovery artefacts** (the
  embedded Schema index, `paths.txt` and the snapshots). A refresh that
  removes or deprecates a registered method fails `go test ./...`, naming the
  method id and the command that breaks. That failure is the deterministic
  evidence the Discovery triage routine cites before any model reads the diff.
- **The registry also resolves the request.** `apiregistry.Resolve` turns a
  method id into the verb and URL template, so the registry is complete by
  construction rather than by good faith: a method that is called is
  necessarily registered.
- **The per-method facts the transport needs live next to the verb and URL
  they derive from**: whether a replay is safe
  ([ADR-0058](./0058-api-failure-classification.md)) and whether a response
  is one page of a listing
  ([ADR-0057](./0057-bounded-responses-and-pagination.md)).

### The completeness gate

- **No hand-written API base URL or resource path under `internal/play/` or
  `commands/`.** A test parses every shipped Go file there and fails on a
  string literal that looks like one, naming file and line. A path written by
  hand is a method that ships without a registry line, which COVERAGE.md
  would then omit.
- **Only code that builds real requests is scanned.** `_test.go` files and
  test-support packages (a package name ending in `test`) build fixtures, and
  a fixture URL is data, not a call. Import paths are skipped: they are
  string literals that happen to contain segments like `/games/`.
- **The gate lives in `internal/apiregistry`**, next to the invariant it
  protects; a package of its own would hold one test file and no code. The
  broader copy gates generalise it in `internal/ratchet`
  ([ADR-0064](./0064-architecture-ratchets.md)).
- **`internal/play/gcs` is the one exception.** `reviews history` reads the
  Cloud Storage JSON API, which has no snapshot under `docs/discovery/`, so
  there is nothing for `Resolve` to derive a URL from and nothing to anchor an
  entry to. That package keeps its own base URL. A second exception would mean
  the API it calls has a Discovery snapshot, and therefore belongs in the
  registry instead.

### Testing the resolver against Google

- **The resolver test reads the raw `flatPath` of the committed snapshots**,
  not the derived Schema index the resolver itself reads: deriving both sides
  from the same file would prove nothing.
- **It walks the Discovery resource tree as raw JSON**, not through a typed
  mirror, so it stays independent of `internal/schemaindex`'s own parsing.

### Rendering COVERAGE.md

- **COVERAGE.md is rendered, never edited** (`make coverage-update`): the
  universe of methods is `paths.txt`, the called set is the registry, and the
  dispositions of ADR-0047 sit in between. Everything else is uncovered by
  subtraction, so no row can claim more than the code does.
- **The render is deterministic**: the same inputs give byte-identical
  output, so a freshness test re-renders and compares bytes, as the Schema
  index gate does.
- **The generator refuses an inconsistent input** rather than render it: an
  id in any list that `paths.txt` does not know would silently vanish, and a
  method with two dispositions would render as whichever case wins. The
  registry tests catch both too; the generator refuses on its own so it can
  never write a quietly incomplete document.
- **Method ids are sorted by the generator**, although `paths.txt` is already
  sorted, so the rendering does not depend on how the producer orders it.
- **A note column joins its parts with a semicolon**, not parentheses: several
  notes already carry a parenthesised ADR reference, and nesting reads badly.
- **An excluded API without a snapshot has no rows.** The Play Integrity API
  is excluded by nature (ADR-0026) but is its own API with no snapshot, so
  there is no method id to list.

### Anchoring Go lists to Discovery enums

A snapshot refresh is diffed against the previous snapshot, never against the
code. Google had removed `promoGraphic` from `AppImageType` before the first
committed snapshot, yet a hand-written table in `internal/play/images` kept
it and every `metadata images` command hit Play's 400 (#758).
`internal/enumanchor` closes that gap; it holds no production code.

- **Every Go list copied from a Discovery enum is pinned to the enum** of the
  embedded Schema index: a path segment, a value the CLI sends, a state it
  switches on. A refresh that adds, renames or removes a value fails CI.
- **A row is exact or a subset.** Let E be the enum minus its "unspecified"
  sentinel. With no unhandled values, the code must equal E (path segments,
  flag choices). With unhandled values, the code plus the unhandled keys must
  equal E, each unhandled value carrying its reason: leaving a value out is a
  recorded choice, and a new upstream value still fails.
- **To anchor a list**, make its values reachable from outside its package
  (exported constants, a variable or a function) and add one row naming the
  enum's source (a schema property, or a parameter of one or more methods).
- **A second test keeps the table complete**: any string literal in shipped
  code that equals a Discovery enum value must belong to an anchored row, or
  be listed as coincidental with the reason no row is needed. A list copied
  later fails until it is anchored.
- **Only scalar properties and method parameters carry an enum in the
  index**, never array items. Array-item enums such as the team permissions
  are anchored next to their vocabulary, in `internal/team/vocab`.

### The snapshot format

- **A snapshot is normalised for review**: object keys sorted, so each regen
  yields a minimal diff; the transport-level top-level `etag` dropped as noise;
  numbers kept verbatim (`json.Number`), so no float is reformatted; HTML
  escaping off, so descriptions stay readable.
- **Normalisation is idempotent**, which is exactly what the offline integrity
  gate asserts against the committed file: a hand edit or a stale file fails.
- **The refresh tool prints each snapshot's `revision` on stdout**, to be cited
  in the regen commit (`docs/discovery/README.md`).

## Consequences

- A method gplay calls cannot be missing from COVERAGE.md, and a method
  Google removes cannot disappear from under a command without a red test
  naming both.
- Calling an API that has no Discovery snapshot is a decision: it means an
  exception to the completeness gate, argued in review, as Cloud Storage is.
- A new enum-shaped list in the code fails until it is anchored or declared
  coincidental.
