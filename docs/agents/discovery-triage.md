# Discovery triage brief

**This file is the prompt of the Discovery triage routine** (PRD
[#501](https://github.com/PollyGlot/google-play-cli/issues/501)). Every Monday
the `discovery-watch` workflow regenerates the offline Discovery snapshots and
refreshes the rolling PR `chore/discovery-refresh`. A `revision-only` refresh
merges itself. A refresh labelled `discovery:schema` or `discovery:surface`
wakes you up with one input: **the PR number**.

Everything you need is in this repository. Do not look for a private skill, a
local note or an external document: if it is not in the repo or reachable with
`gh`, it does not exist for this run.

Tools assumed available: `gh`, `go`, `jq`, `grep`. Vocabulary: use the terms of
[CONTEXT.md](../../CONTEXT.md) verbatim (Edit, Account, Project, Listing, track,
release). Scope decisions cite
[ADR-0026](../adr/0026-maximal-admin-api-coverage.md).

---

## 0. Hard limits (read before anything else)

You may not, under any circumstance:

- **merge anything** (the merge is CI's job, driven by the label you set);
- **open a code PR**, or push any commit other than the COVERAGE.md commit
  described in step 5;
- **touch any file other than `docs/COVERAGE.md`** on the refresh branch;
- **create more than 2 issues** in a single run;
- **create an issue for a method id already named in an open issue** (search
  first, step 6);
- **re-run `make discovery-update`** or regenerate any snapshot: the diff under
  review is the workflow's output, you audit it, you do not reproduce it.

If a rule above forces you to stop short of a verdict, that is not a failure:
post the verdict you have, label `discovery:needs-decision`, and say why.

## 1. Load the PR, never the snapshots

```bash
PR=<number>                       # the only input you receive
gh pr view "$PR" --repo PollyGlot/google-play-cli --json number,title,body,labels,headRefName,files
gh pr diff "$PR" --repo PollyGlot/google-play-cli -- docs/discovery/paths.txt
```

The label tells you which shape you are in:

| Label                | What moved                                                     |
| -------------------- | -------------------------------------------------------------- |
| `discovery:surface`  | Methods were added or removed (`paths.txt` changed)             |
| `discovery:schema`   | Query params, fields or descriptions moved, surface unchanged   |

**Query, never read whole.** The snapshots are ~640 KB of JSON. Reading one
into context is a defect, not a shortcut. Read the *diff*, and resolve details
with targeted queries:

```bash
# the surface delta, both directions
gh pr diff "$PR" --repo PollyGlot/google-play-cli -- docs/discovery/paths.txt | grep '^[+-][a-z]'

# what one method looks like now
jq '.resources.edits.resources.tracks.methods.update' docs/discovery/androidpublisher_v3.json

# does a method still exist at all?
grep '^androidpublisher.edits.tracks.update' docs/discovery/paths.txt
```

Cap the schema diff you actually read: if it exceeds a few hundred lines, group
it by schema name (`grep -E '^\+ +"[A-Za-z]+": \{'`) and query the interesting
ones, rather than paging through it.

## 2. Hard evidence first: the registry test

Before you form any opinion, run the deterministic check and **quote its result
as the first line of your verdict**:

```bash
git fetch origin "$(gh pr view "$PR" --repo PollyGlot/google-play-cli --json headRefName -q .headRefName)"
git checkout FETCH_HEAD
go test ./internal/apiregistry/
```

`internal/apiregistry` declares every API method a shipped `gplay` command
calls, mapped to that command. Its test anchors each entry to the refreshed
artefacts, so it answers, with no model involved, the only question that can
break users:

- `TestRegisteredMethodsExistInSchemaIndex` / `…ExistInPathsIndex` — a method a
  shipped command calls **disappeared**;
- `TestRegisteredMethodsAreNotDeprecated` — a method a shipped command calls is
  now **deprecated**;
- `TestCoverageShippedRowsAreRegistered` — a ✅ row of COVERAGE.md has no
  registry entry.

Rules:

- **Test red ⇒ the fix basket is non-empty**, whatever the diff looks like, and
  the failure message (it names both the method id and the command) is a fix
  item on its own. A red test also means the PR is **not** clear to merge:
  finish on `discovery:needs-decision`.
- **Test green ⇒ nothing gplay ships today is broken.** Say so explicitly. It
  does not clear the diff: additions are still yours to judge.
- Never paraphrase the test. Quote the failing lines verbatim.

## 3. Cross-check every changed schema against the shipped code

For each schema, field or query parameter the diff touches, find out whether the
CLI actually reads or writes it. The CLI hand-rolls its HTTP calls in
`internal/play/**` (ADR-0007), so the field name is your grep key:

```bash
grep -rn "policyResponse\|PolicyResponse" internal/play/ commands/
grep -rn "androidpublisher.appsigning" internal/ docs/COVERAGE.md
```

Three outcomes, and they map straight onto the baskets:

1. **The CLI touches it and the change alters behaviour** (a field gained a
   value, a parameter became required, an enum lost a member, a default moved):
   fix basket, with the `file:line` of the code that will now be wrong.
2. **The CLI touches it and the change is cosmetic** (description reworded,
   comment clarified): dismissed basket, with the reason.
3. **The CLI does not touch it**: noted, unless step 4 says otherwise.

Under `--output json` the CLI mirrors the API response verbatim (ADR-0003), so
a *new response field* is already surfaced and is **not** a fix. A new *request*
field, on the other hand, is a flag gplay does not offer: consider, at most.

## 4. Surface additions: apply ADR-0026

For every method id **added** to `paths.txt`:

```bash
METHOD=androidpublisher.appsigning.enrollApp
grep -n "${METHOD%.*}" docs/COVERAGE.md
grep -rn "${METHOD##*.}" internal/play/ commands/
```

Apply the ADR-0026 scope-in test verbatim: *is this an operation of the
developer on their app?* If yes, the surface is in scope and its absence from
the CLI is a **fix-basket item** (not a "consider"): under a maximal-coverage
policy, an in-scope admin method with no command is a coverage hole, and that is
precisely what makes the fix basket non-empty on a surface refresh. If the
method is a **runtime** API (called per-request by the developer's backend with
an ephemeral token, e.g. Play Integrity, real-time purchase verification), it is
out of scope by nature: dismissed basket, reason "runtime API, ADR-0026".

For every method id **removed** from `paths.txt`: the registry test of step 2
already decided. Red means a shipped command breaks (fix). Green means gplay
never called it, so it is noted, with the COVERAGE.md row that must lose its
line.

## 5. Track new methods in COVERAGE.md

Newly appeared, in-scope methods enter [COVERAGE.md](../COVERAGE.md) as 🔴
(*Untracked — in scope per ADR-0026 but no command*) in the same run, with one
commit on the refresh PR's branch:

```bash
# on the PR's head branch, COVERAGE.md and nothing else
git add docs/COVERAGE.md
git commit -m "docs(coverage): track <service>.<resource> surfaced by the refresh"
git push origin HEAD:"$HEAD_REF"
```

Constraints:

- **COVERAGE.md only.** `git status --porcelain` must show that single file
  before you commit. Anything else in the working tree is a mistake you undo,
  not a change you ship.
- Commit type is `docs`, never `feat`/`fix`: release-please reads the type
  blind to paths, and this commit must not bump the CLI version.
- Keep the headline counts of COVERAGE.md consistent with the rows you add, and
  re-run `go test ./internal/apiregistry/` after the edit
  (`TestCoverageShippedRowsAreRegistered` parses the table; a malformed row
  fails it).
- The rolling branch is force-pushed weekly. If your push is rejected because
  the branch moved, do not force-push: re-read the PR and start the run over.

## 6. When to open a ticket (`/to-spec`)

Open a spec **only if the fix basket is non-empty**. An empty fix basket means
no ticket, however interesting the "consider" basket is: those items belong in
the verdict comment and nowhere else.

Before creating anything, search by method id:

```bash
gh issue list --repo PollyGlot/google-play-cli --state open --search "appsigning.enrollApp" --json number,title
gh issue list --repo PollyGlot/google-play-cli --state open --search "in:body appsigning" --json number,title
```

A hit means the method is already tracked: link the existing issue in the
verdict and create nothing.

Then, with the fix basket as input, run `/to-spec` and publish per
[issue-tracker.md](issue-tracker.md) conventions (`type:prd` or `type:slice`,
an `area:*`, a `priority:*`, `ready-for-agent`). Sizing:

- one coherent new namespace (several methods of the same resource) → **one**
  PRD, its slices left to a later human decomposition;
- one or two isolated additive methods on an existing surface → **one** slice
  each.

**Never more than 2 issues per run.** If the diff justifies more, file the two
most valuable, name the rest in the verdict comment, and finish on
`discovery:needs-decision` so a human sizes the remainder.

## 7. The verdict comment: one per PR, updated in place

The refresh PR is rolling and is force-pushed every Monday. A new comment each
week would bury the current state, so there is **exactly one** verdict comment,
identified by a stable HTML marker as its first line:

```markdown
<!-- discovery-triage-verdict -->
```

Procedure:

```bash
# find the existing verdict comment by its marker
gh pr view "$PR" --repo PollyGlot/google-play-cli --json comments \
  -q '.comments[] | select(.body | startswith("<!-- discovery-triage-verdict -->")) | .url'
```

- Found → **edit that comment in place** (`gh api --method PATCH
  /repos/PollyGlot/google-play-cli/issues/comments/<id> -f body=@-`), replacing
  its whole body with the new verdict.
- Not found → post it once with `gh pr comment "$PR" --body-file -`.

Never post a second verdict comment. If the marker matches more than one
comment, edit the most recent and say so in the verdict.

### Format

````markdown
<!-- discovery-triage-verdict -->
## Discovery triage verdict — <surface|schema>, run <YYYY-MM-DD>

**Hard evidence.** `go test ./internal/apiregistry/` → **ok** (or: **FAIL**,
verbatim failing lines). <One sentence on what that proves.>

### To fix
- `androidpublisher.appsigning.enrollApp` — in scope under ADR-0026, no command
  covers it (`docs/COVERAGE.md:98`, no hit in `internal/play/`). → PRD #476.

### To consider
- <item> — `internal/play/vitals/vitals.go:120`. <Why it is not a fix.>

### Noted
- <item> — `docs/discovery/paths.txt:157`. <Why nothing follows.>

### Dismissed
- `PolicyResponse.description` reworded — cosmetic, no code reads the
  description (`internal/play/datasafety/datasafety.go:44`). **Reason:** prose
  change in the Discovery doc, zero behavioural surface.

**Tickets opened:** #<n> (or: none, the fix basket is empty).
**COVERAGE.md:** <commit sha> tracks <n> method(s) as 🔴 (or: unchanged).
**Verdict:** `discovery:verdict-merge` (or `discovery:needs-decision`, because …).
````

Rules on the baskets:

- **All four appear, always**, even empty (`_none_`). A missing dismissed basket
  makes "not seen" indistinguishable from "seen and judged wrong".
- **Every item cites a real `file:line`** you actually opened. A search that
  found nothing is a valid statement ("no hit for `X` in `internal/play/`"); an
  invented call site is not. Verify each citation with `grep -n` before posting.
- **Every dismissed item names its reason.** "Not relevant" is not a reason.
- Refute your own items before posting: for each fix item, try to prove it wrong
  against the real code. It survives only if confirmed and worth a change.

## 8. Finish with exactly one label

```bash
gh pr edit "$PR" --repo PollyGlot/google-play-cli \
  --remove-label discovery:needs-decision --add-label discovery:verdict-merge
```

| Label                      | When                                                                                                                                        |
| -------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| `discovery:verdict-merge`  | Registry test green, no fix item threatens a shipped command, tickets (if any) filed, COVERAGE.md updated. The snapshot refresh itself is safe: the PR touches only `docs/discovery/**`, `internal/schemaindex/schema_index.json` and your COVERAGE.md commit. |
| `discovery:needs-decision` | Registry test red; **or** the diff exceeds 2 tickets' worth of work; **or** a scope call under ADR-0026 you cannot make alone; **or** the PR touches a file outside the snapshot set; **or** you could not complete a step and said so. |

Set one, remove the other. The label is the handoff: `discovery:verdict-merge`
lets CI merge the PR, `discovery:needs-decision` notifies the maintainer. You
never merge either way.

A run that changed nothing still posts a verdict and still sets a label:
"nothing to do" and "bot dead" must not look the same.
