# Canonical verb vocabulary: `list`/`view`/`status`, `set`, `create` vs `add`/`remove`, in three verb categories

## Status

accepted

## Context

gplay grew one namespace at a time, and the verb for a given gesture was
chosen locally each time. By the time the MVP surface was complete, the same
gesture carried different verbs in different namespaces:

- **Inspect one resource** was `apps info` but `tracks status` — two verbs for
  one gesture — plus two *verb-less* reads (`apps details`, `tracks
  availability`) where the read had no verb at all.
- **Delete** was `remove` (`apps remove`, `team users remove`) but `revoke`
  (`team grants revoke`).
- **Create/register** was `add` (`apps add`, `team users add`) and `create`
  (`tracks create`) with no stated rule for which applies.

[ADR-0010](0010-versioning-public-contract-and-ga.md) names this as a debt to
pay before any surface freeze: command names are part of the Public contract,
so a rename after the freeze is a breaking change. ADR-0010's 1.0 freeze is
currently dormant (we ship `0.x` rolling), but the **companion skills repo**
(#53) is the live trigger: a published `SKILL.md` that drives `gplay tracks
status` is as expensive to rename as a frozen contract once agents depend on
it. So the vocabulary is settled now, before the v1 skills pin to it.

We compared the two closest analogues and two reference CLIs:

| | read N | read 1 | session/health | write | create |
|---|---|---|---|---|---|
| asc (sibling) | `list` | `view` | — | `edit` | `create` |
| gh | `list` | `view` | `status` (`gh auth status`) | `edit` | `create` |
| kubectl | `get` | `describe` | — | `edit`/`set` | `create` |
| docker | `ps` | `inspect` | — | — | `create` |

The deciding lens is **agent legibility**: gplay's v1 story is agent-first +
CI + manual. A human tolerates synonyms; an agent's tool-selection error rate
rises with every synonym for one gesture. Consistency is worth more to the
machine reader than to the human one.

## Decision

### 1. Three verb categories

Every command verb belongs to exactly one of three families:

1. **CRUD grammar** — the generic resource verbs, held to strict
   consistency: `list`, `view`, `status`, `create`, `add`/`remove`, `set`.
2. **Domain verbs** — each names a real gesture no generic verb captures:
   `upload`, `promote`, `rollout`, `halt`, `resume`, `complete`, `reply`,
   `pull`, `apply`, `validate`, `login`/`logout`. Admission test: it must
   describe a domain gesture that `set`/`create`/`view` could not state
   honestly (`upload` ≠ create, `promote` ≠ set, `reply` ≠ create). This test
   keeps the category from becoming a catch-all for exceptions.
3. **Reference / diagnostic / scaffold** — meta-commands outside the resource
   grammar: `version`, `exit-codes`, `doctor`, `team permissions` (offline
   catalog), `init`.

### 2. CRUD grammar rules

- **Read N → `list`.** (Already universal; confirmed.)
- **Read one addressed resource → `view`.** Unifies the old `info`/`status`
  split; every resource read takes an explicit verb (no verb-less reads).
- **Session / health state, no addressed resource → `status`.** Reserved for
  exactly this; the only member is `auth status` (the `gh auth status` idiom).
- **Bring a new object into existence → `create`.** Versus:
- **Manage membership of an existing entity in a collection → `add`/`remove`.**
  Test: "am I making the object *exist*?" An app already exists on Play and
  gplay cannot create it (no API) — it *registers* it locally, so `apps add`.
  A user already exists — they are *added* to the org roster. A closed track
  does not exist until made — `tracks create`.
- **Delete / remove from a collection / clear a field → `remove`.** One delete
  verb. `revoke` is dropped: the access domain's pair is `grant`/`revoke`, but
  gplay's write verb is the generic `set` (not `grant`), so a lone `revoke`
  next to `set` was the inconsistency.
- **Write / declare state with explicit values, non-interactively → `set`.**
  Deliberately **not** asc/gh's `edit`. Three reasons: (a) gplay's writes are
  declarative/idempotent upserts (full-replace or deterministic patch), which
  is `set` semantics, not the imperative/incremental connotation of `edit`;
  (b) gplay is no-interactive-prompts/agent-first, while `edit` connotes an
  `$EDITOR` flow; (c) **`Edit` is already gplay's central domain noun** (the
  Google transactional unit; `gplay edits begin/commit/discard` is reserved in
  CONTEXT.md) — reusing `edit` as a write verb would collide with it. asc has
  no `Edit` concept and could not see reason (c).

Universal domain pairs are preserved even though they overlap CRUD meaning:
`auth login`/`logout` stays (it deletes the credential, but `login`/`logout`
is the universal auth idiom).

### 3. Renames (hard rename, no deprecation aliases)

| Before | After |
|---|---|
| `apps info` | `apps view` |
| `tracks status` | `tracks view` |
| `team grants revoke` | `team grants remove` |
| `apps details` (verb-less read) | `apps details view` |
| `tracks availability` (verb-less read) | `tracks availability view` |

`apps details` and `tracks availability` stop being commands-that-act and
become pure grouping nouns: `apps details` now holds `view` + `set`; bare
`apps details` prints help.

No `DEPRECATED:` shadow commands. There are no users yet (public preview,
`0.x`); the handful who may exist update with the library. The ADR-0010
deprecation mechanism stands available for any future rename once the surface
has dependents, but is not used here.

### 4. Non-changes (confirmed correct under the rules)

`list` everywhere; `auth status`; `apps add`/`remove`, `team users
add`/`remove`, `tracks create`; `set` on all writes; `auth login`/`logout`;
the flat `releases` rollout cluster (`rollout`/`halt`/`resume`/`complete` are
domain verbs acting on a release's rollout *state*, not CRUD on a "rollout"
resource — no nesting); all reference/diagnostic commands.

## Consequences

- The v1 skills (#53) are written against the post-rename names from the
  start; none is born on a condemned name.
- A new namespace no longer re-litigates "info or status?", "add or create?",
  "remove or revoke?" — the rules decide. Future surfaces (subscriptions, iap,
  vitals) inherit `view`/`list`/`set` for free.
- `docs/DESIGN.md` gains a "Verb vocabulary" section (the normative table);
  CONTEXT.md prose referencing old names (`apps info`, bare `apps details`,
  `tracks status`) is corrected to match the binary.
- A latent follow-up surfaced and is parked, not decided here: `team users`
  has no `view`, and the Play API has no `users.get` (only `users.list`), so a
  `team users view <email>` would be a client-side list+filter — its own PRD.

## Considered options

- **Align the write verb on asc/gh `edit`** (the `view`/`edit` pair). Rejected
  for the three reasons in §2, the decisive one being the `Edit` domain-noun
  collision unique to gplay.
- **One uniform read verb including `auth view`.** Rejected: `auth status` has
  no addressed resource; folding it into `view` loses the precise "is my
  session healthy" meaning that `status` (per gh/fly) carries.
- **Uniformize create/delete** (`apps create`/`delete`). Rejected: gplay
  cannot create or delete an app on Play; `add`/`remove` honestly names
  local-registry membership, `create` would lie.
- **Nest the rollout cluster** (`releases rollout halt/resume/complete`).
  Rejected: there is no "rollout" resource (it is a release *state*), the four
  are flat sibling domain verbs, and these are hot-path CI commands where the
  extra level only adds friction.
- **Keep verb-less reads** (`apps details`, `tracks availability`). Rejected:
  an agent applying "read → `view`" would type `apps details view` and hit
  "unknown command" — the exception defeats the rule it lives under.
- **Ship `DEPRECATED:` aliases for the renames.** Rejected for now: no users
  to protect in `0.x`; aliases would be dead weight. The mechanism remains for
  later renames with real dependents.
