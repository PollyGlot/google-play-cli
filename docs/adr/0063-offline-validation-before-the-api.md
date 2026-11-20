# ADR-0063: What gplay checks before the API, and what it leaves to Google

## Status

accepted (2026-11-20)

amended (2026-11-20): implementation decisions moved from code comments

## Context

A write command can fail on its own input in two places: locally, before the
first request, or at Google, after an Edit was opened and an artifact sent.
gplay checks some inputs offline and passes others through untouched, and the
line between the two is a decision per input. These decisions lived in code
comments; the comment purge (the `scripts/lint/comments.py` ratchet) gathers
them here.

This ADR is a catalogue. The rule comes first; the sections record where it
was applied, one per surface. Rules owned by a surface's own document stay
there: Listing limits and locales in
[ADR-0011](./0011-metadata-apply-sync-model.md), Store image rules in
[ADR-0013](./0013-image-slot-reconciliation.md), release notes in DESIGN §3.

## Decision

### The rule

- **What is knowable offline is checked before `edits.insert`.** A mistake
  caught after the Edit opened costs an Edit, an artifact upload and, for a
  multi-locale write, a half-applied attempt; caught before, it costs
  nothing. Such checks also run under `--dry-run`, so a preview cannot pass
  what the live run would refuse.
- **gplay checks the shape it owns and leaves the meaning to Google.** It
  does not keep a copy of server-side state or of a registry it cannot keep
  current; where Google's answer is clear, its error is the authority
  ([ADR-0003](./0003-json-passthrough.md)).
- **An ambiguous input is refused rather than guessed.**

### Release uploads

- **Retained versionCodes are sent unvalidated.** `--retain-version-code`
  appends codes the app already has; an unknown or inactive one fails with
  Google's own error, and gplay makes no extra read to check it first.
- **An externally hosted APK is checked for what gplay owns only** (#546):
  the file must be a JSON object, and its `packageName`, when present, must
  be the target package. The URL, hashes, certificates and versionCode are
  Google's to accept or refuse, and the bytes are sent untouched. The command
  checks the file before it loads credentials, even though the orchestrator
  checks it again, so an invalid file is refused offline (exit 20) instead of
  surfacing as a missing-Account error.
- **`--mapping` with several artifacts is refused.** A mapping deobfuscates
  one versionCode and nothing says which artifact it belongs to; `releases
  mappings upload --version-code` attaches it to the right one afterwards.

### Usage before state

- **A usage refusal runs before the pinned package is read.** `apps details
  set` with no field flag is refused before `--package` and the Project pin
  are resolved, so the misuse is reported the same way whatever the pin
  state.

### `--dry-run`

- **An offline `--dry-run` resolves neither the Account nor the explicit-Edit
  pin.** (The online previews of `metadata apply` and `images apply` read the
  API, ADR-0011, and load the credential.)
  Nothing reaches the network and the dry-run path never reuses a pinned Edit,
  so a missing Account or a corrupt pin must not fail a preview; both are
  resolved on the live path only (`releases upload`, `promote`, `rollout`,
  `releases mappings upload`). An unreadable pin is therefore refused by the live run
  (DESIGN §4), not by its rehearsal.

### Input size caps

- **A release-notes file is capped at 1 MiB.** The cap guards memory against
  a hostile or accidental huge file; it is unrelated to Play's 500-character
  limit, which is checked on the text itself (DESIGN §3).

## Consequences

- A new write command states, for each input, which side of the line it sits
  on; an input gplay cannot judge offline is passed through, not
  approximated.
- An offline check that turns out stricter than Google needs a bypass, the
  way `--skip-preflight` relaxes the image rules (ADR-0013).
