# Discovery snapshots

Offline, version-pinned snapshots of Google API **Discovery documents** — the
canonical machine-readable schema for each REST API gplay speaks. They let
agents and maintainers answer *"does this method exist / what's its request
shape?"* from a local file, with **no network and no WebFetch** of Google's HTML
reference.

## Files

| File | What it is |
|------|-----------|
| `androidpublisher_v3.json` | Pinned, normalized snapshot of the Android Publisher v3 Discovery doc. |
| `playdeveloperreporting_v1beta1.json` | Pinned, normalized snapshot of the Play Developer Reporting v1beta1 Discovery doc — the read-only crashes/ANR vitals service (#49), a distinct host and OAuth scope. |
| `gamesConfiguration_v1configuration.json` | Pinned, normalized snapshot of the Play Games Services Publishing (`gamesConfiguration`) v1configuration Discovery doc — achievement & leaderboard configuration (#241), a distinct host. |
| `playcustomapp_v1.json` | Pinned, normalized snapshot of the Play Custom App Publishing (`playcustomapp`) v1 Discovery doc — managed Google Play private-app creation (#242), a distinct host. |
| `paths.txt` | Derived existence-check index — one `id⇥method⇥path` line per API method, across **all** snapshotted services (ids carry their service prefix). |

**Generated — do not hand-edit.** Both files are produced by
`make discovery-update`; an offline integrity test (`go test ./...`) fails if
either is hand-edited or left stale.

## Query, never read whole

These are `jq`/`grep` targets, **not** prose to load into context. Reading the
whole snapshot wastes tokens — query it:

```bash
# Does a method exist? (token-frugal: grep the small index)
grep '^androidpublisher.edits.tracks.update' docs/discovery/paths.txt

# What's a method's request schema?
jq '.resources.edits.resources.tracks.methods.update.request' \
  docs/discovery/androidpublisher_v3.json

# List every method id
cut -f1 docs/discovery/paths.txt
```

## Regenerating

```bash
make discovery-update   # fetches upstream, normalizes, re-derives paths.txt
```

The tool prints each snapshot's `revision` — cite it in the regen commit
message. Snapshots are normalized (sorted keys, `etag` stripped) so each regen
produces a minimal, reviewable diff.

- **Sources:**
  - `https://androidpublisher.googleapis.com/$discovery/rest?version=v3`
  - `https://playdeveloperreporting.googleapis.com/$discovery/rest?version=v1beta1`
  - `https://gamesconfiguration.googleapis.com/$discovery/rest?version=v1configuration`
  - `https://playcustomapp.googleapis.com/$discovery/rest?version=v1`
- **Last synced:** `androidpublisher_v3` revision `20260613`; `playdeveloperreporting_v1beta1` revision `20260611`; `gamesConfiguration_v1configuration` revision `20260604`; `playcustomapp_v1` revision `20260618`

Freshness is **not** a per-PR gate — upstream drift is normal and must never
block an unrelated PR. A human runs `make discovery-update` on demand. See
[#52](https://github.com/PollyGlot/google-play-cli/issues/52).

## What breaks when a method disappears

`internal/apiregistry` declares every API method a shipped `gplay` command
calls, mapped to that command. Its offline test anchors each entry to the three
artefacts above (the embedded Schema index, `paths.txt`, the snapshots), so a
refresh that **removes** or **deprecates** a method gplay depends on turns
`go test ./...` red, naming both the method id and the command that breaks,
before anyone reads the diff. Ship a command that calls a new method, add its
line to the registry: the ✅ rows of [COVERAGE.md](../COVERAGE.md) are
cross-checked against it.

## Triaging a refresh PR

[docs/agents/discovery-triage.md](../agents/discovery-triage.md) is the brief the
Discovery triage routine runs on a `discovery:schema` or `discovery:surface`
refresh PR (PRD [#501](https://github.com/PollyGlot/google-play-cli/issues/501)):
registry test first, four-basket verdict, COVERAGE.md tracking, then one label.
Read it before triaging a refresh by hand too.
