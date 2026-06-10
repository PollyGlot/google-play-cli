# Design conventions — gplay

Living reference for the CLI design decisions that didn't rise to ADR-level
(reversible, or "the obvious thing once you know the constraint") but that we
still want to be consistent about across commands.

For deeper rationale on the load-bearing choices, see:

- [ADR-0001 — Credential storage](./adr/0001-credential-storage.md)
- [ADR-0002 — Safe production defaults](./adr/0002-safe-production-defaults.md)
- [ADR-0003 — `--output json` is API pass-through](./adr/0003-json-passthrough.md)
- [ADR-0004 — Cascading config](./adr/0004-cascading-config.md)
- [ADR-0005 — TTY-aware output defaults](./adr/0005-tty-aware-output.md)
- [ADR-0019 — Canonical verb vocabulary](./adr/0019-canonical-verb-vocabulary.md)
- [ADR-0023 — JSON error envelope on failure](./adr/0023-json-error-envelope.md)
- [ADR-0024 — `GPLAY_READONLY` environment policy](./adr/0024-readonly-environment-policy.md)

---

## 0. Verb vocabulary

Every command verb belongs to one of three categories. The rationale and the
considered alternatives live in
[ADR-0019](./adr/0019-canonical-verb-vocabulary.md); this is the normative
quick-reference.

**1. CRUD grammar** — generic resource verbs, held to strict consistency:

| Gesture | Verb | Example |
|---|---|---|
| Read many | `list` | `gplay tracks list` |
| Read one addressed resource | `view` | `gplay apps view`, `gplay tracks view` |
| Session / health (no addressed resource) | `status` | `gplay auth status` |
| Bring a new object into existence | `create` | `gplay tracks create <name>` |
| Add/remove membership of an existing entity | `add` / `remove` | `gplay apps add`, `gplay team users remove` |
| Delete a resource / clear a field | `remove` | `gplay team grants remove` |
| Write/declare state with explicit values | `set` | `gplay apps details set`, `gplay testers set` |

Deciding rules:

- **`view` vs `status`:** `view` reads a resource you point at (`--package`,
  `--track`, …); `status` reports session/health with nothing pointed at.
  `auth status` is the only `status`.
- **`create` vs `add`:** ask "am I making the object *exist*?" Yes → `create`
  (a closed track). No, it already exists and I am enrolling it in a
  collection → `add` (registering an app locally, adding a user to the org).
  gplay cannot create or delete an app on Play, so apps use `add`/`remove`,
  never `create`/`delete`.
- **`remove`, never `revoke`:** one delete verb across the surface.
- **`set`, never `edit`:** writes are declarative/idempotent (full-replace or
  deterministic patch), non-interactive, and `edit` would collide with the
  `Edit` transaction concept (`gplay edits …`).
- **No verb-less reads:** a read always carries `view` (so `apps details view`
  and `tracks availability view`, not the bare nouns).

**2. Domain verbs** — each names a real gesture no generic verb captures:
`upload`, `promote`, `rollout`, `halt`, `resume`, `complete`, `reply`, `pull`,
`apply`, `validate`, `login`/`logout`. Admission test: it must state a domain
gesture `set`/`create`/`view` could not say honestly. The `releases` rollout
state machine (`rollout`/`halt`/`resume`/`complete`) lives flat here — these
act on a release's rollout *state*, not on a "rollout" resource, so they are
not nested.

**3. Reference / diagnostic / scaffold** — meta-commands outside the resource
grammar, keeping their own names: `version`, `exit-codes`, `auth doctor`,
`team permissions` (offline catalog), `init`.

---

## 1. Authentication

### Credential resolution precedence

In order, first match wins:

1. `--service-account <path-or-json>` flag on the command
2. `--account <name>` flag (selects a stored Account)
3. `GPLAY_SERVICE_ACCOUNT` env var (path or inline JSON)
4. `GPLAY_ACCOUNT` env var (name of a stored Account)
5. The Account marked **active** in `~/.gplay/config.json` (or `$XDG_CONFIG_HOME/gplay/config.json`)

If nothing resolves: exit code `10` with a message pointing at `gplay auth login`
and the env var docs.

**Absent vs. invalid.** Resolution has two distinct failure modes, and gplay
keeps them apart ([ADR-0020](adr/0020-resolution-error-surfacing.md)):

