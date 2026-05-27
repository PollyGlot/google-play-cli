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

**Alt 1 (replacement angle)**
> Replace Fastlane on Android CI. One binary. No Ruby runtime. JSON in,
> JSON out.

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
your `~/.local/bin`, your runner. `go install` works. Homebrew formula
coming.

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

## What's shipped today (v0.1.0-alpha.2 — May 26, 2026)

**Auth** — full lifecycle
- `gplay auth login --service-account ./sa.json`
- `gplay auth logout / status / list / doctor`
- OS-native keystore (Keychain on macOS, libsecret on Linux, Credential
  Manager on Windows)

**Releases**
- `gplay releases upload <aab> --track <X> [--user-fraction N] [--dry-run]`
- `gplay releases promote --from <X> --to <Y>`
- `gplay releases rollout --to <fraction>`
- `gplay releases halt / resume / complete`
- `gplay releases list`

**Apps** (local registry, API-validated)
- `gplay apps add <package>` (validates against the API on add)
- `gplay apps list / info / remove`

**Foundations**
- Cascading config: global → project (`.gplay/config.json`) → local
  override → env vars → flags
- TTY-aware output dispatcher (table / json / markdown)
- Cobra-based CLI, semantic exit codes throughout

## What's coming next

- `gplay tracks list / status` (read-only)
- `gplay reviews list / reply` (with 7-day API window documented)
- Then: vitals (crashes/ANR), metadata sync, IAP, subscriptions —
  see [BACKLOG.md](../BACKLOG.md)

## What's deliberately *not* coming soon

(So we don't over-promise.)
- Custom closed-track creation
- ProGuard mappings upload
- APK legacy support
- Anything outside MVP — by design, see [ROADMAP.md](../ROADMAP.md)

---

## The vision

`gplay` is the **CLI half** of a two-repo plan. The other half:

**`google-play-cli-skills`** — a companion repo of `SKILL.md` files
that Claude Code and similar agents load to drive `gplay`
autonomously. Release flows, review triage, vitals dashboards — all
agent-orchestrable. Install with `npx skills add <user>/google-play-cli-skills`.

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
- "An earlier Go CLI that inspired this project. `gplay` aims for full
  MVP coverage plus an agent-first design."

**vs GPC (yasserstudio, TypeScript)**
- "Comprehensive coverage, requires Node. `gplay` trades coverage for a
  single binary."

---

## Status & honesty

- **v0.1.0-alpha.2.** Pre-1.0 by design. Breaking changes expected.
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
- "Replace Fastlane on Android CI. One binary."
- "The Google Play API, as one binary your CI and your agents can drive."
- "Ship Android apps from your terminal, your CI, or your agent."
