---
title: API surfaces & coverage
description: "The six Google Play developer APIs, what gplay covers today, and the coverage policy: every admin API in full, runtime and user-only APIs never."
sidebar:
  order: 8
---

"The Google Play API" is actually **several** Google services. Knowing which
one owns a capability tells you whether gplay covers it today, will cover it,
or never will.

## The six APIs

| API | Service | What it owns | gplay |
|---|---|---|---|
| Android Publisher | `androidpublisher` | Publication (listings, images, releases, tracks, testers), monetization, reviews, compliance, team | **Core: covered**, long tail planned |
| Play Developer Reporting | `playdeveloperreporting` | Post-launch observability: crash/ANR rates, errors, slow start/rendering, wakeups/wakelocks | Covered (`vitals`) |
| Play Games Services Publishing | `gamesConfiguration` | Achievements & leaderboards configuration for games | Covered (`games`, `[experimental]`) |
| Play Custom App Publishing | `playcustomapp` | Private enterprise apps via managed Google Play, the only API that can *create* an app | Covered (`customapps`, `[experimental]`) |
| Play Games Services Management | `gamesManagement` | Achievement, event and score resets for testers, hidden players | **Never** (see below) |
| Play Integrity | `playintegrity` | Runtime device/binary verification | **Never** (see below) |

A few things have **no API at all** (public app creation, content rating,
store listing experiments) and stay manual in the Play Console. Where gplay
stops because Google's API stops, that limit is documented, not silent.

## The coverage policy

gplay's rule is simple: **if a Play *admin* API exposes it, gplay wraps it.**

An *admin* API is one you (or your agent, or your CI) call **about** your app:
configuration, publication, distribution, reporting, team. With agent
skills, no command is "too obscure to find": you ask in natural language. And
because `--output json` is verbatim API pass-through, every wrapped endpoint
doubles as a data source for your own dashboards and automations.

The only exclusions are by **nature**, not priority. The first kind is
*runtime* APIs, the ones your app or backend calls while serving end users. Play Integrity is the
canonical case: its tokens are generated on-device and consumed server-side
within minutes, so no terminal session could ever hold one. A CLI wrapper
would be structurally unusable.

The second kind is excluded by its **auth model**: an API no service account
can call. The Play Games Services Management API only accepts a tester player or a
Play Console user account, through a user-delegated OAuth scope, and gplay
authenticates with service accounts alone. Its methods are still listed, as
excluded, in the [coverage table](https://github.com/PollyGlot/google-play-cli/blob/main/docs/COVERAGE.md),
so you can tell a decision from an oversight.

New surfaces ship with the `[experimental]` stability label first and
graduate into the public contract once their shape settles.

## Sizing it against App Store Connect

Apple exposes one giant API (≈1,200 endpoints) covering everything from
signing to Game Center. Google splits the same ground across the smaller
services above; Android Publisher alone is ~137 methods. The honest
comparison is not endpoint counts but coverage: gplay targets **100% of what
Google's admin APIs allow**.
