# ADR-0066: The offline test harness and what it guarantees

## Status

accepted (2026-11-20)

## Context

gplay's tests never reach Google: a fake Play answers every request, a shared
key signs every token, and the command tree is exercised in process. Three
pieces carry that guarantee: `internal/testkit` (the fake transport, the wire
recorder, the request-constraint table), the contract tests of `cmd/gplay`
(the surface golden, the leaf rules, the runnable examples), and the fuzz
oracles of the parsers that read bytes gplay did not produce. The ratchets
that force tests onto the kit are [ADR-0064](./0064-architecture-ratchets.md);
the frozen-surface policy the contract tests serve is
[ADR-0042](./0042-one-zero-ga-and-stability-label-mechanism.md). These
decisions lived in code comments; the comment purge (the
`scripts/lint/comments.py` ratchet) gathers them here.

This ADR is a catalogue, one section per piece. A new guarantee, or a new
reason a test is built the way it is, gets its entry under the piece it
concerns.

## Decision

### The fake transport (`testkit.Fake`)

- **An unclaimed request fails its round trip.** The Fake terminates the
  token exchange, records every other request and serves it from the first
  responder that matches; a request no responder claims is an error, so an
  unexpected call surfaces instead of passing as an empty 200.
- **The token is served at `TokenURL` exactly.** A suffix match (any path
  ending in `/token`) would hand a bearer to a misdirected auth host, or to
  an API path that happens to end in `/token`, and the test would pass for
  the wrong reason.
- **A body that breaks a documented constraint panics.** The Fake holds no
  `testing.TB`, and a panic fails the test whatever the code under test does
  with the error, which Play's 400 would not guarantee.
  `CollectRefusals` turns the panic into a failed round trip recorded in
  `Refusals`, for tests that run whole commands and report the rule per case:
  a command that sends from a goroutine would otherwise take the test binary
  down.
- **The Fake's exchange golden renders every call in a stable text form**
  (`Exchange`, `Exchanges`, `Sequence`): method, full URL as sent (escaping
  included), every header sorted, the declared `ContentLength`, whether the
  retry transport could re-send the body, and the body. Two request builders
  that render the same text put the same bytes on the network. The pin rule
  for such a golden is in [ADR-0003](./0003-json-passthrough.md).
- **A test that asserts on the token exchange serves it itself.** The Fake
  terminates the exchange without recording it, so a test that checks the
  scope claim the exchange carries (a `vitals` command minting the reporting
  scope) answers the token hop in its own transport.

### The wire recorder (`testkit.Wire`)

- **It records requests as a server reads them off the connection**: the
  request line, every header net/http put on the wire and the body. An
  in-process `RoundTripper` never sees `Content-Length` or
  `Transfer-Encoding` as net/http frames them, and a test that pins what a
  module sends byte for byte needs both.
- **Its client dials a local TLS server whatever the URL's host**, so the
  registry's real https URLs are exercised unchanged.
- **The server moves `Transfer-Encoding` out of the header map**, so the
  recorder puts it back: the transcript shows how the body was framed. Its
  transcript holds the request line, the sorted headers and the body; it does
  not say whether the body is replayable, which only the Fake's exchange
  golden records.
- **It reuses the test server's own client**, whose `Close` also drops the
  idle connections; `internal/testkit` is a regular package, not a `_test`
  file, so it cannot lean on test-only helpers to build one.
- **A body that breaks a constraint fails the test** and receives the 400
  Play would answer.

### The request-constraint table

- **Every request body is checked against the constraints Play documents in
  prose and refuses at runtime**, which the schema alone does not express. The
  table is keyed by `Schema.property` as Discovery names them, and each rule
  quotes the Discovery sentence it encodes; an anchor test fails when Google
  rewords it, so a snapshot refresh forces a review of the check. Both the
  Fake and the Wire apply it, so a test that pins a body Play refuses fails.
  A request is matched to its method on the escaped URL path, so a
  parameter whose value holds a slash (a review id, an `applications/<pkg>`
  name) stays one segment; matching on the decoded path would silently skip
  the check for such a request.
