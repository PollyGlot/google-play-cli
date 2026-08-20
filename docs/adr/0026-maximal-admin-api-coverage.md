# ADR-0026 — Maximal coverage of Play admin APIs

Date: 2026-06-13
Status: accepted

## Context

Until now the scope policy was demand-driven: every non-MVP API surface sat in
`docs/BACKLOG.md` with a "why later" note, and most entries said *"only if a
concrete case asks for it"*. That policy was designed for a world where each
command had to be **found** by a human browsing `--help` — obscure surfaces
cost more confusion than they earned.

Two things changed that calculus:

1. **Agents removed the discoverability tax.** gplay's primary consumers
   increasingly are AI agents driven through the companion skills
   ([ADR-0021](0021-companion-skills-repo.md)). Nobody has to find a command;
   they ask in natural language and the skill knows it. A command "nobody can
   find" no longer exists.
2. **JSON pass-through made every endpoint a data source.**
   ([ADR-0003](0003-json-passthrough.md)) Any wrapped read endpoint feeds
   custom dashboards (local pages, Cowork, cron + alerting) at near-zero
   marginal design cost. Surfaces we dismissed as "dashboard material the Play
   Console already shows" are exactly what users want to pull into *their own*
   dashboards.

The approach is known to scale: quasi-exhaustive coverage of a
~1,200-endpoint API fits in a single Go binary driven by an offline spec
snapshot, with risk managed by stability labels instead of a gatekeeping
backlog. That is the architecture gplay already has (Discovery snapshots,
schema index).

## Decision

**If a Google Play *admin* API exposes it, gplay wraps it.**

An **admin API** is one a developer (or their agent/CI) calls *about* their
app: configuration, publication, distribution, reporting, team. The scope-in
test is *"is this an operation of the developer on their app?"* — not *"does
someone need it today?"*.

In scope (full coverage, in priority order):

1. **Android Publisher API** (`androidpublisher`) — complete the remaining
   surface: deobfuscation files, internal app sharing, app recovery, device
   tier configs, expansion files, monetization, orders.
2. **Play Developer Reporting API** (`playdeveloperreporting`) — vitals
   (crash/ANR rates, errors, slow start/rendering, wakeups/wakelocks),
   anomalies, release filters.
3. **Play Games Services Publishing API** (`gamesConfiguration`) —
   achievements and leaderboards configuration.
4. **Play Custom App Publishing API** (`playcustomapp`) — private enterprise
   app creation/distribution (managed Google Play).

Out of scope by **nature**, not by priority:

- **Play Integrity API** — a *runtime* API: the developer's backend calls it
  per-request with an ephemeral token generated on-device. No terminal or
  agent session ever holds that token; a wrapper would be structurally
  unusable and would mislead skills into proposing it.
- **Real-time purchase verification** (`purchases.products.get`,
  `purchases.subscriptionsv2.get` as a serving path) — same runtime nature.
  Ad-hoc *debugging* reads of a purchase token may still ship later as an
  explicitly-framed diagnostic, but they are not part of the coverage sweep.

## Consequences

- `docs/BACKLOG.md` changes meaning: from a gatekeeping registry ("out of
  scope until a case shows up") to an **ordering** registry ("in scope, not
  yet scheduled"). Only nature-excluded runtime APIs remain out of scope.
- Risk moves from *scope control* to *stability labels*
  ([ADR-0010](0010-versioning-public-contract-and-ga.md)): new surfaces ship
  `[experimental]` first and graduate into the Public contract, instead of
  waiting at the gate.
- Each newly adopted Google service gets its **own Discovery/spec snapshot**
  under `docs/discovery/` (never conflated, per the Discovery snapshot term in
  `CONTEXT.md`) and, when shipped, its own companion skill
  ([ADR-0021](0021-companion-skills-repo.md)).
- The per-command rituals stay fully in force — canonical terms in
  `CONTEXT.md`, DESIGN conventions, RoundTripper-tested, verb-gate
  ([ADR-0019](0019-canonical-verb-vocabulary.md)). Maximal coverage changes
  *what* enters, not *how*.
- Positioning: gplay can claim **100% coverage of what Google's admin APIs
  allow** — a stronger story than a command count, and the honest counterpoint
  to App Store Connect's much larger (≈1,200-endpoint) single API.
