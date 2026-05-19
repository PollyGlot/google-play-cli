<!--
Thanks for the contribution!
Before opening:
  - Read AGENTS.md and docs/DESIGN.md if you're touching a CLI convention.
  - For anything bigger than a typo, an issue should exist first — link it below.
-->

## Summary

<!-- 1–3 bullet points. What changes and why. -->

-

## Linked issue

<!-- "Closes #123" / "Refs #123" — or "n/a" for trivial PRs (typos, docs). -->

## Test plan

<!--
How did you verify this works?
For CLI changes, include the actual command you ran and the relevant output:

```
$ gplay releases upload app.aab --track internal --release-notes-dir ./whatsnew
✓ uploaded versionCode 142
✓ track 'internal' updated
```

If there's no obvious way to test (refactor, docs), say so.
-->

## Checklist

- [ ] CI is green (`make format && make lint && make test`).
- [ ] Behavior change is reflected in `--help` text and (if cross-command) in [`docs/DESIGN.md`](../blob/main/docs/DESIGN.md).
- [ ] New canonical term → added to [`CONTEXT.md`](../blob/main/CONTEXT.md).
- [ ] Irreversible / surprising decision → ADR added under [`docs/adr/`](../blob/main/docs/adr/).
- [ ] Deferred work → noted in [`docs/BACKLOG.md`](../blob/main/docs/BACKLOG.md).
