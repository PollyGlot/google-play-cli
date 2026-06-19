# Custom apps surface: account-axis gated creation (`playcustomapp`)

## Status

accepted

## Context

[ADR-0026](./0026-maximal-admin-api-coverage.md) puts every Play admin API in
scope. The Play Custom App Publishing API (`playcustomapp`) creates a private app
distributed to one organisation through managed Google Play — notable as the
**only** API path that can create an app record (public apps are Console-only).
PRD [#242](https://github.com/PollyGlot/google-play-cli/issues/242).

A Discovery snapshot (`playcustomapp_v1.json`) was added during this prep — by
declaring the service in `internal/discovery/discovery.go` (`discovery.Services`)
and running `make discovery-update` (alongside `gamesConfiguration`,
[#241](https://github.com/PollyGlot/google-play-cli/issues/241) /
[ADR-0033](./0033-games-services-configuration-draft-crud.md)). Querying it
settled the draft's open questions: the entire current surface is **one method**,
`accounts.customApps.create` (POST, multipart media upload, request/response
`CustomApp` with `title`, `languageCode`, `packageName`, `organizations[]`),
keyed by `accounts/{account}` — the developer-account ID. There is **no read**
(`get`/`list`) and **no delete**.

## Decision

1. **`gplay customapps` namespace, developer-account axis.** Addressed by
   `accounts/{account}` ([ADR-0015](./0015-developer-account-addressing-rides-on-account.md)),
   not the package axis — custom-app creation is an account-level act, and the
   app does not yet exist to be keyed by package.
2. **Single verb `customapps create`.** A multipart upload of the AAB/APK with
   `--title`, `--default-language`, and a repeatable `--organization` for org
   targeting; `--output json` mirrors `CustomApp` verbatim
   ([ADR-0003](./0003-json-passthrough.md)). No read or delete verbs — the API
   exposes none.
3. **Write safety: `--confirm`.** Creating an app record is irreversible (no
   delete endpoint; the app permanently occupies the account) → the
   destructive/irreversible tier
   ([ADR-0017](./0017-write-safety-and-agent-resolvable-refusals.md)):
   `--confirm` is required, a missing flag is exit 3 naming it, and `CI=true`
   never auto-confirms. Draft-by-default ([ADR-0002](./0002-safe-production-defaults.md))
   has no meaning here — a custom app has no draft state.
4. **Capability + enrollment refusal.** Requires the account enrolled in managed
   Google Play and the service account holding `CAN_CREATE_MANAGED_PLAY_APPS`
   (account-level, already in the team vocab, never bundled into a Role —
   [ADR-0016](./0016-permission-vocabulary.md)). A 403 / not-enrolled is an
   agent-resolvable refusal naming the missing capability (ADR-0017).
5. **`[experimental]` first** ([ADR-0010](./0010-versioning-public-contract-and-ga.md));
   companion skill ([ADR-0021](./0021-companion-skills-repo.md)).

## Considered options

- **Package-axis addressing.** Rejected: the API is keyed by developer account,
  and the app does not yet exist to be keyed by package.
- **No safety gate (creation is additive).** Rejected: creation is irreversible
  at the account level (no delete), which is exactly the asymmetric-danger case
  the named-flag pattern exists for.
- **Synthesize read verbs from another API.** Rejected for v1: out of scope;
  `playcustomapp` has no read, and cross-API listing is a separate concern.

## Consequences

- `customapps` joins the experimental surface; `--confirm` / exit 3 are
  documented in `customapps create --help`.
- `CONTEXT.md` term **Custom app** gains the account-axis + gate + create-only
  detail.
- One slice: [#285](https://github.com/PollyGlot/google-play-cli/issues/285)
  (`customapps create`).
- `playcustomapp_v1.json` is now a committed Discovery snapshot; `gplay schema`
  indexes its single method.
