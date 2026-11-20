# Artifact preflight: hand-rolled manifest readers, refusal at exit 20

## Status

accepted

amended (2026-11-20): implementation decisions moved from code comments

## Context

gplay uploaded whatever file it was handed. A wrong path (an APK where an
AAB is expected, a mislabeled zip, a build for another application) was only
rejected by Google after the bytes were sent. With resumable uploads on
150 MB-plus artifacts, that is minutes of wasted transfer, a burned
[Edit](../../GLOSSARY.md), and a `400` whose message is far less specific
than a local check could be. The worst case is not slow, it is wrong: nothing
stopped app A's release from receiving app B's build.

Every fact needed to catch this is inside the file. An AAB and an APK are both
zip containers with distinguishing members, and both carry an
`AndroidManifest.xml` that declares the package name: as an aapt2 protobuf
message in the AAB, as Android binary XML in the APK. Both formats are stable
and documented in AOSP.

Reading them, however, has a cost. The obvious routes are linking
`google.golang.org/api/androidpublisher` (which does not parse artifacts
anyway), vendoring a protobuf runtime plus aapt2's generated `Resources.pb`
types, or shelling out to `aapt2`. The first two contradict
[ADR-0007](./0007-raw-http-not-google-go-sdk.md) and grow the dependency
surface of a binary whose whole distribution promise is one static file; the third makes
a CI job's success depend on an Android SDK gplay does not ship.

The second question was which exit code a refusal carries.
[PRD #448](https://github.com/PollyGlot/google-play-cli/issues/448) proposed
the exit-2 family, reading exit 20 as "the API rejected the artifact".

## Decision

**Preflight before the first byte.** `internal/artifact` exposes two calls,
`Inspect` (what is this file?) and `Preflight` / `Verify` (does it match what
the command promised?). Every upload surface calls it before opening an Edit,
before reserving a resumable session, and before authenticating where the
order allows. It is silent on success: the happy path's output is unchanged.

**Classification by structure, never by extension.** `BundleConfig.pb` or
`base/manifest/AndroidManifest.xml` means an AAB; a root `AndroidManifest.xml`
means an APK; anything else, including a non-zip, is `unknown`, which is a
legitimate expectation (not a failure) on the surface that uploads `.obb`
expansion files.

**Two hand-rolled readers, no new dependency.** A minimal protobuf field
walker for the aapt2 manifest, and a bounds-checked binary-XML chunk walker
for the APK one. Neither recurses, both are fuzzed, and every decompression is
capped (member count, per-member size, total budget) so a crafted artifact
cannot zip-bomb the check that exists to make uploads fast to fail.

**Refusal is exit 20, not exit 2.** `docs/DESIGN.md` §9 defines 20 as
client-side validation and names "malformed AAB" as its example: a preflight
refusal is exactly that, and by construction the API was never reached. The
sibling checks these commands already ran (`cannot tell APK from AAB by
extension`, `not a regular file`) exit 20 too, so this keeps one code for
"gplay refused your artifact locally" rather than splitting it in two. Exit 2
stays what an automated caller needs it to mean: you typed the command wrong.

**A parser gap never becomes a false refusal.** Whenever gplay cannot answer,
it says so and steps aside instead of concluding. A recognized container whose
manifest will not parse skips the package check; a container past the member
cap is `indeterminate`, a state of its own, and skips the container check too.
Both write a `NOTE:` to stderr and let the upload proceed. The distinction is
load-bearing in both directions: an asset-rich AAB is over the cap and calling
it `unknown` would refuse a legitimate bundle, while on the expansion-file
surface `unknown` is the expectation, so the same conflation would silently
vouch for a file nothing checked.

`--skip-preflight` lifts the container and package checks. It does not lift the
local-file check ("missing", "not a regular file"): those surfaces refused
those paths at exit 20 before this ADR, and an escape hatch that turned a
rehearsal on a never-built artifact into a success would be a regression, not a
restoration.

## Consequences

- A wrong artifact path fails in milliseconds, offline, with a message naming
  expected and found, enough for an agent to self-correct without scraping.
- gplay now owns two parsers for formats it does not produce. They are
  bounded, fuzzed, and read-only, and their failure mode is deliberately
  "shrug and continue", so the blast radius of a gap is a missing check rather
  than a blocked release.
- Test suites for the upload surfaces must build real (if minimal) containers;
  `internal/artifacttest` does that in-test, so no binary fixture enters the
  repo.
- Signature verification, `versionCode` comparisons, and minSdk policy stay
  out of scope: this ADR covers identity and container, not policy.

## Implementation notes

### Order and output

- **The container check runs before the package check**: "you passed an APK
  to a bundle upload" is the more actionable message when both would fail.
- **`--dry-run` names what the preflight found even on a match.** A preview
  that stays silent tells the user nothing about the check it just ran; the
  live path stays silent on success.

### Bounds

- **The member cap bounds the classification walk only.** `zip.OpenReader`
  has already read the whole central directory by then; the cap keeps a
  crafted archive of millions of tiny entries from turning classification
  into the slow path the preflight exists to avoid.
- **The per-member cap is sized for a manifest**: a real
  `AndroidManifest.xml` is tens of kilobytes. A member is read to the cap plus
  one byte, so an entry that lies about its uncompressed size is caught by the
  bytes actually read, not by the header.
- **The string pools have their own decode budget.** The member cap bounds
  the manifest's size, not what its pools decode to: every index may alias
  one long entry, each index decodes its own copy, and a document may carry
  many pools. Past the budget the preflight degrades to a note, as for any
  manifest it cannot parse.
- **A length the protobuf manifest, a zip member or a pool index declares is
  compared as `uint64`** before any conversion, so a huge value cannot wrap
  into a plausible `int`. The binary-XML reader converts first and relies on
  its `< 0` and `<= 0` guards instead, which must stay. An index
  array whose declared count cannot fit in the chunk is refused before it
  drives an allocation, and a chunk size of zero, negative or past the
  document stops the walk.

### Reading the formats

- **The AAB markers are checked before the APK ones.** An AAB has no root
  `AndroidManifest.xml`, so the two cannot both match; the AAB pair is the
  more specific test.
- **A duplicated member name resolves to its first occurrence**, the
  classic spoofing defence.
- **Both string-pool encodings are read**: aapt2 writes modified UTF-8,
  older aapt wrote UTF-16LE, and both occur in the wild.
- **The `package` attribute carries no namespace**, in both encodings; an
  `android:`-prefixed attribute named `package` is a different attribute.
- **The aapt2 protobuf manifest is read by field number** from three
  messages of `Resources.proto`: `XmlNode` (`element` = 1), `XmlElement`
  (`name` = 3, `attribute` = 4) and `XmlAttribute` (`namespace_uri` = 1,
  `name` = 2, `value` = 3). The deprecated group wire types (3 and 4) never
  appear in aapt2 output and are refused.
- **A partial answer is still an answer.** A truncated binary-XML tail is
  clamped to the bytes present, since `<manifest>` sits near the front; a bad
  string offset costs that one string, not the pool, since only `manifest` and
  `package` must resolve.

### Fixtures

- **Fixtures store empty members and deflate the rest.** A Deflate writer per
  member made the 65k-member cap fixtures the slowest tests in the suite,
  while members with content stay deflated so the decompression limits are
  still exercised. Production code does not import `internal/artifacttest`.
