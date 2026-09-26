# Getting help

`gplay` is maintained by a single person: please pick the right channel so
questions and bugs don't get crossed. Everything goes through GitHub Issues,
each kind with its own form; the repository has no Discussions.

## Quick reference

| Situation | Channel |
|---|---|
| "How do I…?", usage question, design feedback before a concrete proposal | [Ask a question](https://github.com/PollyGlot/google-play-cli/issues/new?template=question.yml) |
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

## Why a separate question form?

Questions are welcome in Issues, through their own form rather than as a bug
or a feature request, because:

1. The `question` label keeps them searchable on their own, so future readers
   find the answer.
2. Issues labelled `bug` or `enhancement` feed milestone planning; a question
   fits neither until it turns into one, and then it is relabelled.

## Response expectations

This project is a side effort. Best-effort response time:

- Security reports: within 72 hours.
- Bugs with clear reproduction: within 1 week.
- Feature requests / questions: when it makes sense.

Pull requests almost always get a faster response than issues. If you can
fix what bothers you, that's the most welcome contribution.
