# Bulk localization for Games achievements and leaderboards

gplay has no command that translates every achievement or leaderboard of a
game in one sweep.

## Why this is out of scope

The sweep composes from commands that already ship: `list` gives the ids,
`view --output json` gives the full body (verbatim,
[ADR-0003](../docs/adr/0003-json-passthrough.md)), and `set --file -` sends it
back. A dedicated command would only fix one translation source format and one
merge policy, which a shell loop leaves to the caller.

`set` is a full replace of the draft, so the loop edits the fetched body rather
than sending a single field flag (`--name --locale` alone would replace the
rest of the configuration). With your translations in `fr-FR.json` as
`{"<achievementId>": "<name>"}`:

```bash
APP=123456789012
gplay games achievements list --application-id "$APP" --output json \
  | jq -r '.items[].id' \
  | while read -r id; do
      name=$(jq -r --arg id "$id" '.[$id] // empty' fr-FR.json)
      [ -n "$name" ] || continue
      gplay games achievements view "$id" --output json \
        | jq --arg n "$name" '.draft.name.translations |=
            (map(select(.locale != "fr-FR")) + [{locale: "fr-FR", value: $n}])' \
        | gplay games achievements set "$id" --file - --dry-run
    done
```

Drop `--dry-run` once the previewed bodies look right. The same loop works for
`gplay games leaderboards list|view|set`; page with `--page-size` and
`--page-token` when a game has more configurations than one page holds.

## Prior requests

- #522: "Parking: bulk localization sweep for Games Services achievements and
  leaderboards"
