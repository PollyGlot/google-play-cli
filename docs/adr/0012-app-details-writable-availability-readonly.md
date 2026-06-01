# App details is the writable app-global surface; country availability is read-only and track-keyed

## Status

accepted

## Context

PRD #113 set out to give gplay "complete info about an app" by covering
two `edits` sub-resources presented side by side: `edits.details`
(default language + contact email/phone/website) and
`edits.countryavailability` (the countries an app is distributed to).
The PRD assumed both were app-global and both editable, and floated
`gplay apps details` **and** `gplay apps availability` living together
under `apps`.

Checking the Developer API reference against that assumption broke it:

- **`edits.details`** exposes `get` + `patch` + `update` — read **and**
  write. Its four fields (`defaultLanguage`, `contactEmail`,
  `contactPhone`, `contactWebsite`) are keyed by **app**.
- **`edits.countryavailability`** exposes **only `get`** — no `insert`,
  `patch`, or `update`. And its `get` requires a **`track`** parameter,
  returning a `TrackCountryAvailability` (`syncWithProduction`,
  `restOfWorld`, `countries[]`). It is **read-only** and keyed by
  **track**, not by app. (Setting availability is a Play Console gesture
  with no Developer API equivalent.)

This contradicted both the PRD ("édition de ces champs") and the
two-axis routing rule recorded in ADR-0011 and `CONTEXT.md`, which
filed country availability under `apps` as an app-global editable
surface. The Play Console *presents* availability as an app-level
setting, so the wrong mental model is the natural one — exactly the kind
of trap that needs recording.

## Decision

1. **Split the PRD by what the API actually allows.** App details and
   country availability are not siblings; they go to different
   namespaces with different capabilities.

2. **App details → `apps`, read + write.** `gplay apps details` (bare
   noun) reads the full `edits.details` record; `gplay apps details set`
   writes it field-by-field via `patch`. This is the one writable
   app-global surface gplay exposes. It stays distinct from `apps info`,
   the cross-resource identity card (package + title + default language,
   where `title` comes from a Listing, not from details) — different
   altitudes, deliberately (the `kubectl get`/`describe` pattern).

3. **Country availability → `tracks`, read-only.** It surfaces as a read
   under `tracks` (e.g. `gplay tracks availability --package <P> --track
   <T>`). gplay does **not** synthesize an app-global, editable
   availability surface — no aggregating the four standard tracks into a
   fake "app availability", no client-side emulation of a writer the API
   does not provide. We surface the resource at its real grain (per
   track) and at its real capability (read-only), and stop.

4. **The routing rule gains a third axis.** ADR-0011's "is it keyed by
   locale?" two-way test (`metadata` vs `apps`) becomes a three-way test
   on the **key axis** of any store-presence surface: keyed by locale →
   Store front → `metadata`; keyed by app → App details → `apps`; keyed
   by track → Country availability → `tracks`.

5. **No `[experimental]` label and no contested verbs.** The write verb
   `set` is already gplay's convention (`testers set`, `tracks set`); the
   read is the bare noun, asserting no verb at all. Nothing new is
   frozen that the verb-vocabulary audit (#98) could be forced to rename,
   so these commands ship as stable preview surface. `set` is a partial
   patch at flag granularity — omitted flag leaves the field untouched,
   an explicit empty value clears it — consistent with the missing-vs-
   empty rule of ADR-0011.

## Consequences

- ADR-0011's parenthetical filing `edits.countryavailability` under
  `apps` is superseded by this ADR; its two-axis test is widened to the
  three-axis test in item 4.
- `gplay apps details` reads a **single** endpoint, so its `--output
  json` is a clean `edits.details.get` pass-through — unlike `apps info`,
  it needs no ADR-0003 exception.
- The command names (`apps details`, `apps details set`, and the future
  `tracks availability`) fall under the Public contract (ADR-0010) once
  GA: stable, changeable only with a major bump.
- A user who wants to *change* where an app is available is pointed at
  the Play Console; gplay reports the state but will not pretend to set
  it until (if ever) the API grows a writer.
