# `--pretty` JSON output

gplay has no `--pretty` (indented) mode for `--output json`.

## Why this is out of scope

`--output json` mirrors the API response verbatim
([ADR-0003](../docs/adr/0003-json-passthrough.md)): its job is to be a
stable machine contract for CI, agents and downstream consumers. A second,
reformatted JSON shape adds a flag to every command and a second thing to keep
byte-stable, for a purely cosmetic gain that a pipe already gives:

```bash
gplay tracks list --output json | jq .
```

Human readers already have the default table/text output.

## Prior requests

- #466: "parking: deferred ideas: startup perf, --pretty, WinGet"
