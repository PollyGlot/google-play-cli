# Threat model: gplay

## What this project does and where untrusted input enters

`gplay` is a Go CLI for the Google Play Developer API: one static binary that
CI pipelines and AI agents run with a Google service-account key to publish
Android apps, edit store listings, reply to reviews and manage subscriptions.
The key it holds can ship a build to every user of an app, so the asset worth
protecting is the credential and the integrity of what it publishes.

Untrusted input reaches the binary through:

- **Google API responses** (Play Developer API, GCS for reports). Treat them as
  attacker-influenced: a hostile or compromised endpoint, a TLS-terminating
  proxy, or attacker-controlled store data (review text, listing text written
  by another account holder, product IDs) all flow back into files and
  terminals.
- **Local files the user points at**: APK/AAB artifacts (parsed by hand in
  `internal/artifact`, APK binary XML and AAB protobuf manifests), CSV
  (`internal/reviews/batch`, `internal/compliance/datasafety`), JSON inputs
  (`--file`/`--json` on many commands, the monetization catalog, service-account
  JSON).
- **The repository the command runs in**: `.gplay/config.json`,
  `.gplay/config.local.json` and `.gplay/edit-<package>.json`, found by walking up
  from the working directory (`internal/walkup`, `internal/config`). A cloned
  repo is untrusted: it must not redirect credentials, write outside its tree,
  or run code.
- **Environment variables** `GPLAY_*` (see `docs/DESIGN.md`) and `GIT_*`.

The service-account key itself is trusted, but its `token_uri` field is
attacker-reachable when the key comes from an untrusted source (an inline
`--service-account` value from a CI variable, for example).

## Components that matter most / least

Most important, in this order:

1. **Credential confidentiality** (`internal/auth/**`, `internal/redact`,
   `internal/transport`, `internal/kernel`). Any path that sends the OAuth
   bearer token or the private key to a host other than Google, or writes them
   to stdout, stderr, a file or an error envelope without redaction.
   Particular areas: the resumable-upload session URL taken from the
   `Location` header (`internal/play/api/resumable.go`), image downloads that
   follow URLs returned by the API (`commands/metadata/images/pull`), redirects
   followed by the authenticated client, the `token_uri` of the key, and the
   keystore's plaintext fallback (`internal/auth/keystore`, files must stay
   `0600` in a `0700` directory).
2. **Path containment for writes driven by remote data**
   (`internal/pathguard` and its callers: `internal/metadata/tree`,
   `internal/metadata/imagetree`, `internal/editpin`, `internal/config`,
   `internal/auth/keystore`, `internal/releases/notes`). Also
   `internal/monetization/catalog`, which validates `productId` by hand rather
   than through pathguard, and deletes stale `*.json` files in its directory.
   Traversal, symlink races or a symlinked final component that let API data or
   a hostile repo write outside the target tree are in scope.
3. **Hand-written parsers** (`internal/artifact` binary XML and protobuf
   manifest readers, `internal/reviews/history` UTF-16 CSV decoding,
   `internal/play/api` error envelope parsing). Panics, unbounded allocation or
   decompression bombs that escape the size caps (`maxManifestBytes`,
   `maxTotalDecompressedBytes`, `MaxAPISuccessBodyRead`, the 64 MiB GCS cap).
4. **Subprocess execution of git** (`internal/gitenv`, `internal/config/gittracked.go`,
   `commands/installskills`). Code execution through a hostile repo's git
   config, hooks, fsmonitor or environment is in scope. `install-skills` fetches
   a pinned commit and verifies it; a way to install files other than the
   pinned ones is in scope.
5. **Terminal injection**: control characters or ANSI escapes from API data
   reaching a terminal in the human formats (`internal/output/sanitize.go`).
   `--output json` mirrors the API verbatim by design and is not a finding.
6. **Write safety and read-only mode**: a bypass of the `GPLAY_READONLY`
   policy, or of the `production` default to `draft` (ADR-0002), that
   makes a command mutate a live app the user did not ask to mutate.

Lower priority but in scope:

- `install.sh` (checksum verification, fail-closed) and the Cloudflare Worker
  in `deploy/gplay.sh/` that serves `/install` from a tag-validated GitHub URL.

Out of scope:

