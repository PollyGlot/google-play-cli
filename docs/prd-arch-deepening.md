# PRD — Architecture deepening: kernel, exit codes, seams

> Source: handoff `/var/folders/qv/gptgt7l1181dq7mzq_ww68y00000gp/T/handoff-XXXXXX.md.lQbYuNy33y` (2026-05-22). Synthesises GitHub issues #32–#38 with the grill verdicts captured below. This PRD intentionally restates the **why** and the **shape**; it does NOT repeat the per-issue acceptance criteria — those live on the issues.

## Problem Statement

The `gplay` codebase has crossed the 5-command threshold (`login`, `logout`, `status`, `list`, `doctor`) and is about to cross 10 (`releases upload`, `tracks list / promote / rollout`, `reviews list / reply`, `apps add`). At this scale, three shallow patterns make every new command more expensive than it should be:

1. **`main.exitCode()` knows every error type.** Adding a command that raises a new error class (`edits.AlreadyOpenError` → 60, `tracks.InvalidRolloutError` → 60, etc.) forces an edit to `cmd/gplay/main.go`. Bad locality. Bad leverage.
2. **Five commands re-câble `config → keystore → resolver` by hand.** That's ~30 lines of identical boot code copy-pasted per command, and a `t.TempDir()` floor on every unit test because the business logic is buried under wiring.
3. **Doctor's HTTP seam lies.** `CheckScope` advertises `*http.Client` as an argument but reads from `ctx.Value(oauth2.HTTPClient)` and silently falls back to `http.DefaultTransport`. Tests learn this by reading the source.

These are not bugs — the code works. They are **architectural deepening opportunities**: each one lets us delete a class of friction that every future command would inherit.

A Go CLI that aspires to replace Fastlane and to be driven by AI agents needs a test surface that maps to business behaviour, not to wiring. Today it maps to wiring.

## Solution