- **An inventory keeps the table complete.** A wording matcher finds every
  Discovery description that states a rule on a request property; each match
  is either in the table or listed in the inventory with the reason it is
  not. The matcher is deliberately wider than the table: phrase lists, bare
  modals ("is only allowed to be set", "can not be updated", "will be
  rejected") and quantity wordings ("maximum length is 55 characters", "up to
  20").
- **Two description shapes state no request constraint.** The AIP prefix
  `Immutable.` marks a create-time identifier and is stripped before matching
  (an immutability rule worded in prose still matches); an `Output only.`
  field is filled by Play, which ignores it in a request, so nothing gplay
  sends there can be refused.
- **A body is matched to its schema through the method's URL template**, and
  when several templates match, the one with the most literal characters
  wins, so `{productId}` does not take a custom method such as
  `{productId}:archive`.
- **A rule reads values the way proto3 JSON does**: `null` counts as absent,
  an unset integer may arrive as `0`, and a number may arrive as a string
  ([ADR-0003](./0003-json-passthrough.md)).
- **A rule may be wider than Discovery's sentence when Play is**: the
  `countryTargeting` rule accepts a `halted` release as well as `inProgress`
  ([ADR-0055](./0055-play-api-behaviours-the-client-absorbs.md), Tracks). It
  checks what the body carries, never more: the production-track half of that
  sentence is out of reach because a rule sees the release, not its track, so
  `promote`, the one verb that moves targeting across tracks, pins it in its
  own test.
- **A rule holds the documented figure even when Play says
  "approximately"** (the 350-character reply): a test that needs a longer
  reply is one that exercises the refusal.
- **A half of a sentence that gplay's own code makes unreachable is not
  checked.** The batch rules hold the 100-element limit alone; "different
  offers" cannot be broken because `apply` keys every change by its identity.
  A patch that leaves `SubscriptionOffer.phases` out keeps the stored phases,
  so only a present list is held to one or two entries.

### Shared fixtures

- **One RSA key per test binary**, generated on first use. A 2048-bit keygen
  costs about 60 ms (170 ms under `-race`), and one per test was nearly the
  whole CPU cost of the suite. It is generated at run time rather than
  committed as a PEM, which keeps secret scanners and the redaction patterns
  quiet. No test depends on key uniqueness, and `*rsa.PrivateKey` is safe for
  concurrent signing, so parallel tests share it.
- **Store-image fixtures are grayscale, encoded at `BestSpeed`, once per
  size.** The image checks read only the header (`image.DecodeConfig`), so a
  fixture needs exact dimensions and a real format, not real pixels; a
  full-size RGBA screenshot per call made the image suites some of the
  slowest under `-race`.
- **A golden over Discovery-derived output runs on a fixture index.** The
  `schema` view goldens use a three-method slice of the Schema index, so they
  pin the view's shape, not the embedded Discovery surface (frozen by its own
  snapshot), and a snapshot refresh does not churn them. The fixture
  descriptions carry `<`, `>` and `&` because Google's verbatim text does.
- **Artifact fixtures are built in-test** by `internal/artifacttest`
  ([ADR-0046](./0046-artifact-preflight-hand-rolled-manifest-readers.md)).
- **`-update` exists only in test binaries that import `testkit`.** It
  rewrites that package's golden files; `go test ./... -update` fails in the
  packages that do not know the flag, so it is run on the packages meant to be
  refreshed.

### Contract tests (`cmd/gplay`)

- **The surface golden pins what the Public contract freezes**:
  `testdata/surface.golden`, one self-describing line per leaf, flag and exit
  code, whose first field is the element's stability, the key
  `scripts/contract-gate.sh` reads to refuse a change to a `frozen` line
  without a breaking marker or an ADR. A renamed flag, a changed default or a
  dropped shorthand on a frozen leaf passed the whole suite before it.
- **The golden leaves help prose out** (`Short`, `Long`, `Example`, flag
  usage): it is not contract, and pinning it would make every help rewrite
  read as a break. A flag renders the parts a script depends on: name,
  shorthand, type, default, required, hidden or deprecated (a hidden
  compatibility flag is still accepted, so still contract). Root flags are
  listed once under `global`, and cobra's `--help` is skipped because cobra
  adds it lazily at execution, which would make the golden depend on test
  order.
