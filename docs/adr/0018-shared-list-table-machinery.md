# Shared list-command table machinery (generic columns + exit.UsageError)

## Status

accepted

## Context

By the time `gplay reviews list` (#92) shipped, four read-only commands
carried a near-identical block of column plumbing:

- `commands/reviews/list`, `commands/tracks/list`, `commands/releases/list`,
  and `commands/tracks/status`.

Each one declared, verbatim, its own:

- `columnDef` struct + `columnRegistry` map (header + cell extractor per
  key),
- `(Payload).headers()` / `(Payload).row()`,
- `renderTable` (a `text/tabwriter` block with the same `0,2,2,' ',0`
  config) and `renderMarkdown` (a loop into `output.MarkdownTable`),
- `resolveColumns` (`--columns` → validated, ordered keys),
- a private `usageError` (exit 2) for the unknown-column and missing-flag
  cases,
- and a per-command `TestDefaultColumns_matchRegistryExactly` test guarding
  that `DefaultColumns` and `columnRegistry` had not drifted apart.

That is ~120–150 lines copied per command (issue #93). A cross-cutting fix —
a table-alignment tweak, a new `--columns` behaviour, a Markdown escaping
change — had to be applied in four places and would drift independently. The
summary-alignment fix in #92 was exactly the kind of change that should have
been one edit, not N.

The auth/HTTP half of the same duplication had already been consolidated:
`baseHTTP` (the `oauth2.HTTPClient` ctx seam) and `authError` (exit 10) now
live once in `internal/kernel` behind `RunContext.AuthedClient()`. What
remained was the *column-selection + table/markdown rendering* layer and the
exit-2 `usageError`.

This is distinct from [ADR-0005](./0005-tty-aware-output.md) (Format
selection / the `Renderers{Table,JSON,Markdown}` dispatch) and from #38 (the
payload-driven Renderer interface): those are about *which* renderer runs;
this is about the column machinery layered on top of it.

## Decision

1. **A generic column helper in `internal/output`** (Go generics, the module
   is on Go 1.25):
   - `Column[T]{Key, Header string; Value func(T) string}` — one selectable
     column.
   - `ColumnSet[T]` built by `NewColumnSet(cols...)`. Its **declaration
     order is both the registry and the default order**, so the
     `DefaultColumns ↔ registry` invariant is now *structural* — a single
     list cannot drift from itself — rather than re-tested per command.
   - `ColumnSet.Resolve(spec) ([]Column[T], error)` parses `--columns`
     (empty → all in order; unknown/empty selection → exit 2).
   - `RenderTable[T]` / `RenderMarkdown[T]` render a resolved `[]Column[T]`
     over `[]T`, preserving the exact byte layout the hand-rolled blocks
     produced.

2. **A shared `exit.UsageError` (exit 2)** in `internal/exit`, the package
   that already owns the DESIGN §9 exit-code contract. The verbatim
   per-command `usageError` is promoted here so "CLI misuse → 2" lives in
   one place; `Usagef` is the printf-style constructor for the common
   dynamic-message call sites.

3. **The four carriers migrate** to the helper, each dropping its local
   column block and `usageError`, and keeping only genuinely
   command-specific pieces: `reviews` keeps its `{"reviews":[…]}` JSON
   envelope and `summary` truncation; `releases list` keeps its
   `(no releases on track …)` empty-state line; `tracks status` keeps its
   `!HALTED` marker and `Track: … (kind)` context header; the JSON
   pass-through paths (ADR-0003) are untouched. A small exported
   `ResolveColumns` per command lets render-focused tests build a `Payload`
   from the one registry instead of a bare `[]string`.

## Consequences

- A column/render fix is now **one edit** in `internal/output`, covering all
  four commands.
- `Payload.Columns` is `[]output.Column[T]`, not `[]string`. The old
  "unknown key in a directly-constructed Payload renders an empty cell"
  guard (and its per-command not-a-panic tests) is **gone by construction**:
  a Payload can only hold resolved columns with a non-nil `Value`. The
  exit-2 unknown-column path is tested once in `internal/output` and via
  each command's `--columns` tests.
- No user-facing change: identical table/markdown bytes, identical flags,
  identical exit codes (the existing kernel-level e2e suites for all four
  commands pass unchanged). This was a non-goal to violate.
- `internal/output` now imports `internal/exit` (a leaf package — no cycle).
- **Scope:** only the four commands that actually carried the column
  machinery migrate. `exit.UsageError` is the new single home for the exit-2
  contract; the ~20 other commands still keep a local `usageError` and can
  adopt `exit.UsageError` incrementally as they are touched — a mechanical,
  behaviour-preserving change, not a prerequisite for this one.

## Considered options

- **Keep `DefaultColumns` as a separate `[]string` and centralise only the
  invariant test** (the shape #93 sketched). Rejected: collapsing the
  registry and the default order into one ordered `ColumnSet` removes the
  drift *class* entirely, which beats testing for it.
- **A non-generic helper over `[]string` keys + a `map[string]func`.**
  Rejected: it reproduces today's stringly-typed registry and keeps the
  nil-value-func footgun; `Column[T]` makes an unresolved/unknown column
  unrepresentable in a `Payload`.
- **Home `UsageError` in a new `internal/clierr` package.** Rejected:
  `internal/exit` already is the exit-code vocabulary; the exit-2 type
  belongs next to `exit.For`, not in a parallel package.
- **Fold this into #38's Renderer rework.** Deferred: #38 changes the
  dispatch shape and contradicts ADR-0005; this change is orthogonal and
  the bigger line-count win, so it lands on its own.
