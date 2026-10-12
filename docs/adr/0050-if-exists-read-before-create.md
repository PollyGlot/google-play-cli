# `--if-exists`: read before create, only where identity is caller-supplied

## Status

accepted

## Context

A CI job or an agent that provisions Play resources is often re-run: a later
step failed, the workflow was retried, the agent lost track of what it already
did. Every create was fail-on-exists, so a re-run of `gplay tracks create
beta-qa` surfaced the raw API error and the caller had to wrap each create in
its own "list, then decide" logic, or swallow errors it could not tell apart
from real conflicts ([PRD #687](https://github.com/PollyGlot/google-play-cli/issues/687)).

Two facts constrain the answer:

- The API does not document what a create returns when the resource already
  exists. A duplicate track surfaces as a generic 4xx from `edits.tracks.create`,
  a duplicate member as a 409 from `users.create`, and neither code is specific
  to "already exists": a 400 is any malformed request, a 409 any state conflict.
- Transport idempotency (`internal/apiregistry/idempotency.go`) is about
  replaying a request whose outcome is unknown. It says nothing about a
  resource that already exists, and a create is never replayed.

## Decision

1. **One flag, one enum.** `--if-exists skip|update|fail`, registered through
   `internal/ifexists` so every adopter gets the same name, values, parse-time
   validation and help text. Each command offers a subset; an unoffered or
   unknown value is CLI misuse (exit 2) before auth and before any HTTP, on a
   `--dry-run` too.
2. **`fail` is the default and is exactly the behaviour before the flag.** It
   adds no request. The change is additive for every existing script.
3. **Read before create.** Under `skip` or `update` the command reads the
   resource first, with the read the command already trusts (a GET, or the
   existing list lookup). Only a 404 (or "not in the list") means absent; any
   other read failure stops the command before the create. Existence is never
   inferred from a create-time 400 or 409.
4. **Outcomes.** Absent: the create runs as before. Present + `skip`: no write,
   exit 0, stdout carries the read response verbatim under `--output json`
   ([ADR-0003](./0003-json-passthrough.md)), stderr carries one line saying it was skipped (never a `✓`:
   nothing was committed). Present + `update`: the command's existing update
   path runs, with its own gates, and stdout carries that response. Present +
   `fail`: the API error, unchanged. No new exit code, no new diagnostic code.
5. **Edits.** For a resource inside an Edit the read runs in the same Edit as
   the create, so a resource staged earlier in an explicit Edit counts as
   existing. A skip in an implicit Edit discards the Edit instead of committing
   it (`edits.ErrNothingToCommit`); in an explicit Edit it adds nothing.
6. **`--dry-run` stays offline.** The preview reports a non-default policy but
   does not resolve existence.
7. **Scope rule for adopters.** A create may offer `--if-exists` only when the
   caller supplies the resource's identity (a track name, a user email) **and**
   that identity has a reliable read. Creates whose ID the server generates
   (device tiers, recovery actions, games achievements and leaderboards, custom
   apps) do not qualify: there is no natural key, and matching by display name
   is ambiguous. `update` is offered only where the command family already has
   an update path; it is never invented for the flag.
8. **Per-command sets at introduction.** `tracks create` offers `skip|fail`: a
   track's settings are its releases, owned by the release verbs, so there is
   nothing for `update` to apply. `team users add` offers `skip|update|fail`,
   `update` reusing the `team users set` write and its `--grant-admin` gate.
9. **Experimental flag on frozen leaves.** The flag is classified with
   `kernel.ExperimentalFlag` ([ADR-0042](./0042-one-zero-ga-and-stability-label-mechanism.md)):
   the leaves stay frozen Public, the value set may still grow.

## Consequences

- A re-run of an `--if-exists skip` create converges with one extra read and
  the same output shape whether the resource was created or found.
- The read and the create are not atomic. A writer that creates the resource
  between them makes the create fail exactly as it does without the flag; the
  flag never hides a real conflict and gplay does not retry.
- A new adopter must satisfy decision 7 and pick its allowed set explicitly;
  the shared helper refuses everything else at parse time.
