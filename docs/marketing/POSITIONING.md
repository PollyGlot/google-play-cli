# gplay — Positioning manifesto

> Canonical source of truth for how we talk about `gplay` in public.
> Every X post, Threads post, README sentence, talk pitch, and skill
> description should be downstream of this file. Update this first when
> the project's positioning shifts; everything else follows.

---

## One-liner (pick one)

**Primary (CI angle, broadest)**
> A standalone Go binary for the Google Play Developer API — built for
> CI pipelines, scripts, and AI agents.

**Alt 1 (historical angle)**
> Android CI has been running on Ruby since 2014. Here's the native Go
> binary I wish I'd had.

**Alt 2 (agent angle)**
> The Google Play Developer API, as one binary your agents can drive.

**Alt 3 (safety angle)**
> Ship Android releases without accidentally rolling out to 100%.

---

## The why

Every team shipping an Android app eventually hits the same wall:

- **Fastlane `supply`** is the de facto standard, but it drags a Ruby
  runtime, Bundler, and an opaque config model into your container CI.
  Cold start is slow. Failures are generic. Output is meant for humans.
- **Other CLIs** (Node-based, Go-based partial wrappers) either pull in
  another runtime or cover only a slice of the API.
- **AI agents** can't drive any of them well: no JSON pass-through,
  generic exit codes, no machine-parsable everything.

`gplay` exists because shipping to Google Play in 2026 should be a
single static binary with semantic exit codes and JSON output that
matches the Google Play API verbatim — nothing more, nothing less.

### The historical accident behind the status quo

The deeper reason Android CI runs on Ruby today is **not technical**.
It's an inherited choice:

1. Fastlane was built for **iOS** in 2014–15. Ruby was the natural pick
   because macOS shipped Ruby on every build agent.
2. Fastlane became the iOS standard. Teams shipping both platforms
   wanted **one tool**, so Android lanes (`supply`, `gradle`,
   `screengrab`) were bolted on.
3. By the time anyone asked "wait, why does Android CI need Ruby?",
   the answer was "because it always has."

There is no native Android-first publishing CLI in wide use. Java/Kotlin
CLIs are blocked by JVM cold start. Gradle plugins live too deep inside
the build. Python and TypeScript options exist but each carries a
runtime. The standalone-Go-binary slot has been empty.

**`gplay` is what you would build today if you started fresh, knowing
what we know now.** This is the strongest single-paragraph framing for
the project.

---

## The 5 pillars (what makes gplay different)

### 1. Standalone Go binary
One file. No Ruby. No Node. No Python. Drop it in your Docker image,
your `~/.local/bin`, your runner. `go install`, Homebrew, and the
install script all work today.

### 2. JSON is the Google API verbatim
`--output json` returns the raw Google Play Developer API response
shape. There's no custom envelope to learn, no field renaming, no
schema documentation to maintain in parallel. **The Google docs *are*
the docs.**

