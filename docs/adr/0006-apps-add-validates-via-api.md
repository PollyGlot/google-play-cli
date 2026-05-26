# ADR-0006 — `gplay apps add` validates via API by default

## Status

Accepted.

## Context

`gplay apps add <package>` is the first registry-mutating command in
gplay's lifecycle: after `gplay auth login` registers the Service
Account and `gplay init` (or `gplay apps init`) pins the package to a
repo, `apps add` records the (Account, Package) pairing in the global
config so commands like `gplay vitals` or `gplay reviews list --all`
can iterate over every package the active credential is responsible
for.

Two failure modes are responsible for most "gplay can't reach my app"
support traffic:

1. **Typo in the package name.** `com.example.myaap` vs
   `com.example.myapp` — the registry happily stores either; the
   mistake only surfaces weeks later when the first `gplay releases
   upload` lands a 404 in CI.
2. **Service Account not invited on the app.** The Play Console
   requires a per-app permission grant on top of project-level IAM.
   The credential authenticates fine (no 403 on the token exchange),
   but every Edit-mutating call against that app returns a 403. Again,
   the failure typically surfaces in CI well after `apps add` succeeded.

Both are recoverable client-side mistakes that a cheap API round-trip
at registration time would catch instantly: open an Edit on the package
(`edits.insert`) and immediately discard it (`edits.delete`). No state
changes, no commit, the call is idempotent and there are no rate-limit
implications for a one-shot probe.

## Decision

`gplay apps add` validates access via API by default. The probe is the
existing read-only Edit lifecycle (`WithReadOnlyEdit` in
`internal/play/edits`), wrapped behind a thin `edits.Validate(ctx, hc,
pkg)` entrypoint. An explicit `--no-verify` flag skips the round-trip
for the rare cases where it's wrong — offline or preparatory
registration when the credential is known to lack reachability today
but will gain it later.

Persistence is gated on validation success: a failed probe propagates
verbatim from `WithReadOnlyEdit`, the global config is never written,
the registry stays consistent with what the credential can actually
touch.

### Exit-code mapping

The probe surfaces three Play-API failure modes the user needs to
distinguish:

| Symptom | Status | Exit | Source |
|---|---|---|---|
| SA not invited on the app | 403 | 11 | `api.StatusToExitCode` (existing) |
| Package does not exist | 404 | 30 | `api.StatusToExitCode` (existing) |
| Edit already open on the package | 400 + `editAlreadyExists` reason | 30 | `edits.EditConflictError` (new) |

The first two reuse the existing `*api.Error.ExitCode()` table without
modification. The third case is new and carries a *distinct* exit 30
message (not the generic "HTTP 400") with a recovery hint pointing the
operator at the only two paths that work today: wait ~24h for the open
Edit to auto-expire, or release it manually via the Google Play
Console. A `gplay edits discard --package <pkg>` subcommand is planned
per DESIGN.md §4 but not yet wired; the message intentionally does not
reference it so users following the hint are not dead-ended at "unknown
command". Exit 30 (API 4xx, recoverable) rather than 60 (state conflict,
often non-recoverable) signals that a clear remediation path exists; 60
would imply "give up and call support".

Detecting the `editAlreadyExists` condition required teaching
`api.Error` to retain the structured `errors[].reason` values from the
Google API error envelope; the existing top-level `message` field is
not discriminating ("Edit ID is required" for `editAlreadyExists` and
several other failures). The new `api.ParseErrorEnvelope` helper
returns both message and reasons; `APIErrorMessage` is preserved as a
backwards-compatible wrapper.

### Registry storage

The registry lives on the global config layer
(`$XDG_CONFIG_HOME/gplay/config.json` or
`~/.gplay/config.json`, see ADR-0004), scoped under each Account.
On-disk shape:

```json
{
  "accounts": [
    {
      "name": "playci",
      "active": true,
      "packages": ["com.example.myapp", "com.example.utils"]
    }
  ]
}
```

`packages` is `omitempty`, so configs written before the field existed
round-trip unchanged. `internal/apps/registry` operates on the
`[]config.Account` slice directly via pointer receivers; persistence
goes through `config.Global.Save` (the caller's job). This keeps the
registry package pure — no IO, no logging — and reuses the existing
locked-file write path from auth/login.

Per-Account scoping (rather than a global package list) matters because
the same package can be reachable from multiple Accounts (CI vs dev)
or unreachable from some (a shared registry would lie). A package
registered under Account A and only Account A is invisible to
operations running under Account B; this matches the "Account is the
identity unit" rule from ADR-0001.

## Consequences

- `gplay apps add` makes exactly one HTTP round trip per invocation in
  the default mode: one `edits.insert` followed by one `edits.delete`
  (the delete is a defer'd cleanup in `WithReadOnlyEdit`). The
  oauth2 `/token` exchange is shared with any later command in the
  same process and is not counted against the per-day publish quota.
- `--no-verify` exists, is documented, and is the supported way out of
  the cheap-but-not-free round trip when needed. It is not the default
  because the failure modes it papers over are exactly the ones the
  command is designed to catch.
- The per-Account `packages []string` field is the foundation for
  later read-side commands (`gplay apps list`, `gplay apps info`) and
  for fleet-wide loops (`gplay vitals --all`, `gplay reviews list
  --all`). Future ADRs covering those commands inherit this on-disk
  shape rather than re-deciding it.
- The structured `errors[].reason` capture on `*api.Error` benefits
  any future command that needs to discriminate between Play API
  failures sharing the same HTTP status (e.g. distinguishing
  `quotaExceeded` from `rateLimitExceeded` on a 429). It is a small
  extension to a load-bearing type — adopt cautiously, don't grow
  the field set in this ADR.

## Alternatives considered

- **Always validate, no `--no-verify`.** Rejected. The 1% case
  (registering a package the SA is going to gain access to next week)
  is real and the cost of getting it wrong (refusing a valid
  registration) is worse than the cost of papering over a typo for one
  user who explicitly asked for it.
- **Validate as a separate `gplay apps validate <pkg>` command,
  decoupled from `apps add`.** Rejected. Two-step workflows decay; the
  whole point is that the failure surfaces *at registration time* with
  no extra cognitive load.
- **Map `editAlreadyExists` to exit 60 (state conflict).** Rejected.
  60 is for state the user cannot easily reverse (a half-rolled-out
  release, a rate-limited account). A stale open Edit is recoverable
  with one command. 30 with a distinct hint is the right shape.
- **Store the registry as a separate file
  (`$XDG_CONFIG_HOME/gplay/registry.json`).** Rejected. Splitting the
  global config across two files doubles the locking surface for the
  same logical write transaction. Embedding it as a per-Account field
  in the existing `config.json` keeps `auth login` and `apps add`
  using the same load/save primitives.