- **Its regeneration flag is `-update-contract`**, not `-update`, so it
  cannot collide with the golden helper's flag in a package the test binary
  imports.
- **`TestLeafContract` holds the rules every leaf follows** (`Long`,
  `Example`, `--output`, flag and verb vocabulary). Today's violators sit in
  ratchet allowlists that may only shrink: an entry that now passes, or that
  names nothing, fails so it gets deleted, and a ceiling pins each list's
  size, so growing one is a deliberate, reviewed edit rather than the easy way
  past a red test. A leaf's own `Long` is what it is judged on: the notice
  `kernel.Experimental` prefixes, and the `Long` it synthesises from `Short`
  when none was written, count as absent.
- **Flag names follow one concept, one name** (DESIGN §0). A pair recorded as
  drift fails by definition even when its name is in the vocabulary: that is
  a collision, the same name for another concept.
- **Every `gplay ...` line of an Example must parse** against its leaf: the
  leaf, the flags (name, type, required, mutually exclusive groups), the
  positional count, and no deprecated or hidden flag, since an Example is what
  a new user copies. A flag validated as required in `RunE` (so a missing
  value exits 2, not 1) is recognised from its usage text ("(required)", a
  trailing "Required."); a safety gate's "required when ..." is not, because
  a `--dry-run` Example rightly omits it.
- **Every write command's Examples run against the Fake** and its constraint
  table, from a fresh directory holding the files they name: a body Play
  refuses fails with the rule's name. A non-preview Example (no `--dry-run`,
  no `--plan`) must send a write of its own, or the table judged nothing;
  `edits.insert`, `edits.commit` and the discard of an implicit Edit prove no
  write of their own. An allowlist names the Examples that cannot run, each
  for a known defect, and fails on a line that now runs or no longer exists.
- **The example world is the smallest Play state a write reads before it
  writes**: one app, each track serving one staged rollout (two releases would
  make every release verb ask which one), and a developer account holding the
  members the team Examples name. `HOME` sits apart from the run directory, so
  the Project walk-up never takes `~/.gplay`, the macOS config directory, for
  a Project ([ADR-0004](./0004-cascading-config.md)).
- **Registry walks classify every runnable leaf** of the real tree: the
  mutating set ([ADR-0024](./0024-readonly-environment-policy.md)), the
  stability set ([ADR-0042](./0042-one-zero-ga-and-stability-label-mechanism.md)).
  A leaf nobody classified fails, and so does an entry that resolves to no
  leaf, so a rename cannot leave a stale line covering nothing. They resolve
  commands without executing them, so they touch neither credentials nor the
  network.
- **A credential value is never echoed** by any command: the test looks for a
  PEM marker, a canary, or any 16-byte block of the raw value (blocks aligned on 16 bytes, so a leak of
  31 bytes or more is always caught), the only check
  that catches a base64 blob no redaction pattern matches
  ([ADR-0062](./0062-credential-exposure.md)).

### Fuzz oracles

- **Every parser of bytes gplay did not produce is fuzzed** (the API error
  envelope, the artifact manifests, the report and review CSVs, the reply
  batch, the Data Safety CSV, among others). The invariant is the one the
  caller depends on: the parser returns rather than panics, and never invents
  a value a malformed input does not carry; the CSV readers also never return
  rows alongside an error.
- **A round-trip oracle skips the inputs its fixture writer cannot carry
  faithfully**, which are not defects of the parser: `encoding/csv` folds a
  quoted `\r\n` to `\n` on read, invalid UTF-8 cannot survive a UTF-16 round
  trip, a header's surrounding space and a leading BOM rune are not part of
  its text, and a name `archive/zip` refuses to write cannot build the
  fixture.

## Consequences

- A test that reaches the network is wrong by construction, and a test that
  pins a body Play refuses fails, whatever the code under test does with the
  error.
- A Discovery refresh that adds or rewords a constraint fails the inventory or
  the anchor test until the table is reviewed.
- A new leaf is bound at once by the golden, the leaf rules, the registry
  walks and, when it writes, its runnable Examples.
