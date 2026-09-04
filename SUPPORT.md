# Getting help

`gplay` is pre-1.0 and maintained by a single person — please pick the right
channel so questions and bugs don't get crossed.

## Quick reference

| Situation | Channel |
|---|---|
| "How do I…?" / general usage question | [Discussions → Q&A](https://github.com/PollyGlot/google-play-cli/discussions/categories/q-a) |
| Idea, feature proposal, design feedback | [Discussions → Ideas](https://github.com/PollyGlot/google-play-cli/discussions/categories/ideas) |
| Something that worked yesterday is broken today | [Open an issue](https://github.com/PollyGlot/google-play-cli/issues/new?template=bug_report.yml) |
| Concrete missing feature | [Open an issue](https://github.com/PollyGlot/google-play-cli/issues/new?template=feature_request.yml) (search the [parking issues](https://github.com/PollyGlot/google-play-cli/issues?q=is%3Aissue+label%3Atype%3Aparking) first) |
| Security vulnerability | **Do NOT open a public issue.** [Private advisory](https://github.com/PollyGlot/google-play-cli/security/advisories/new) |

## Before opening an issue

- **Search the [parking issues](https://github.com/PollyGlot/google-play-cli/issues?q=is%3Aissue+label%3Atype%3Aparking).** Some features are
  deferred on purpose, each with its rationale on the issue. A reaction on
  the existing issue is the right signal, please don't re-file it.
- **Run `gplay auth doctor --package <your.package>`.** The most common
  "it doesn't work" cause is a service account that wasn't invited on the
  app in Play Console. The doctor catches this and tells you what's
  missing.
- **Search existing issues.** Especially for upload / track / rollout errors.

## What about asking a question on Issues anyway?

Issues filed as questions will be politely routed to Discussions — not
because the question isn't welcome, but because:

1. Discussions are searchable and indexed separately, so future readers
   benefit.
2. Issues with a `bug` or `enhancement` label trigger label automations and
   appear in milestone planning; questions don't fit either.

## Response expectations

This project is a side effort. Best-effort response time:

- Security reports: within 72 hours.
- Bugs with clear reproduction: within 1 week.
- Feature requests / questions: when it makes sense.

Pull requests almost always get a faster response than issues. If you can
fix what bothers you, that's the most welcome contribution.
