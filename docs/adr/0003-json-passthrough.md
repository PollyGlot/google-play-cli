# `--output json` is a pass-through of the Google Play Developer API response

For every command that wraps a Google Play Developer API call, `--output json` returns the API response **verbatim**, without renaming, flattening, or reshaping fields. The envelope key therefore varies per command (`{"reviews":[...]}`, `{"tracks":[...]}`, `{"bundles":[...]}`, etc.) because that is how Google's API natively shapes its responses.

Three reasons:

1. **Single source of truth for the schema.** Users and agents who want to know what a field means go to <https://developers.google.com/android-publisher/api-ref/rest>. gplay does not maintain its own JSON schema documentation.
2. **No mapping bugs.** Every rename or flattening is a place divergence with the API can sneak in. Pass-through has zero such risk.
3. **Forward-compatible by default.** When Google adds a field to a resource, it surfaces in `gplay ... --output json` immediately, without a gplay release.

The `table` output (TTY default) is **not** pass-through — we pick the columns that matter for a human reader. The `markdown` output follows the same rule as `table`. Only `json` is committed to API fidelity.

## Exceptions

- **`apps list`** has no API source (there is no `apps.list` endpoint — see the local-registry workaround). Its JSON shape is `{"apps":[...]}`, chosen to mirror Google's naming convention for consistency with the rest of the output.
- **Errors** are not API pass-through. They use a gplay-defined shape on stderr: `{"error":{"code":"<symbolic>","message":"<human>","details":{...}}}`. Wrapping the API error inside `details` preserves the upstream payload.

## Considered Options

- **Normalize to a gplay-specific shape** (e.g. `{"data":[...],"pageInfo":{...}}` envelope, snake_case fields, etc.) — rejected. We would own a schema we have no reason to invent, and every Google API evolution would require a gplay change.
- **Filter "noise" fields like `kind` and `etag`** — rejected for v1. The filter list becomes its own maintenance burden, and these fields are cheap to ignore client-side with `jq`.
