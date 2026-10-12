# An API no service account can call is excluded by nature

## Status

accepted, amends [ADR-0026](./0026-maximal-admin-api-coverage.md) and
[ADR-0047](./0047-coverage-dispositions-redundant-and-parked.md)

## Context

`docs/COVERAGE.md` only knows the APIs gplay snapshots under
`docs/discovery/`. The Play Games Services **Management** API
(`gamesManagement` v1management, 18 methods: achievement, event and score
resets, plus `players.hide`, `players.unhide` and `applications.listHidden`)
had no snapshot, so a reader could not tell whether gplay forgot it or decided
against it (#692).

It is a developer-facing API, but Google only accepts a token for an
authenticated **tester player** or a **Play Console user account**: its single
OAuth scope, `https://www.googleapis.com/auth/games`, is user-delegated, and
the Discovery document says of 12 methods (the `ForAllPlayers` resets and the
hidden-player methods) that they are "only available to user accounts for your
developer console", and of the other 6 (per-player resets) that they act on
"the currently authenticated player" and are "only accessible to whitelisted
tester accounts". gplay
authenticates with service accounts only ([`docs/DESIGN.md`](../DESIGN.md)
§1), so no gplay command could ever call these methods.

ADR-0026 and ADR-0047 reserve "excluded by nature" for *runtime* APIs. The
Management API is not runtime, yet it is just as unreachable.

## Decision

1. **A second ground for exclusion by nature: no service account can call
   the method.** An exclusion is legitimate when the method is runtime
   (ADR-0026) or when Google accepts only a user-delegated token for it. The
   doctrine stays narrow: an admin method a service account *can* call is
   never excluded; it is called, redundant, parked or uncovered.
2. **Snapshot what gplay excludes.** `gamesManagement` v1management joins
   `discovery.Services`, so `make discovery-update` refreshes it and the weekly
   Discovery watch (PRD #501) sees it with no extra list to maintain. Its 18
   methods are listed in `internal/apiregistry/exclusions.go` with one shared
   reason naming the auth model, and render as ⚫️ in `docs/COVERAGE.md`.
3. **No kind field.** The reason string carries the ground (`runtime:` or
   `user auth:`); ADR-0047's rejection of a kind field on `Exclusion` stands.

## Consequences

- `docs/COVERAGE.md` gains a fifth service section: 199 methods, 33 excluded,
  0 uncovered.
- The embedded Schema index carries the `gamesManagement` shapes, so
  `gplay schema` answers questions about an API gplay will not wrap. That is
  the point of snapshotting it: the exclusion is visible and machine-checked.
- Supporting user OAuth (an installed-app flow) would reopen this decision;
  it is not on the table.