Land seven targeted architectural changes (already filed as issues #32–#38), in this order, **TDD**, **one PR per issue**:

1. **#33** — `Coder` interface + `internal/exit` package. Each error type carries its own exit code. `main.exitCode()` collapses to one call.
2. **#32** — `internal/kernel` package. Single resolution of `Account / Project / Format / HTTP / FS / ctx`. Each command's business function becomes `fn(*RunContext, in) (Renderable, error)`.
3. **#35** — Thread `context.Context` + injected `FS` through `internal/config` and `internal/auth/*`. Config tests run in-memory.
4. **#34** — `Check.Run(ctx, sa, *http.Client)` becomes a real dependency contract. `scopeObserver` extracted to a public transport wrapper.
5. **#36** — Collapse `resolver.Resolver` to a pure function (variant A — see grill verdict below).
6. **#37** — Remove `sync.Once` globals in `keystore.Select`; the kernel holds the `Backend` for the invocation lifetime. `ResetSelectForTest` is deleted.
7. **#38** — **Park**. Speculative, contradicts ADR-0005, revisit at ~10 commands.

The vocabulary is **Account / Edit / Project / Format / Renderer** (CONTEXT.md). Nothing in this PRD introduces new vocabulary.

## User Stories

1. As a **CLI contributor adding `releases upload`**, I want to ship the command without editing `cmd/gplay/main.go`, so that the wiring of every new error code stays local to the package that raises it.
2. As a **CLI contributor**, I want to unit-test a command's business logic without `t.TempDir()` and without spawning a cobra root, so that my test cycle is sub-second.
3. As a **CLI contributor**, I want to know exactly which `*http.Client` a doctor check uses by reading its signature, so that I don't have to grep the source to discover a `ctx.Value` injection.
4. As an **AFK agent migrating to the kernel**, I want a deterministic place to plumb a new flag (e.g. `--account`), so that I edit one file (kernel boot) instead of five command files.
5. As an **AFK agent writing a doctor check**, I want a single `*http.Client` argument that I can replace in tests with a stub transport, so that scope-drift assertions don't need `ctx.WithValue` plumbing.
6. As a **CI user running `gplay` under heavy load**, I want the keystore probe to be constructed once per invocation and discarded with the process, so that parallel test runs don't trample a shared `sync.Once`.
7. As a **future maintainer reading `docs/DESIGN.md` §9**, I want the exit-code table to be testable inside `internal/exit`, so that the table and the code can never drift.
8. As a **CLI user pressing Ctrl-C during a slow keystore probe**, I want cancellation to propagate, so that the process exits promptly instead of stalling on a blocking syscall.
9. As an **agent reading the resolver source**, I want the credential precedence to live as a single function rather than a struct-around-an-if-chain, so that I don't have to re-read the constructor before reading the resolution logic.
10. As a **reviewer of a parallel test suite**, I want `ResetSelectForTest` to be absent from the production API surface, so that I can't accidentally call it from a non-test code path.
11. As a **user running `gplay auth status | jq`**, I want the existing behaviour preserved across the kernel migration (TTY-aware default format), so that the deepening lands without breaking ADR-0005.
12. As a **future contributor adding a new credential source** (WIF, OIDC, ADC), I want a 1-PR mechanical path from "pure function with N cases" to "provider chain with N+1 cases", so that picking variant A today does not paint us into a corner.

## Implementation Decisions

### Grill verdict — #36 (resolver collapse) → **Variant A: pure function**

`func Resolve(ctx context.Context, deps Deps, in Inputs) (*serviceaccount.ServiceAccount, error)`

Re-read of `BACKLOG.md` and ADR-0001 confirms:
- ADR-0001 explicitly rejected `gcloud` / ADC as a credential source.
- BACKLOG.md (Listings, IAP, Subscriptions v2, Vitals, Reviews CSV history, Cloud Resource Manager) introduces zero new credential providers.
- The five sources in DESIGN.md §1 form a **closed cascade**, not an open extension point.

Variant B (CredentialSource chain) would mean 5 interfaces for 5 once-only call sites with no realistic 6th. That is the textbook premature abstraction — kept on a watch list (re-evaluate when WIF / OIDC / ADC enters the backlog), not built today.

No ADR needed for this. If reviewers want a sealing decision, ADR-0006 "credential resolution is a closed cascade" can be opened later.

### Grill verdict — #32 (RunContext shape)

```go
package kernel

type RunContext struct {
    Ctx     context.Context
    Account *serviceaccount.ServiceAccount   // CONTEXT.md "Account" — type rename is follow-up
    Project string                           // package name pinned by the Project
    Format  output.Format                    // resolved (never FormatAuto)
    Stdout  io.Writer
    Stderr  io.Writer
    HTTP    *http.Client                     // pre-wired with OAuth2 + scope observer (#34)
    Log     Logger
    FS      config.FS                        // injected (#35)
}

type Boot struct {
    Args        []string
    Env         func(string) string
    Stdin       io.Reader
    Stdout      io.Writer
    Stderr      io.Writer
    FS          config.FS                            // default OSFS{}
    TTYDetect   func(*os.File) bool                  // default term.IsTerminal
    HTTPFactory func(context.Context, *serviceaccount.ServiceAccount) (*http.Client, error)
}

func Run(boot Boot, fn func(*RunContext) (output.Renderable, error)) error
```

The split is intentional: `Boot` is process-level inputs; `RunContext` is the post-resolution, ready-to-execute snapshot. Tests construct a `Boot`, capture stdout/stderr, and assert on the captured output.

`RunContext.Account` uses the CONTEXT.md term despite the underlying type still being `serviceaccount.ServiceAccount`. A pure rename pass (`serviceaccount → account`) is tracked as a **follow-up** (see Out of Scope).

### Major modules built/modified

| Module / package | Action | Issue |
| --- | --- | --- |
| `internal/exit` (new) | `Coder` interface + `For(err)` helper | #33 |
| `internal/kernel` (new) | `RunContext` + `Boot` + `Run` | #32 |
| `internal/config` | + `ctx`, + `FS` interface, + `OSFS{}` | #35 |
| `internal/auth/keystore` | + `ctx`, remove `sync.Once`, remove `ResetSelectForTest` | #35, #37 |
| `internal/auth/resolver` | Collapse to pure function | #36 |
| `internal/auth/doctor` | `Check.Run` takes `*http.Client` explicitly | #34 |
| `internal/transport` (new) | Public `WithScopeObserver` wrapper | #34 |
| `cmd/gplay/main.go` | Becomes thin: build `Boot`, call `kernel.Run` | #32 |
| `commands/auth/*` | Migrate to `fn(*RunContext, in) (Renderable, error)` | #32 |

### Architectural alignment

- **CONTEXT.md vocabulary** is non-negotiable: Account, Edit, Project, Format, Renderer.
- **ADR-0001 to 0005 are untouched.** #38 contradicts ADR-0005 and is parked.
- **DESIGN.md §1 (precedence)** is preserved verbatim — Variant A only changes the *shape* of the code that implements the precedence, not the precedence itself.
- **DESIGN.md §9 (exit codes)** is preserved verbatim — #33 makes the table testable; it does not renumber.

### Migration discipline

- **One PR per issue.** No batching.
- **TDD** on every issue. Acceptance criteria → red tests first.
- Each PR body contains `Closes #N` (per the personal global convention).
- The `ready-for-agent` label is **not** applied by the agent — the user posts it manually.

## Testing Decisions

### What makes a good test for this work

- **Anchored on behaviour, not wiring.** A test for `kernel.Run` asserts on stdout/stderr bytes and exit code, not on the type of the resolver struct.
- **In-memory, sub-second.** Config and keystore tests should run without `t.TempDir()` once #35 lands.
- **No `ctx.WithValue` hacks.** A test for `CheckScope` should construct a `*http.Client` with a stub transport, not stuff one into a context.
- **Renderer parity preserved.** Existing renderer tests (table / JSON / Markdown for each command) must keep passing through every issue.

### Modules to be tested

| Module | Test focus |
| --- | --- |
| `internal/exit` | Each known typed error maps to its documented code; unknown errors → 1. |
| `internal/kernel` | Boot resolves Format / Account / Project / FS once; business function is invoked with the resolved values; error → `exit.For` is honoured. |
| `internal/config` | All public funcs accept `ctx`; the `FS` seam is exercised by an in-memory fake. |
| `internal/auth/keystore` | No global state survives a test; `Select` returns a `Backend` that lives until disposed. |
| `internal/auth/resolver` | Precedence cases enumerated as table tests (one row per §1 cascade step + a "nothing resolves → exit 10" row). LOC ratio against prod < 2×. |
| `internal/auth/doctor` | Each `Check.Run` takes a real `*http.Client`; scope drift is observable via a transport wrapper, not via `ctx.Value`. |
| `internal/transport` | `WithScopeObserver` round-trips and records the OAuth2 `scope` form field. |

### Prior art

- `internal/output/output_test.go` already exercises a small "build a fake `*os.File` to fake a TTY" pattern — reuse it for the kernel's TTY detector seam.
- `commands/auth/doctor/doctor_test.go` already wires stub `http.Client` via context — its callers become the migration target for #34.

## Out of Scope

- **#38 (payload-driven Renderer interface).** Speculative, contradicts ADR-0005. Reopen at ~10 commands, not before.
- **Rename `internal/auth/serviceaccount` → `internal/auth/account`.** Mechanical but noisy; track as a separate cleanup after the kernel migration is merged. The kernel's `RunContext.Account` field name is forward-compatible.
- **Feature work** — `releases upload`, `tracks`, `reviews`, `vitals`, `metadata`. Those are issues #6–#24, a different problem.
- **`internal/output` changes** beyond the addition of the `Renderable` interface required by #32.
- **`internal/walkup`** stays untouched (deep module per the architecture review).
- **ADR 0001–0005** stay untouched.
- **A new ADR for Variant A (#36).** Document as a code comment + this PRD; raise an ADR only if a reviewer pushes back.

## Further Notes

### Sequence of work (ratified by the user in the source handoff)

```
#33 (Coder)  ──┐
               ├──► #32 (kernel) ──┬──► #36 (resolver, absorbed) ──► #37 (keystore globals)
               │                   ├──► #35 (ctx + FS)
               │                   └──► #34 (doctor HTTP seam) ── parallelisable with #35
               │
#38 ── PARKED ─┘
```

### Known traps (verbatim from the source handoff)

- `commands/auth/login/login.go` shadows the persistent `--service-account` flag from `cmd/gplay/main.go`. Marked "intentional and harmless" today; the kernel migration is the place to delete the local flag.
- `internal/auth/keystore/keyring.go` exports `ResetSelectForTest` in production — #37 deletes it. If #32 lands before #37, the kernel migration will still call this reset; that's expected and gets cleaned up by #37.
- `oauth2.HTTPClient` injected via `ctx.Value` is *currently* the only way for doctor tests to override the transport. Migrating #34 requires adjusting the test suite **and** production wiring simultaneously.

### What this PRD does NOT do

- It does not create new GitHub issues. The seven existing issues (#32–#38) are the vertical slices. See the companion section in the handoff for the proposed *publishing* of these issues with the `ready-for-agent` label (which the user must apply manually).
- It does not modify any code. The next agent starts from `main`, branches per issue, TDD.
