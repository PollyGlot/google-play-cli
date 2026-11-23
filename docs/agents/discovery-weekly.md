# Weekly Discovery brief

**This file is the prompt of the weekly Discovery task**, a fixed-time t3code
task on the maintainer's always-on machine, Mondays 07:00 Paris time. One run,
one thread: it starts the refresh, waits for it, triages what it produced and
gets the verdict refuted, so the maintainer reads a single report.

GitHub fires the `discovery-watch` cron hours late, which is why this task
dispatches the workflow itself. The cron and the webhook task stay as the
fallback when this machine is down: a schema/surface refresh they produce is
triaged by the webhook task, and this run only checks that verdict.

Work in a fresh git worktree of the google-play-cli checkout, created from
`origin/main`. Repository: `PollyGlot/google-play-cli` (pass `-R` to every
`gh` call).

## 1. Start the refresh and wait for it

```bash
gh workflow run discovery-watch.yml -R PollyGlot/google-play-cli -f notify=false
gh run list -R PollyGlot/google-play-cli -w discovery-watch.yml -e workflow_dispatch -L 1 \
  --json databaseId,status,createdAt,url
```

The run you want is the one created after your dispatch; if the list does not
show it yet, list again a few seconds later. `notify=false` keeps the workflow
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
  labelled `discovery:schema` or `discovery:surface`.

A failed run is an anomaly; still do step 3, an older refresh PR may be waiting.

## 3. Triage every refresh PR without a verdict

```bash
gh pr list -R PollyGlot/google-play-cli --state open --head chore/discovery-refresh \
  --json number,labels --jq '.[] | {number, labels: [.labels[].name]}'
```

For each PR labelled `discovery:schema` or `discovery:surface` that carries
neither `discovery:verdict-merge` nor `discovery:needs-decision`, follow
[discovery-triage.md](discovery-triage.md) to the letter, in this thread, with
that PR number as its input. Its hard limits are yours. Its step 7 starts the
two refutation seats (Codex and Fable) as child tasks of this thread: wait for
both before posting.

A PR that already carries a verdict label was triaged by the fallback webhook
task. Read its verdict comment (the one starting with
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
