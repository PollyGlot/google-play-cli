# Write-safety tiers and agent-resolvable refusals (exit 3)

## Status

accepted

## Context

`gplay team` (PRD #147) is gplay's first surface with frequent, *varied*
write-safety needs in one namespace: routine permission changes,
destructive off-boarding, and the high-blast-radius act of conferring
admin. gplay already owns two safety primitives, and this ADR reuses them
rather than inventing a third:

- **Declarative replace + explicit empty** — `testers set` replaces the
  whole list, has no `add`/`remove`, and a bare `set` is a misuse (exit 2)
  so the list can never be silently wiped; emptying is the explicit
  `--clear` (DESIGN §10). Idempotent, agent/CI-friendly.
- **Named opt-in for asymmetric danger** — the production release path
  requires a *named* flag (`--complete` / `--staged`), so a forgotten flag
  is a safe no-op, not a catastrophe ([ADR-0002](./0002-safe-production-defaults.md)).

gplay is agent-first and CI-first. The exit taxonomy (DESIGN §9) has **no
code** for "well-formed, but a safety-acknowledgment flag is missing" —
today that case collapses into exit `2` (CLI misuse). For an automated
caller those are opposite signals: `2` means *malformed → do not retry*,
whereas "add `--confirm` and re-run" is *deterministically resolvable*.
Conflating them makes an agent either loop on a real error or abandon a
trivially fixable one.

## Decision

1. **Three write-safety tiers**, each reusing an existing primitive:
   - **Routine** (`users add`, `users set`, `grants set` that do **not**
     confer admin): no gate; `--dry-run` is the rehearsal. Keeps CI
     onboarding scriptable (US8).
   - **Destructive** (`users remove`, `grants revoke`): require
     **`--confirm`**.
   - **Admin-conferring** (any write whose resolved permission set
     includes the all-permissions enum — `--role admin` or raw
     `CAN_MANAGE_PERMISSIONS[_GLOBAL]`): require the dedicated, named
     **`--grant-admin`**, on top of its tier (ADR-0002 pattern).

2. **`set` is declarative replace** (like `testers set`): `--role` **XOR**
   `--permissions`; a bare `set` is a misuse (exit 2), so permissions can
   never be silently blanked; emptying on purpose is the explicit
   **`--clear`**. A permission-*reducing* `set` is a normal declarative
   statement (previewable via `--dry-run`), **not** a separately gated
   event — gating downgrades would contradict the declarative model.

3. **New exit code `3` — "a required safety flag is missing."** Additive
   to DESIGN §9. Distinct from `2`: the command is well-formed; the only
   thing missing is a named acknowledgment flag, which the error message
   states verbatim. Deterministically resolvable by re-running with that
   flag (not blind-retry-safe like `40`/`50` — the caller knows the exact
   fix).

4. **`--dry-run` surfaces the gates machine-readably.** `--output json`
   includes a `requires` array (e.g. `["grant-admin"]`) beside the
   previewed payload, so the canonical agent flow is *dry-run → read
   `requires` → re-run live with exactly those flags*, with no
   trial-and-error against exit codes.

5. **`team permissions --output json` marks admin-conferring** aliases and
   bundles, so the danger is discoverable *before* a command is built.

6. **No interactive prompts; `CI=true` never auto-confirms** any gate
   (ADR-0002/0014 inheritance). Every gate is a flag.

## Considered options

- **Reuse exit `2` for a missing safety flag.** Rejected: conflates
  *malformed* (do-not-retry) with *resolvable* (add-flag-and-retry) — the
  one distinction an automated caller most needs.
- **A single generic `--confirm` for everything, including admin.**
  Rejected: a habitually-typed `--confirm` hands out full control by
  reflex; the named `--grant-admin` makes the dangerous act name itself.
- **Gate a permission-reducing `set` behind `--confirm`.** Rejected:
  incoherent with declarative replace; `--dry-run` plus the
  `--clear`-to-empty rule already prevent accidental loss.
- **Signal "confirmation required" only via stderr/JSON text, no new exit
  code.** Rejected: CI branches on exit codes without parsing; the
  structured `requires` field complements the code, it does not replace
  it.

## Consequences

- Exit `3` joins the **Public contract** (DESIGN §9 / [ADR-0010](./0010-versioning-public-contract-and-ga.md)).
  It is **additive** — no existing code changes meaning, and callers that
  treat "non-zero = failure" uniformly are unaffected. DESIGN §9, the
  `internal/exit` comment table, and `gplay help exit-codes` must list it.
- The `requires` array and the admin-conferring marking become part of the
  `--output json` contract for these commands.
- The pattern is **reusable**: any future high-blast-radius write (a
  monetization surface, an app-deletion path) inherits the same tiers, a
  `--grant-*` named flag, and exit-`3` semantics — so callers learn it
  once.
- `--confirm`, `--grant-admin`, `--clear`, and exit `3` are documented in
  each affected command's `--help`, per ADR-0002's "discover the rule
  where you need it."
