# Games Services configuration surface: application-ID axis, draft CRUD (`gamesConfiguration`)

## Status

accepted

## Context

[ADR-0026](./0026-maximal-admin-api-coverage.md) puts every Play admin API in
scope. The Play Games Services **Publishing** API (`gamesConfiguration`)
configures a game's achievements and leaderboards — an admin surface, distinct
from the Games **runtime** APIs (sign-in, score submission) which stay out of
scope by nature. PRD [#241](https://github.com/PollyGlot/google-play-cli/issues/241).

A Discovery snapshot (`gamesConfiguration_v1configuration.json`) was added during
this prep (declared in `discovery.Services`, regenerated with
`make discovery-update`). Querying it corrected three draft assumptions:

- Two resources, each with `list`, `get`, `insert`, `update`, `delete`:
  `achievementConfigurations` and `leaderboardConfigurations`.
- Methods keyed by `applications/{applicationId}` (list/insert) or the resource
  id (get/update/delete), where `applicationId` is the **Play Games Services
  application ID** — a numeric ID in its own namespace, not the Android package.
- Each resource carries a `draft` and a `published` detail — but there is **no
  publish method**.
- There is **no `imageConfigurations`** resource — the draft's icon-upload
  surface is not exposed by the current API.

## Decision

1. **`gplay games` namespace**, with `games achievements` and `games
   leaderboards` sub-namespaces.
2. **Addressing by Play Games application ID** (`--application-id`, numeric) for
   list/create; the resource id for view/update/delete. A distinct axis from the
   Android package — Games resources live in their own ID space. `CONTEXT.md`
   records the axis.
3. **Canonical CRUD verbs** ([ADR-0019](./0019-canonical-verb-vocabulary.md),
   verb-gate): `list`, `view` (get), `create` (insert), `update`, `delete`.
   `delete` is destructive → `--confirm` (exit 3 if missing,
   [ADR-0017](./0017-write-safety-and-agent-resolvable-refusals.md)); the others
   are routine, with `--dry-run` as the rehearsal.
4. **Draft-only model.** Writes affect the editable `draft`; `published` is
   read-only live state. The API has **no publish method** — publishing to
   players is Console-only and therefore out of scope. No mapping onto gplay's
   [Edit](../../CONTEXT.md) lifecycle (there is no edit/commit here).
5. **Localization inline; bulk sweep deferred.** Localized name/description are
   part of the draft payload (declarative, per resource). The bulk localization
   sweep (add a locale across every achievement/leaderboard at once) is a
   higher-order workflow, deferred (recorded in `docs/BACKLOG.md`).
6. **No icon/image upload** — not exposed by the API; dropped from the draft's
   scope.
7. **`[experimental]` first** ([ADR-0010](./0010-versioning-public-contract-and-ga.md));
   companion skill `gplay-games-management` ([ADR-0021](./0021-companion-skills-repo.md)).

## Considered options

- **Package-axis addressing.** Rejected: the API is keyed by the Play Games
  application ID, a different identifier space from the Android package; forcing
  a package mapping would be lossy and wrong.
- **Map draft→published onto the Edit lifecycle.** Rejected: there is no
  publish/commit method; `published` is read-only and publishing is Console-only
  — an Edit metaphor would imply a commit that does not exist.
- **Include icon upload (`imageConfigurations`).** Rejected: the resource and its
  methods are absent from the current Discovery doc — the draft assumed a surface
  the API does not expose.
- **Bulk localization in v1.** Rejected: a per-resource declarative payload ships
  first; the cross-resource sweep is a separate, larger workflow.

## Consequences

- `games` joins the experimental surface; `--confirm` / exit 3 are documented in
  the delete verbs' `--help`.
- `CONTEXT.md` term **Play Games Services Publishing API** is corrected (drop
  images; add the application-ID axis + the draft-only / no-publish model).
- Three slices:
  [#286](https://github.com/PollyGlot/google-play-cli/issues/286) (achievements
  read — skeleton),
  [#287](https://github.com/PollyGlot/google-play-cli/issues/287) (achievements
  write), and
  [#288](https://github.com/PollyGlot/google-play-cli/issues/288) (leaderboards
  CRUD); #287 and #288 are blocked by #286.
- `gamesConfiguration_v1configuration.json` is now a committed Discovery
  snapshot; `gplay schema` indexes its methods.
- The bulk localization sweep is tracked in `docs/BACKLOG.md` as a deferred
  follow-up.
