# ADR-0005 — TTY-aware output defaults and shared format dispatcher

## Status

Accepted.

amended (2026-11-20): implementation decisions moved from code comments

## Context

`docs/DESIGN.md` §7 and `CLAUDE.md` both promise three things that the
code does not currently deliver:

1. **`--output markdown` exists.** Documented as a first-class Format
   alongside `table` and `json`. The current code returns
   `unsupported --output "markdown" (want table or json)`.
2. **The default is TTY-aware.** `table` when stdout is a terminal,
   `json` when piped or in CI. Every command currently hardcodes
   `"table"` as the default flag value, so `gplay auth status | jq .name`
   fails and CI scripts must pass `--output json` everywhere.
3. **A single dispatch path.** Each command today carries its own
   `switch output { case "table": ...; case "json": ...; default: error }`
   block. Markdown and TTY-detection would multiply this drift across
   every new command.

The fixes are coupled: adding `markdown` without centralising the
dispatch path triplicates the bug; centralising the dispatch path
without TTY detection just moves the hardcoded default.

## Decision

A new package `internal/output` owns Format selection and dispatch. Each
command supplies three Renderers (one per Format) and calls one helper.

### Format

```go
package output

type Format string

const (
    FormatAuto     Format = ""         // flag default — dispatcher resolves
    FormatTable    Format = "table"
    FormatJSON     Format = "json"
    FormatMarkdown Format = "markdown"
)
```

`--output` defaults to `FormatAuto` (empty string). The dispatcher
resolves it to a concrete Format:

- If `CI=true` (any non-empty value) → `FormatJSON`.
- Else if stdout is not a TTY → `FormatJSON`.
- Else → `FormatTable`.

An explicit `--output table` always wins, even when stdout is piped. This
is the escape hatch for `tee`, TTY-emulating wrappers, and any case
where the auto-detect is wrong.

### TTY detection

`golang.org/x/term.IsTerminal(int(fd))` against the writer's file
descriptor. The writer is asserted to have an `Fd()` method, not to be an
`*os.File`: `main` wraps stdout in a byte counter (#593), and that wrapper
keeps `Fd()`, so commands do not suddenly see a pipe and switch their
default format to JSON. A writer without a descriptor is not a TTY. The check
is wired through a package-level function variable so tests can force TTY or
non-TTY without spawning a pty.

`NO_COLOR` is **not** consulted here — that knob is colour-only, lives
in DESIGN.md §8, and is enforced when colour is added (a separate
concern).

### Renderer interface

```go
type Renderers struct {
    Table    func(w io.Writer) error
    JSON     func(w io.Writer) error
    Markdown func(w io.Writer) error // may be nil — see below
}

func Render(w io.Writer, requested Format, r Renderers) error
```

`Render` resolves the Format (calling `Resolve(requested, w)` first),
picks the matching field, and returns
`fmt.Errorf("unsupported --output %q (want table, json, or markdown)", ...)`
when the field is nil. Each command's `run()` builds a `Renderers`
value and delegates.

### Markdown semantics

Markdown is a **first-rank** Format, not "table in markdown syntax".
Each command renders Markdown in whatever shape is idiomatic for its
data:

- `auth list` → a Markdown table (helper: `output.MarkdownTable`).
- `auth status` → a list of `- **Field**: value` lines or a fenced block.
- `auth doctor` → a GitHub-style checklist (`- [x] Check 1`, `- [ ] Check 2 — hint: ...`).

A single shared helper `output.MarkdownTable(headers, rows)` covers the
tabular cases (`list` today, `apps list` and the future release/track
commands tomorrow). Non-tabular commands write their Markdown inline —
the cost is ~15 lines of `fmt.Fprintf` per command, comparable to their
existing `renderTable` body.

### Login / logout

`auth login` and `auth logout` do not have `--output` and do not grow it
in this slice. They emit free-form human text on stderr-style success
messages; there is no structured payload worth formatting three ways.
This is recorded explicitly in DESIGN.md §7 so future commands without
structured output do not get forced into the dispatcher.

## Implementation notes

- **A record view keeps its empty rows.** The key-value view of a single
  record (`appstore catalog view`) prints every row, empty values included,
  so the shape of the record never depends on the data; only whole empty
  sections are skipped.
- **Human views are sanitised line by line** as they are written (DESIGN §7,
  control-sequence sanitization). Holding back the unfinished line keeps an
  escape sequence or a multi-byte rune split across two writes (`tabwriter`,
  `bufio`) whole for the filter, while a view of several hundred MiB (a
  markdown report) never sits in memory at once. No sequence survives a line
  break: its ESC is a control rune, dropped on the line that holds it.
- **A raw `userFraction` renders in its shortest decimal form**
  (`strconv.FormatFloat(f, 'f', -1, 64)`: `0.1`, `0.5`, `1`), never with the
  `'g'` verb, which switches to scientific notation for a small staged
  fraction (`1e-03` instead of `0.001`).
- **A one-line summary of a track shows its release with the highest
  versionCode.** A track can carry several releases at once (a staged
  rollout above a completed one, a draft above production); the highest
  versionCode is the newest build whatever order the API lists them in, and
  the status column says whether that build is a draft (`tracks list`).
- **A command whose output splits per item between stdout and stderr writes
  it itself.** `reviews reply` sends a stderr confirmation plus the verbatim
  API body for one reply, and per-line `OK`/`ERR` on stderr plus a
  `{"results":[…]}` envelope for a `--batch`. That split does not fit the
  three-renderer payload, so its `Run` writes through `output.WriteJSON` and
  the logging helpers and returns only an error. It is the exception: a
  command that can return a `Renderable` does.

## Consequences

- The dispatcher centralises the "unsupported format" error path. New
  commands that do not yet support Markdown leave `Renderers.Markdown`
  nil and inherit a uniform error message.
- The flag value default changes from `"table"` to `""` (FormatAuto).
  This is a user-facing change: `gplay auth status | jq` now works
  without `--output json`. Scripts that already pass `--output table`
  explicitly are unaffected.
- A new dependency on `golang.org/x/term` (already an indirect via
  `golang.org/x/sys`). One direct line in `go.mod`.
- Per-command `OutputTable`/`OutputJSON` constants (introduced in #15)
  are replaced by the centralised `output.Format*` constants. The
  rename is mechanical.

## Alternatives considered

- **Markdown = table-in-markdown syntax only.** Rejected: forces a
  1-row, N-column Markdown table on `auth status` (artificial) and
  breaks `auth doctor`'s checklist idiom (the very reason that command
  exists). The whole point of Markdown output for agents is that they
  digest idiomatic Markdown — a forced tabular shape is no better than
  the table output for a non-tabular command.
- **Per-command `chooseDefault()` helpers.** Rejected: every command
  would re-implement TTY detection, the CI env-var check, and the
  "unsupported format" error string. Three copies today, ten tomorrow.
- **Honouring `NO_COLOR` here.** Rejected: `NO_COLOR` is about ANSI
  colour, not output shape. Conflating the two would make
  `NO_COLOR=1 gplay auth status` silently switch from `table` to `json`,
  which is not what `NO_COLOR` means anywhere else in the ecosystem.
- **A pluggable renderer registry (`map[Format]Renderer`).** Rejected
  for v1: three named fields on a struct give the same flexibility with
  static typing and IDE support, and there is no extensibility need
  outside the three documented Formats.
