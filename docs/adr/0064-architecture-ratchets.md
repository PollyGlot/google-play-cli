# ADR-0064: Architecture ratchets

## Status

accepted (2026-11-20)

## Context

The 2026-09 audit (PRD #573) found the same helpers copied from command to
command: a private request helper, a hand-rolled test `RoundTripper`, a fresh
RSA key per test, an ad-hoc `http.Client`, a local usage-error type, a
renderer printing straight to stdout. The paved road replaced each of them
with one shared implementation. Without a gate, the next command copies its
neighbour again, and the copy is the old pattern. `internal/ratchet` is that
gate. These decisions lived in code comments; the comment purge (the
`scripts/lint/comments.py` ratchet) gathers them here.

## Decision

### What a ratchet is

- **One rule per pattern the paved road replaced**, each scanning the Go
  files under `cmd/`, `commands/` and `internal/`.
- **Each rule compares what it finds with a committed allowlist** of today's
  offenders, `internal/ratchet/testdata/<rule>.allow`, one `path symbol` per
  line. The comparison fails both ways: an offender missing from the list
  fails (a new copy), and a line that no longer matches an offender fails too
  (the copy was migrated, so its line must go). An allowlist can therefore
  only shrink.
- **A rule with no offenders left has no allowlist file**: it forbids
  outright.
- **`make ratchets` prints the size of every allowlist**, for a before and
  after report on a migration.

### The rules and what replaces each pattern

| Rule | Forbids | Use instead |
|---|---|---|
| `request-helper` | a direct `(*http.Client).Do` outside `internal/play/api` | the executor, `api.Do` / `api.DoJSON` |
| `test-roundtripper` | a `RoundTrip` method declared by test code outside `internal/testkit` | `testkit.NewFake`, or a `testkit.RoundTripFunc` built on `testkit.TokenResponse` and `testkit.Response`; extend the kit when it cannot express a case |
| `test-rsa-keygen` | `rsa.GenerateKey` in test code outside `internal/testkit` | `testkit.RSAKey`, `PrivateKeyPEM` or `ServiceAccountJSON` (one key per test binary) |
| `http-client` | an `http.Client` built by shipped code outside `internal/transport` | the client the RunContext builds (`rc.AuthedClient`, `rc.UploadClient`); wrap transports in `internal/transport`, whose `NewClient` is the one constructor |
| `usage-error-type` | a local usage-error type | `exit.UsageError` (`exit.Usagef`) |
| `stdout-bypass` | stdout written outside `output.Render` | an `output.Renderable` (or `output.WriteJSON` for a JSON-only echo) |

`stdout-bypass` exists for the terminal, not for style: `output.Render`
sanitises every human view (DESIGN §7), so a renderer that prints an API
string around it can carry an escape sequence to the screen.

### Exemptions

An exemption is a directory where the pattern is the implementation itself or
cannot apply; each is stated with its reason.

- **The owner of the pattern**: `internal/play/api` for `request-helper`,
  `internal/transport` for `request-helper` and `http-client`,
  `internal/testkit` for the test rules, `internal/output` and
  `internal/kernel` for `stdout-bypass` (the render boundary, and the code
  that hands `rc.Stdout` to it).
- **Cloud Storage** (`internal/play/gcs`, `reviews history`) is not a Play
  API: no Discovery snapshot, no registry method, so the executor cannot
  address it. The registry gate exempts it for the same reason
  ([ADR-0065](./0065-api-registry-and-discovery-snapshot.md)).
- **`internal/discovery`** fetches Discovery documents for
  `make discovery-update`, a development tool outside the binary; the
  Discovery service is not in the registry either.
- **`internal/testkit` counts as shipped code** for `http-client`, because its
  package name does not end in `test`; no production package imports it and
  its clients only ever wrap the fake transport.
- **`cmd/gplay`** wires `os.Stdout` into the boot and prints the `version`
  line, which carries no API string.
- **The binary downloads** (`releases generated download`,
  `releases system-apks download`) stream an APK to stdout: bytes, not a
  view, so sanitising them would corrupt the file.

### Detectors

- **Detectors read syntax, not types**: the module does not depend on
  `golang.org/x/tools`. Each detector is a pattern tight enough to have no
  false positive on the tree, and a test pins every detector against a sample
  of its pattern.
- **`request-helper` matches a one-argument `x.Do(arg)`** whose receiver is
  not an imported package (`api.Do` takes three arguments) and whose argument
  is not a function literal (`sync.Once.Do`). It does not match `Get` or
  `Post` on a client, which no code uses; the detector grows the day one
  appears.
- **The `rsa` and `http` rules resolve the file's own import name**, so an
  aliased import does not slip through.
- **`stdout-bypass` flags a `fmt.Print*` call, or a stdout handle**
  (`os.Stdout`, a `.Stdout` field such as `rc.Stdout`, `cmd.OutOrStdout()`)
  used for anything but wiring it into another `Stdout` (an assignment or a
  `Stdout:` field) or passing it to `output.Render` or `output.WriteJSON`. The
  AST walk visits a parent before its children, which is what lets the wiring
  and sink checks mark a handle before the handle itself is visited.
- **Known blind spot**: a handle stored under another name is not followed.
  `w := rc.Stdout` is caught at the assignment, but a helper that takes an
  `io.Writer` is not inspected.
- **Test code is a `_test.go` file or a test-support package** (a package
  name ending in `test`, such as `internal/teamtest`), the convention the
  registry gate set.

### Where the gates live

The literal-API-path gate that these rules generalise stays in
`internal/apiregistry`, next to the registry it protects
([ADR-0065](./0065-api-registry-and-discovery-snapshot.md)). The ratchets
live in a package of their own because they protect no single package.

## Consequences

- A new command cannot reintroduce a replaced pattern without editing an
  allowlist in a reviewed diff, and an allowlist line can only be removed.
- When the paved road cannot express a case (a test transport the kit lacks),
  the fix is to extend the shared implementation, not to add an allowlist
  line.
- A detector's blind spot is a known gap, not a licence: code that routes
  stdout through a helper to avoid the gate defeats the sanitisation the rule
  exists for.