- The Google Play Developer API and GCS themselves; upstream Go modules (report
  upstream).
- `website/` content (static documentation), `docs/`, `scripts/` used only by
  the maintainer, `internal/discovery` (developer tooling that refreshes the
  API snapshot), and anything under `*_test.go` or `internal/testkit`.
- `.github/workflows/`: the repository's CI is not part of the shipped binary.
- Attacks that need the attacker to already control the user's account, home
  directory, `PATH`, or the service-account key file. A user who passes
  `--dest /etc/passwd` gets what they asked for.
- Denial of service against the local CLI (a slow or huge response that makes
  one invocation hang or fail) unless it bypasses a documented size cap.

## How to exercise it

- `go test ./...` runs the whole suite offline; `go test -race ./...` is what CI
  requires. Tests use `internal/testkit` (a fake Play transport and a shared RSA
  key) injected as `&http.Client{Transport: ...}`. Follow that pattern for a
  reproducer: no test may reach the network.
- Fuzz targets (run with `go test -run=^$ -fuzz=<Name> ./<pkg>`):
  `internal/play/api` FuzzParseErrorEnvelope;
  `internal/transport` FuzzScopesFromAssertion, FuzzParseRetryAfter;
  `internal/reviews/batch` FuzzParse;
  `internal/reviews/history` FuzzHistoryParse;
  `internal/artifact` FuzzParseBinaryXMLPackage, FuzzParseProtoManifestPackage;
  `internal/redact` FuzzRedactString;
  `internal/output` FuzzSanitizeCell;
  `internal/compliance/datasafety` FuzzDataSafetyValidate.
- The binary is built with debug info at `/usr/local/bin/gplay` (`dlv` is on
  `PATH`). `gplay schema --list`, `gplay <cmd> --help` and `gplay auth list`
  work offline. Commands that talk to Google need a fake: start an
  `httptest.Server` and point the transport at it from a Go test, as the
  existing command tests do.
- Existing security tests to start from:
  `internal/pathguard/escape_hatch_test.go`,
  `internal/**/containment_test.go`, `internal/kernel/redaction_test.go`,
  `internal/output/envelope_redaction_test.go`.
- Design references: `docs/DESIGN.md` (auth precedence, output contract, path
  containment), `docs/adr/` (0001 credential storage, 0017 write safety, 0024
  read-only, 0046 manifest readers).

## How you rate severity

- **Critical**: the OAuth bearer token or the service-account private key
  leaves the process to a non-Google host, or is written in clear to a file or
  log, from attacker-controlled input with no unusual user action; or code
  execution from a cloned repository or from API data.
- **High**: arbitrary file write or overwrite outside the target tree from API
  data or a hostile repo; a read-only or draft-default bypass that publishes to
  users; a credential leak that needs one plausible user action (running a
  normal command inside a hostile repo, for example).
- **Medium**: terminal escape injection in human output; a crash (panic) or
  unbounded memory from a crafted artifact, CSV or API response that bypasses a
  size cap; file permission weaknesses on stored credentials.
- **Low**: information disclosure of non-secret data (package names, file
  paths), crashes that need a local attacker who already controls the input
  file, defense-in-depth gaps without a demonstrated exploit.

Without a working reproducer, cap the rating at Medium.

## How reports and patches should look

- One finding per report, with a Go test reproducer in the style of the
  package's existing tests (testkit fake transport, no network).
- Patches must keep `--output json` byte-identical to the API response
  (ADR-0003) and must not add a dependency on `google.golang.org/api` or
  `golang.org/x/oauth2/google` (ADR-0007, enforced by depguard).
- API URLs come from `internal/apiregistry`; a patch must not hand-write one.

## Anything to leave alone

- `--output json` printing API data verbatim, unredacted on stdout: deliberate
  (stdout carries data; secrets are redacted on stderr and in error envelopes).
- The root `Dockerfile` base image not pinned by digest: deliberate, the release
  image is signed and attested.
- `GPLAY_INSTALL_NO_VERIFY=1` and `GPLAY_ALLOW_EXTERNAL_SYMLINKS=1` disabling
  their checks: documented opt-outs.
- Keys stored in plaintext files when no OS keyring is available: documented
  fallback, with a warning.
