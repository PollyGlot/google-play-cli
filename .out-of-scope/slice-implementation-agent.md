# Slice implementation agent

No automation turns a `ready-for-agent` slice into a pull request. A slice is
picked up by the maintainer, who runs an agent locally and opens the PR.

## Why this is out of scope

The Discovery triage half of PRD #501 runs unattended and earns its keep: it
merges `revision-only` refreshes and triages the rest. The implementation half
(a webhook-triggered routine opening draft PRs) was built and dry-run, then
left switched off by choice: the maintainer reviews every code change anyway,
and starting the agent locally costs less attention than reviewing PRs nobody
asked for at that moment. A dormant workflow, gate script and brief were
maintenance with no use, so they were removed.

The removed files are in the history of the commit that deleted them (search
the log for `slice-agent.yml`).

## Prior requests

- #507: "Ops: webhook-triggered routine opens a draft PR on ready-for-agent slices"