- **Absent** — no source is configured (none of layers 1–5 yield one), or the
  named/active Account has no key in the store. This is a benign state: a
  command that consumes a credential exits `10` ("run `gplay auth login`"),
  but `gplay auth status` reports "No active account" and exits `0`, and
  read-only `apps list` still works off the local registry.
- **Invalid** — a credential *was* provided but its bytes are unusable
  (malformed JSON, a missing required field, an unreadable file, a keystore
  read error). This is always an error: exit `10` with the underlying cause
  in the message (`could not read credential: <cause>`), on **every** command
  — including `auth status`, which no longer masks a corrupt active credential
  as "No active account".

### `gplay auth doctor`

Runs these checks in order, stopping on the first hard failure:

1. Service account JSON present, readable, well-formed (required fields:
   `client_email`, `private_key`, `token_uri`).
2. OAuth2 access token can be minted (RSA-signed JWT exchange succeeds).
3. Token bears the `androidpublisher` scope.
4. For every package in the local registry (or the one passed via
   `--package`): call `edits.insert` then `edits.delete` round-trip. Catches the
   common case "SA created but not invited on the app in Play Console".

Output: one `✅`/`❌` line per check, with an action hint on failure.

---

## 2. Package targeting

### Project pinning

`gplay init --package com.example.myapp` writes `.gplay/config.json` at the
repo root. Any subsequent command run inside that tree (we walk up from cwd
looking for `.gplay/`) defaults its target to that package — so `--package`
becomes optional.

`--package` always overrides.

### Cascading layers (see [ADR-0004](./adr/0004-cascading-config.md))

The same `config.json` schema appears at three levels. Later wins:

```text
$XDG_CONFIG_HOME/gplay/config.json     (global, machine-local — Accounts live here)
<repo>/.gplay/config.json              (project shared, committed — package pin)
<repo>/.gplay/config.local.json        (project local, gitignored — per-developer overrides)
GPLAY_* env vars                       (e.g. GPLAY_ACCOUNT, GPLAY_SERVICE_ACCOUNT)
CLI flags                              (e.g. --account, --package, --service-account)
```

The walk-up that finds `.gplay/` refuses to traverse into `$HOME` (or any
ancestor of `$HOME`) so a stray `~/.gplay/config.json` cannot masquerade
as a project pin. `gplay init` refuses to run when `cwd == $HOME` for
the same reason.

### Field rules

- **`account` is forbidden in committed `config.json`.** Account names
  are machine-local; pinning one in shared state breaks teammates. The
  loader rejects it with an error naming the offending file path.
- `account` may appear in `config.local.json`, as `GPLAY_ACCOUNT`, or as
  `--account`.

### `.gplay/` contents

- `config.json` — package pinning (the `package` field). **Commit this.**
- `config.local.json` — per-developer overrides (`account`, rarely
  `package`). **Gitignore this** — `gplay init` writes the rule for you
  in `.gplay/.gitignore`.
- `edit-<package>.json` — open explicit Edit ID (see `CONTEXT.md` → Edit).
  **Gitignore this** too — transient and per-developer; covered by the
  same `.gplay/.gitignore`.

---

## 3. Release commands

### Status defaults (see ADR-0002)

| Target track | Default status | Default userFraction |
|---|---|---|
| `production` | `draft` | — |
| `internal` / `alpha` / `beta` / closed | `completed` | `1.0` |

Explicit overrides: `--complete`, `--staged <fraction>`, `--draft`.

### `--track` is passthrough

Any string is accepted. The four standard tracks (`internal`, `alpha`, `beta`,
`production`) are documented but not enforced — closed-test tracks with
custom names work out of the box.

### Release notes

Two flags, mutually exclusive:

- `--release-notes "<text>"` — single text applied to the app's
  `defaultLanguage`.
- `--release-notes-dir <dir>` — one file per locale (`en-US.txt`, `fr-FR.txt`,
  ...). Optional `default.txt` is used as fallback for locales without a
  dedicated file.

### Targeting a release

- `releases upload <aab>` → versionCode is read from the AAB; never a flag.
- `releases promote/rollout/halt/resume/complete --track <X>` → targets the
  **latest** release on the track. Override with `--version-code N` or
  `--release-name <name>`. If two releases coexist on the track (e.g.
  `inProgress` + `halted`) and no flag is passed, refuse with exit code `60`
  rather than guess.

### Rollout state machine

Each transition is its own verb:

- `gplay releases rollout --to <fraction>` — set userFraction (status becomes
  `inProgress` if it wasn't already)
- `gplay releases halt`
- `gplay releases resume`
- `gplay releases complete` — userFraction → 1.0, status → `completed`

---

## 4. Edit lifecycle

### Implicit edits (default)

`begin → upload/patch → commit` is wrapped inside each write command. On any
failure after `begin`, the Edit is **auto-discarded** before the error
propagates. Pass `--keep-edit-on-failure` to bypass cleanup when debugging.

### Explicit edits

`gplay edits begin / commit / discard`. The Edit ID is persisted to
`.gplay/edit-<package>.json` so subsequent write commands in the same cwd
reuse it. No auto-discard in this mode — explicit `commit` or `discard` is
required.

---

## 5. Reviews

- API hard limit: **only the last 7 days** are exposed. Surfaced in `--help`
  and as a stderr `WARN:` line on **every** successful run — including an empty
  result (a quiet empty result must not read as "this app has no reviews").
- Auto-pagination is on by default; `--limit N` caps the result count, default
  is no cap.
- `--stars` (e.g. `1`, `1-2`, `1,3,5`) is a **client-side** filter — the API
  has no server-side rating filter.
- Long-history retrieval (CSV reports in the GCS bucket) is in `BACKLOG.md`.

---

## 6. Apps registry (workaround for missing `apps.list` endpoint)

The Google Play Developer API has no `apps.list`, so `gplay apps list` reads a
**local registry**:

- Populated by `gplay init --package X` (auto-adds) or `gplay apps add X`
- Stored in `~/.gplay/config.json` alongside Accounts
- `gplay apps view --package X` still hits the live API (via `edits.details`
  etc.) — only enumeration is local

Backlog: real discovery via Cloud Resource Manager + IAM (see `BACKLOG.md`).

---

## 7. Output

### Formats

A command's output Format is one of `table`, `json`, or `markdown`. The
default Format is `auto` — resolved by the dispatcher in `internal/output`:

- `CI=true` (non-empty) → `json`
- stdout is not a TTY → `json`
- otherwise → `table`

Explicit `--output table|json|markdown` always wins. `--output table` in
a piped context (e.g. behind `tee`) is the escape hatch when the
auto-detect is wrong. See [ADR-0005](./adr/0005-tty-aware-output.md).

### Commands without `--output`

`auth login` and `auth logout` emit free-form human text and do not
expose `--output`. There is no structured payload that would survive
three Renderers, and forcing one would invent a schema with no consumer.
Any future command in the same shape (side-effecting, no structured
result) follows the same rule.

### `--output markdown`

Markdown is a first-rank Format, not "table-in-markdown syntax". Each
command renders the shape that fits its data:

- Tabular data → a Markdown table (helper: `output.MarkdownTable`).
- Status / info → a list of `- **Field**: value` lines.
- Checklists (`auth doctor`) → GitHub-style task list
  (`- [x] Check 1` / `- [ ] Check 2 — hint: ...`).

### `--output json` is API pass-through (ADR-0003)

The JSON output mirrors the Google Play Developer API's native response
shape, including its per-endpoint envelope (`{"reviews":[...]}`,
`{"tracks":[...]}`, etc.). Exception: `apps list` (no API source) uses
`{"apps":[...]}`.

**Scope.** The pass-through guarantee applies to commands that *wrap a
Developer API call* — their JSON is the API's response, unowned by gplay.
**Offline reference commands wrap no API call and synthesise their own JSON**
(`team permissions`, `schema`): the shape is gplay's, documented per command,
and free to evolve (a `schema` is additionally `[experimental]`). Pass-through
is a promise about *not reshaping the API*, not a promise that every `--output
json` is an API echo.

### `--output table`

Columns are chosen for readability — not pass-through. Each command's
default columns are documented in its `--help`. `--columns col1,col2,...`
lets the caller override.

### Control-sequence sanitization (human formats only)

API-returned strings are often user-generated (review text, store-listing
copy). The **table and markdown** renderers strip ANSI escape sequences (CSI,
OSC) and C0/C1 control characters from every cell, centrally at the render
boundary — so a hostile value cannot inject color/cursor/title sequences into a
terminal or CI log. The sanitization is rune-based: legitimate multi-byte
content (accents, CJK, emoji) is untouched. **`--output json` is never
sanitized** — machine consumers get the bytes verbatim (ADR-0003); fidelity
lives on the JSON path, safety on the human path.

### Errors (`--output json` error envelope, ADR-0023)

Errors are **never** pass-through. The human-readable line always goes to
**stderr** (DESIGN §8). Under `--output json` a failing command *additionally*
writes a single structured envelope to **stdout**, so an agent/CI consumer can
branch on the failure without scraping stderr:

```json
{
  "error": {
    "exitCode": 60,
    "message": "edits.commit on com.example.app: edit already exists (HTTP 409) [reason: editAlreadyExists]",
    "reasons": ["editAlreadyExists"],
    "requires": ["confirm"]
  }
}
```

- `exitCode` / `message` are always present; `exitCode` mirrors the process
  exit code (§9).
- `reasons` carries the upstream `error.errors[].reason` values when an API
  envelope was parsed; omitted otherwise.
- `requires` names the missing safety flag on an exit-3 refusal (extends the
  ADR-0017 dry-run `requires` to failure time); omitted otherwise.

Under `table` / `markdown` a failure leaves stdout empty (error → stderr only).
Exit codes and stderr are unchanged by the envelope. The envelope shape is part
of the public contract (ADR-0010); see [ADR-0023](./adr/0023-json-error-envelope.md).

---

## 8. Verbosity and logging

- **stdout** carries data only (the requested output).
- **stderr** carries logs, progress, warnings, errors. Always.
- `-v` / `--verbose` → info level on stderr (flow steps, edit ID, deduced
  versionCode, ...).
- `-vv` → debug level (HTTP method + URL, headers, truncated bodies).
- `-q` / `--quiet` → only errors on stderr.
- Progress bars (e.g. AAB upload) are active **only in TTY** and disabled by
  `--quiet`.
- Color is auto in TTY, disabled in pipes, disabled if `NO_COLOR` env or
  `--no-color` is set.

### Request timeouts (`--timeout`)

Every API call carries a deadline so a hung connection fails the step in
seconds instead of stalling a CI job until the runner-level kill:

- **Control-plane calls** (Edits, tracks, reviews, metadata, team, …) get a
  **60s default** deadline, applied once where the kernel builds the
  authenticated HTTP client — every command inherits it, no per-command
  plumbing.
- **Media uploads** (`releases upload`, `metadata images apply`) are **exempt
  from the default**: a multi-hundred-MB transfer is never killed by the short
  control-plane bound.
- The global **`--timeout <duration>`** flag (e.g. `--timeout 30s`,
  `--timeout 2m`) overrides both — it bounds *every* request, uploads included.
  Unset (`0`) means "60s for control-plane, unbounded for uploads".

A deadline-exceeded failure is a transport-level error and maps to **exit 50**
(network), the retry-safe bucket — so the same CI wrapper that retries a DNS
blip retries a timeout.

### Opt-in retry (`--retry`)

The global **`--retry N`** flag (default `0` = no retry) layers a transport
middleware on the authed client that retries the transient classes — transport
errors, HTTP 5xx, and 429 (honoring `Retry-After`) — with exponential backoff
plus jitter. Non-transient 4xx (auth, validation) and `edits.commit` (a
duplicate could double-publish) are never retried, so it is safe to leave on.
When `--retry` is set, `--timeout` becomes a **per-attempt** bound rather than a
single per-request one; request bodies are recreated per attempt (uploads
re-send from a fresh reader). Details and CI examples: [`CI_CD.md`](CI_CD.md#4-exit-codes--retry-vs-fail).

### Read-only policy (`GPLAY_READONLY`, ADR-0024)

`--confirm` / `--grant-admin` are advisory for an AI agent that holds the
credential — it can pass them itself. `GPLAY_READONLY` is the
environment-enforced authority boundary a harness can impose independent of the
model's flag choices:

- When `GPLAY_READONLY` is **truthy** (`1`/`true`/`yes`/`on`), every command
  that mutates Google Play state is **refused with exit 4** — *before*
  credential resolution and any network I/O, regardless of the flags passed.
  Exit 4 is distinct from exit 3 on purpose: it is **not** resolvable by adding
  a flag (the message says so); the caller must change the environment.
- **Read commands and `--dry-run` of mutating commands still run**, so
  dashboards and agents can observe and plan with a production credential.
- Which commands mutate is a single registration-time annotation
  (`kernel.MarkMutating`), not an ad-hoc per-command check — see CONTRIBUTING.
- Scope: the policy blocks **Google Play mutations**. Local-only operations
  (credential `auth login`/`logout`, the local app registry) are not Play
  writes and are not gated. Fine-grained allowlists (`GPLAY_ALLOW`) are a
  future follow-up, out of scope here.

Under `--output json` the refusal is emitted as the standard error envelope
(exit code 4) on stdout (§7 / ADR-0023).

---

## 9. Exit codes

| Code | Meaning | Retry-safe? |
|---|---|---|
| `0` | Success | — |
| `1` | Generic error (fallback when nothing more specific fits) | No |
| `2` | CLI misuse (unknown flag, bad value, missing required arg) | No |
| `3` | Safety flag required — command is well-formed but a named acknowledgment flag (`--confirm` / `--grant-admin`) is missing; the message names it | Deterministic (re-run with the named flag) |
| `4` | Denied by environment policy (`GPLAY_READONLY`) — a mutating command was refused; the message names the env var | No — **not** resolvable by adding a flag; change the environment |
| `10` | Authentication failure (SA invalid, token refused, scope missing) | No |
| `11` | Authorization (`403` — SA not invited on the app, etc.) | No |
| `20` | Client-side validation (malformed AAB, unknown locale, ...) | No |
| `30` | API 4xx other than auth/perms (not found, conflict, gone, ...) | No |
| `40` | API 5xx (upstream temporarily unhealthy) | **Yes** |
| `50` | Network (timeout, DNS, refused) | **Yes** |
| `60` | State conflict (another Edit open and unrecoverable, rate-limited, ambiguous release target, ...) | Sometimes |

Documented in `gplay help exit-codes` and `docs/CI_CD.md`.

---

## 10. Tracks and testers

`gplay tracks create` and `gplay testers list/set` manage custom closed
testing tracks and their authorized audience. Both surfaces are shaped by
hard constraints of the Play Developer API — see `CONTEXT.md` (**Closed
track**, **Tester**) for the domain terms.

### Creating tracks

- `gplay tracks create <name>` creates a **closed testing** track. The
  create endpoint (`edits.tracks.create`) supports exactly one type
  (`CLOSED_TESTING`), so there is **no `--type` flag** — every created
  track is closed. Open/internal track creation has no API path.
- The new track's form factor is `DEFAULT` (phone). `WEAR` / `AUTOMOTIVE`
  closed tracks are deferred (`BACKLOG.md`) behind a future
  `--form-factor` flag — additive, non-breaking when it lands.
- Creating a track that **already exists** surfaces the API error (exit
  30); gplay does not fake idempotency. "Ensure exists" would be an
  explicit future `--if-not-exists`.
- There is **no `tracks delete`** — the API exposes
  create/get/list/patch/update but no delete. Removing a track is a Play
  Console-only gesture.

### Managing testers

- The `edits.testers` resource exposes a **single** field, `googleGroups[]`,
  and explicitly does not support individual tester emails ("email lists
  are not supported by this resource"). So `gplay testers` manages
  **Google Groups only** (`--group a@googlegroups.com,...`); there is **no
  `--email`**. Adding individuals one-by-one stays Console-only.
- Testers are **declarative**: `testers set` replaces the whole group list
  (maps 1:1 to `testers.update`), `testers list` reads it (`testers.get`).
  No `add` / `remove` — the typical case is a single group, and a
  declarative replace is idempotent (agent/CI-friendly).
- A bare `testers set` with neither `--group` nor `--clear` is a misuse
  (exit 2), so a forgotten `--group` can never silently wipe the list.
  Emptying the list on purpose is the explicit `--clear`.
- Testers are keyed per-track (`…/testers/{track}`), so `--track` is
  **required**. gplay does not restrict which track names are valid —
  `testers set --track production` is sent to the API, which rejects it;
  gplay surfaces that error rather than re-implementing the rule.

### Write safety

Both commands are writes: implicit Edit (open → mutate → commit), with
`--dry-run` (validate + preview the payload, no HTTP) and
`--keep-edit-on-failure` (skip auto-discard for debugging), matching
`releases upload` / `promote`. **No `--confirm`**: a closed test track is
low-stakes and reversible, unlike a production rollout.

### Discovering the create step

`releases upload` / `promote` keep their passthrough `--track` (§3.2). An
upload/promote to a closed track that **does not exist yet** fails at the
API; gplay attaches a hint pointing at `gplay tracks create <name>` (same
pattern as the other track-not-found hints). gplay **never auto-creates** a
track as a side effect of an upload — a typo'd `--track` must fail, not
silently spawn a phantom track.
