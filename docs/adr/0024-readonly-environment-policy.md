# `GPLAY_READONLY` environment policy and exit code 4

## Status

accepted

## Context

gplay's write-safety primitives — `--confirm` for destructive acts,
`--grant-admin` for conferring admin, named `--complete`/`--staged` for the
production path ([ADR-0002](./0002-safe-production-defaults.md),
[ADR-0017](./0017-write-safety-and-agent-resolvable-refusals.md)) — are
acknowledgment flags. They are exactly the right tool when a *human* drives the
CLI. For an **AI agent that holds the credential**, they are advisory: the agent
can pass `--confirm` itself. The only real authority boundary today is the Play
Console permission set of the service account.

A platform engineer operating an agent needs a boundary that lives in the
**harness**, not in the model's judgment: a way to hand an agent a production
credential for *observing and planning* while guaranteeing it cannot mutate the
store, no matter what flags it chooses.

## Decision

Add `GPLAY_READONLY`, read at kernel boot:

1. **Truthy = enforced.** `1`/`true`/`yes`/`on` (case-insensitive) enable the
   policy; unset / `0` / `false` leave writes allowed.
2. **Mutating commands are refused** when the policy is on — *before* credential
   resolution and *before* any network I/O — regardless of the flags passed.
   The refusal happens in `kernel.Run`, after the local config/format resolution
   but before the command's business function (where the credential bytes are
   lazily loaded and the network is reached).
3. **A single registration-time annotation** declares which commands mutate:
   `kernel.MarkMutating(cmd)` sets one cobra annotation; `kernel.IsMutating`
   reads it. There are **no ad-hoc per-command readonly checks**. A
   completeness guard test in `cmd/gplay` pins the exact mutating set, so a new
   write command that forgets the annotation fails CI.
4. **A dedicated exit code: `4`** — "denied by environment policy; not
   resolvable by adding a flag." It is deliberately distinct from exit `3`
   (safety flag required): exit 3 means *re-run with the named flag*, which is
   the opposite of what a read-only refusal wants an automated caller to do.
   Conflating them would invite an agent to "resolve" the refusal by passing
   `--confirm` — exactly the bypass the policy exists to prevent. The message
   names `GPLAY_READONLY`.
5. **Reads and `--dry-run` still run.** Read commands are never marked; a
   `--dry-run` of a mutating command never writes, so it is exempt. Dashboards
   and agents keep working with a production credential.
6. **Scope: Google Play mutations.** Local-only operations — credential
   management (`auth login`/`logout`) and the local app registry — are not Play
   writes and are not gated; blocking `auth login` would also prevent *setting
   up* a read-only deployment.
7. **Integrates with the error envelope.** Under `--output json` the refusal is
   emitted as the standard error envelope (exit code 4) on stdout
   ([ADR-0023](./0023-json-error-envelope.md)).

The new exit code is surfaced in `gplay help exit-codes` (built from
`internal/exit.Catalog`, so it cannot drift) and the DESIGN §9 table. The policy
touches the public contract ([ADR-0010](./0010-versioning-public-contract-and-ga.md)),
hence this ADR.

## Consequences

- A harness can enforce read-only access with one environment variable,
  independent of the model's flag choices — the authority boundary moves from
  "the agent's judgment" to "the environment."
- `exitCode 4` enters the public contract. A wrapper can now distinguish
  "denied by policy" (4 — change the environment or stop) from "add the named
  flag and retry" (3).
- Every future write command carries one obligation: call `kernel.MarkMutating`
  at its registration site (CONTRIBUTING documents it; the guard test enforces
  it).
- Out of scope, natural follow-ups once this lands:
  - **Fine-grained allowlists** (`GPLAY_ALLOW=<ns.verb,...>`) — selectively
    re-enable specific writes under the policy.
  - Marking review text as untrusted / recommending readonly mode in the
    companion agent skills (tracked in that repo).

## Alternatives considered

- **Reuse exit 3 for the refusal.** Rejected: exit 3 is the "resolvable by a
  flag" signal; a policy refusal is precisely *not* that, and reusing it would
  teach an agent to bypass the policy.
- **Per-command `if readonly { refuse }` checks.** Rejected: scatters the policy
  across every write business function, easy to forget, impossible to audit. One
  annotation + one enforcement point + one guard test is the whole surface.
- **Refuse based on the presence of `--confirm`/`--grant-admin`.** Rejected: a
  mutating command without a safety flag (routine `users add`) would slip
  through, and the boundary would still be flag-shaped — the thing we are trying
  to move out of the model's hands.
- **Enforce via Play Console permissions only (a read-only service account).**
  Still recommended and complementary (see the CI guide's least-privilege
  matrix), but it requires minting and managing a separate credential;
  `GPLAY_READONLY` is a zero-setup switch over whatever credential is already in
  hand.
