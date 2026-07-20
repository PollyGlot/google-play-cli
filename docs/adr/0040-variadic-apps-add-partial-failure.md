# Variadic `apps add`: partial-success batch with non-retryable-wins exit

## Status

accepted

## Context

`apps add` registered exactly one package per invocation
(`cobra.ExactArgs(1)`). That friction becomes visible the moment
[#347](https://github.com/PollyGlot/google-play-cli/issues/347) ships
`apps accessible list`: the natural gesture after seeing three accessible
Apps is to register the *selection*, not to run three commands. The
project already has a variadic precedent —
[ADR-0031](./0031-orders-commerce-reads-and-gated-refund.md) made
`orders view <id>...` variadic — but that is a read; `apps add` is the
first **variadic write verb** in gplay, and a write forces a design
decision a read never had to make: what happens when one element of the
batch fails?

`apps add` validates each package by default with a cheap
`edits.insert`+`delete` access probe ([ADR-0006](./0006-apps-add-validates-via-api.md)).
Variadic ⇒ N probes ⇒ N independent outcomes. On `gplay apps add a b c`
where `b` is refused (typo, or the service account is not invited on `b`),
what does the command do?

The decisive fact is the **two-permission split**. Seeing an App through
`apps accessible list` rides the Play Developer **Reporting** scope
([ADR-0027](./0027-vitals-second-service-scope-readonly.md)); adding it
rides the **androidpublisher** edits scope. They are distinct grants: a
service account can be invited on the Reporting side (the App shows up in
`accessible list`) yet not on androidpublisher (the `apps add` probe
returns 403), and vice-versa. So a batch where some packages register and
others are refused is not a rare error condition — it is the **expected**
bootstrap case. That kills all-or-nothing on the spot: refusing the whole
selection because one package's grant is missing would make the common
case unusable.

## Decision

1. **Batch of independents, partial success.** `apps add <pkg>...` is
   `cobra.MinimumNArgs(1)`. Each package is its own unit of work:
   format-validate → probe (unless `--no-verify`) → register in memory.
   The global config is saved **once** at the end, persisting exactly the
   packages whose probe succeeded. A failure on one package neither stops
   nor rolls back the others. There is no all-or-nothing mode.

2. **Sequential probes, no cap.** Probes run one after another (each with
   the full 30 s `validateTimeout`), not concurrently and with no ceiling
   on the argument count. The `>1000 → exit 2` guard on `orders view` is
   **not** an ergonomics precedent — it exists only because the
   `orders.batchget` API caps a request at 1000 ids. `apps add` has no
   such API limit: each probe is an independent `edits.insert`/`delete`
   pair, so a large selection is slow but correct, not refused.

3. **Exit code: non-retryable-wins.** Per `docs/DESIGN.md` §9, an exit
   code tells an automated caller *whether to retry*, not only what broke.
   The batch exit code is computed from the per-package failures:
   - If **any** failure is non-retryable (auth 10, authz 11, validation
     20, other 4xx 30, …), the batch exits with the **first** such code
     (in deduplicated argument order). Retrying the whole batch would just
     re-hit the permanent failure, so the agent is told to inspect, not
     loop.
   - Only if **every** failure is retryable (40 API-5xx, 50 network) does
     the batch report a retryable code, inviting a retry once the
     transient condition clears.
   - `60` (state conflict, "sometimes" retryable in §9) is treated as
     non-retryable here — surfacing it stops a blind retry loop, the safer
     default for a batch write.

4. **Report via stderr + a typed aggregate error.** `apps add` has no
   `--output` (its result is a side effect on the local registry, not an
   API body), so there is nothing for [ADR-0003](./0003-json-passthrough.md)
   to pass through. A multi-package run prints one `✓`/`✗` line per
   package to stderr plus a `N registered, M failed` tally, and — on any
   failure — returns an `aggregateError` carrying every outcome. The
   aggregate's `Error()` is a one-line summary naming the failed packages
   (the per-package detail is already on stderr, so the kernel's final
   `Error:` line does not duplicate it); its `ExitCode()` implements the
   non-retryable-wins rule above.

5. **Dedup arguments.** `apps add a a b` collapses to `a b` before any
   work: `a` is probed and registered once, and produces one `✓`/`✗`
   line. First-seen order is preserved so the exit code stays
   deterministic for a given command line.

6. **`--no-verify` stays global to the invocation.** It skips the probe
   for **every** package in the batch (offline/preparatory registration);
   it is not per-package.

7. **Single-package non-regression.** `apps add <one-package>` behaves
   **exactly** as before: same stderr line, same raw error and exit code,
   same `--no-verify` behavior, no `✗` line and no aggregate wrapper. The
   variadic machinery only engages for `len(args) > 1`. The CLI is
   consumed in production by AI agents and by the maintainer's `storedeck`
   app, so widening the arity had to be strictly additive.

## Consequences

- `apps add` is the reference for any future variadic write in gplay: the
  non-retryable-wins rule and the `✓`/`✗`-per-item + typed-aggregate
  report shape are the pattern to copy.
- An agent bootstrapping from `apps accessible list` can pipe the whole
  selection into one `apps add` and read, from the exit code alone,
  whether a retry is worth attempting or a grant needs fixing first.
- Because the exit code hides *which* packages failed (it reports a single
  representative code), the stderr `✗` lines are the authoritative
  per-package record — machine-readable enough for an agent to re-drive
  only the transient failures.

## Alternatives rejected

- **All-or-nothing (transactional batch).** Rejected: the two-permission
  split makes a partial refusal the *expected* case, so refusing the whole
  selection would make bootstrap unusable.
- **Exit with the first failure's code regardless of retryability.**
  Rejected: it would let a single transient 5xx mask a permanent 403, or a
  permanent 403 (reported first) hide the fact that the rest were merely
  transient — the retryability of the *batch* is what an agent needs.
- **A `--continue-on-error` flag guarding partial success.** Rejected:
  partial success is the only sensible default here, not an opt-in;
  all-or-nothing has no valid use case given the permission split.