### 3. Semantic exit codes
Generic `exit 1` makes CI retry logic guesswork. `gplay` uses an
explicit code table:
- `10` auth missing / bad credentials
- `11` authorization (service account lacks permission)
- `20` validation (bad flag, malformed input)
- `30` Google API 4xx (your fault, don't retry)
- `40` Google API 5xx (retryable)
- `50` network / DNS (retryable)
- `60` state conflict (e.g. concurrent edit)

Your CI knows whether to bail, retry, or alert.

### 4. Safe by default on production
`gplay releases upload app.aab --track production` creates a **draft**
release. No accidental 100% rollout because someone forgot a flag. You
opt in with `--complete` or `--staged 0.05`. Other tracks behave the
"expected" way.

### 5. TTY-aware output
Run interactively → table. Pipe to `jq` → JSON. Append `--output
markdown` → docs-ready Markdown. Auto-detection on day one. No flag
required for 90% of cases.

---

## Who this is for

`gplay` speaks to four overlapping audiences. Same product, different
angle for each.

| Audience | Angle to lead with |
|---|---|
| **Android CI engineers** maintaining Fastfiles | "Drop the Ruby runtime from your Docker image." |
| **Vibe-coders / AI-driven Android devs** | "Your agent can ship apps now. JSON in, JSON out, semantic exit codes." |
| **Indie / solo Android devs** | "Safe production defaults. No accidental rollouts." |
| **OSS Android maintainers** with multiple apps | "One config file per repo. Same binary across all your projects." |

---

## What's shipped today (public preview, `0.x`)

A broad surface is live. The authoritative, always-current list is
`gplay --help` and the [CHANGELOG](../../CHANGELOG.md) — this section stays
categorical on purpose so it doesn't drift version-to-version.

- **Auth** — full lifecycle (`login / logout / status / list / doctor`),
  OS-native keystore (Keychain / libsecret / Windows Credential Manager).
- **Apps** — local registry, API-validated (`add / list / view / remove`)
  plus App details read + set (`apps details`).
- **Releases** — `upload`, `promote`, `rollout` with the staged-rollout state
  machine (`halt / resume / complete`), `list`.
- **Tracks** — `list / view`, custom closed-track `create`, country
  `availability` (read-only).
- **Reviews** — `list / reply` (documented 7-day API window).
- **Metadata** — store listings *and* images, sync model (`list / pull /
  validate / apply`).
- **Compliance** — Data Safety declaration (write-only `datasafety`).
- **Team** — Developer-account `users` and per-app `grants` + permissions.
- **Testers** — Google Groups authorized on closed tracks (`list / set`).
- **Foundations** — cascading config, TTY-aware output (table / json /
  markdown), semantic exit codes throughout.

## What's coming next

- Vitals (crashes / ANR, Reporting API) — [#49](https://github.com/PollyGlot/google-play-cli/issues/49)
- Monetization (subscriptions v2, IAP, RevenueCat sync) — post-v1,
  [#51](https://github.com/PollyGlot/google-play-cli/issues/51)
- Reviews history beyond 7 days (CSV reports) — [#94](https://github.com/PollyGlot/google-play-cli/issues/94)
- See [ROADMAP.md](../ROADMAP.md) and [BACKLOG.md](../BACKLOG.md).

## What's deliberately *not* coming soon

(So we don't over-promise.)
- APK legacy upload (existing apps only) — AAB-first by design
- OBB / expansion files, App Recovery, internal app sharing
- Anything in [BACKLOG.md](../BACKLOG.md) marked out-of-scope, by design

---

## The vision

`gplay` is the **CLI half** of a two-repo project. The other half is **live**:

**[`PollyGlot/google-play-cli-skills`](https://github.com/PollyGlot/google-play-cli-skills)**
— a companion repo of `SKILL.md` files that Claude Code and similar agents
load to drive `gplay` autonomously. Release flows, review triage, metadata
sync, compliance, team management — all agent-orchestrable today. Install with
`npx skills add PollyGlot/google-play-cli-skills`.

The long-term goal: be the canonical way to drive Google Play from
anything that isn't a browser.

---

## How we talk about alternatives

Always respectfully. The Android CI ecosystem is small and
collaborative.

**vs Fastlane `supply`**
- Scope: `supply` is one lane inside Fastlane. `gplay` is a standalone
  CLI focused only on Google Play.
- Trade-off: If you use Fastlane for iOS *and* signing *and*
  screenshots *and* publishing, keep Fastlane. If you only use it for
  Android publishing, `gplay` removes the Ruby dependency.
- Don't say: "Fastlane is bad." Say: "Fastlane is great. `gplay` is
  what you'd reach for if you don't need everything Fastlane carries."

**vs Vacxe/google-play-cli**
- An earlier **Kotlin** CLI that wraps the official Google Play Java
  library. Partial coverage. `gplay` differs by being native Go (fast
  cold start, no JVM warmup), speaking the API over raw HTTP rather
  than via the SDK (see [ADR-0007](../adr/0007-raw-http-not-google-go-sdk.md)),
  and being agent-first by design.

**vs GPC (yasserstudio, TypeScript)**
- Comprehensive coverage (all 217 API endpoints) and ships both as
  `npm install` and as a standalone binary via install script — so
  the "you need Node" line that used to apply doesn't anymore.
  `gplay` differs by being native Go (no TS-binary init overhead),
  MVP-scoped on purpose, and built with agent-first defaults
  (JSON pass-through verbatim, semantic exit codes, no interactive
  prompts) baked in from day one rather than added later.

---

## Status & honesty

- **Public preview, `0.x`.** Pre-1.0 by design. Breaking changes expected.
- The goal is to get the *surface* right before stabilizing.
- Built in public. Issues + PRs welcome.
- This is the author's first OSS project — feedback on the project
  itself (not just the code) is encouraged.

---

## Voice & tone

- **Builder-honest.** Say what works, what doesn't, what's next.
- **Technically precise.** Exit code 40 is retryable. Exit code 30 is
  not. We don't fudge.
- **Respectful to incumbents.** Fastlane is great. We're not bashing
  it.
- **No "AI slop."** No "🚀 super excited 🚀". The product speaks for
  itself.
- **Show, don't tell.** Every claim has a command you can run.

---

## One-line variants for bios

- "Standalone Go CLI for the Google Play Developer API."
- "Native Go CLI for Android publishing. One binary."
- "The Google Play API, as one binary your CI and your agents can drive."
- "Ship Android apps from your terminal, your CI, or your agent."
