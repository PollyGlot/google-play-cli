# Weekly Discovery brief

**This file is the prompt of the weekly Discovery task**, a fixed-time t3code
task on the maintainer's always-on machine, Mondays 07:00 Paris time. One run,
one thread: it starts the refresh, waits for it, triages what it produced and
gets the verdict refuted, so the maintainer reads a single report.

GitHub fires its crons hours late, which is why this task dispatches the
`discovery-watch` workflow itself. The workflow's own cron runs on Tuesdays as
the catch-up for a Monday this machine missed: a schema/surface refresh it
produces is triaged by the webhook task. After a Monday run it finds the open PR
already carrying the snapshots, pushes nothing, and wakes the webhook task only
if that PR never got a verdict.

Work in a fresh git worktree of the google-play-cli checkout, created from
`origin/main`. Repository: `PollyGlot/google-play-cli` (pass `-R` to every
`gh` call).

## 1. Start the refresh and wait for it

First the kill switch: if `gh variable get DISCOVERY_TRIAGE_ENABLED -R
PollyGlot/google-play-cli` does not print `true`, the maintainer has paused the
triage. Still do steps 1 and 2, skip step 3, and report the pause.

```bash
gh workflow run discovery-watch.yml -R PollyGlot/google-play-cli -f notify=false
gh run list -R PollyGlot/google-play-cli -w discovery-watch.yml -e workflow_dispatch -L 5 \
  --json databaseId,status,createdAt,url
```

Note the UTC time one minute before the dispatch (clocks drift). The run you want is the earliest
`workflow_dispatch` run created after it (a replay started
meanwhile comes later); if none is listed yet, list again a few seconds later. `notify=false` keeps the workflow
from firing the webhook task: you triage the PR here. Wait for the run with
`gh run watch <id> -R PollyGlot/google-play-cli --exit-status` as a background
command you wait on, not a sleep loop.

A dispatch that fails, or a run still unfinished after 30 minutes, ends the
run: report it and stop.

## 2. Read the outcome

`gh run view <id> -R PollyGlot/google-play-cli` and its job summary give one of:

- **no change**: Google's documents match the snapshots;
- **revision-only**: the workflow merged the rolling PR itself (or refused, which
  is an anomaly);
- **schema or surface**: the rolling PR `chore/discovery-refresh` is open and
  labelled `discovery:schema` or `discovery:surface`, with this run's commit;
- **already open with these snapshots**: the PR is open from an earlier run and
  Google has not moved since; nothing was pushed.

A failed run is an anomaly; still do step 3, an older refresh PR may be waiting.

## 3. Triage every refresh PR without a verdict

```bash
gh pr list -R PollyGlot/google-play-cli --state open --head chore/discovery-refresh \
  --json number,labels --jq '.[] | {number, labels: [.labels[].name]}'
```

Triage the PR when this run refreshed it (step 2 said schema or surface),
whatever label it carries: an old `discovery:needs-decision` describes last
week's commit, not this one. Triage it too when it is labelled
`discovery:schema` or `discovery:surface` and carries neither
`discovery:verdict-merge` nor `discovery:needs-decision`. To triage, follow
[discovery-triage.md](discovery-triage.md) to the letter, in this thread, with
that PR number as its input. Its hard limits are yours. Its step 7 starts the
two refutation seats (Codex and Fable) as child tasks of this thread: wait for
both before posting.

A PR this run did not refresh that already carries a verdict label was triaged
earlier, by a previous Monday or by the fallback webhook task. Read its verdict comment (the one starting with
`<!-- discovery-triage-verdict -->`): a `**Refutation:**` line that does not
name both seats as having run is an anomaly, and so is a verdict label without
that comment. Do not triage it again.

## 4. Limits of this run

- Everything [discovery-triage.md](discovery-triage.md) forbids stays forbidden:
  no merge, no code PR, at most 2 issues.
- No other dispatch than step 1. The slice agent workflow is retired (#747):
  a `ready-for-agent` slice opened by the triage waits for the maintainer.

## 5. Final message

In French, exactly this Markdown shape, nothing before or after, no em dash.
Link every PR, issue and run (`https://github.com/PollyGlot/google-play-cli/...`).
✅ when a line needs nothing, ⚠️ when it needs a look, ❌ for a failure.

```markdown
**Discovery, <jour JJ mois>**

| | Résultat |
|---|---|
| Refresh | <emoji> run [#id](url) : <pas de changement / revision-only fusionnée / PR [#n](url) schema|surface> |
| Verdict | <emoji> <`discovery:verdict-merge` / `discovery:needs-decision` (raison en une phrase) / sans objet> |
| Réfutation | <emoji> Codex <n> déplacé(s), Fable <n> déplacé(s) / sans objet> |
| Tickets | <emoji> <liens, ou "aucun"> |
| Anomalies | <emoji> <une phrase courte chacune, ou "rien"> |
```

When the verdict is `discovery:needs-decision`, add one line under the table
with the decision the maintainer has to make.
