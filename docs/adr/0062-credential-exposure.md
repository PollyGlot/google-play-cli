# ADR-0062: Where a credential could leak, and what stops it

## Status

accepted (2026-11-20)

amended (2026-11-20): implementation decisions moved from code comments

## Context

gplay holds a service account's private key and the OAuth2 tokens minted from
it, and runs in CI, where logs are pasted into issues. A secret can leave the
process through a log line, through output, or through a request to a host
that is not Google's API. These decisions lived in code comments; the comment
purge (the `scripts/lint/comments.py` ratchet) gathers them here.

This ADR is a catalogue, one section per exit route. A new route, or a new
guard on an existing one, gets its entry under the route it concerns.

## Decision

### stderr

- **One filter at the stderr boundary**, rather than a rule every future log
  line must remember. gplay holds three kinds of secret: a service account's
  PEM private key, the JSON fields that carry it (`private_key`,
  `private_key_id`), and the OAuth2 bearer token minted from it. Any of them
  can reach stderr by accident: a JSON parse error quoting the offending bytes,
  a verbose transport line dumping headers, a wrapped library error echoing
  its input. The filter is always on; no flag disables it.
- **Redaction is a property of the run context, not of a call site.** The
  binary wraps stderr in main, but tests and any caller assembling the kernel
  by hand reach the same stream, so the kernel wraps it again when it builds
  the run context. The redacting writer is idempotent (the two wrappings do
  not stack) and turns a nil writer into a discard.
- **The root command's error writer is the redacting one.** The kernel reads
  the writer back from cobra (`ErrOrStderr`), and several commands re-wire
  stderr the same way before a sub-run, so the boot writer alone would leave
  a path around the filter; setting it on the root hands it down the whole
  tree, leaves added later included.

### stdout

- **stdout is never wrapped in the filter.** It mirrors API responses byte
  for byte ([ADR-0003](./0003-json-passthrough.md)), and masking it would
  corrupt payloads a machine parses; the API never returns gplay's own
  credentials.
- **The gplay-authored strings that land on stdout are masked field by
  field** (`redact.String`): the error envelope's `message` and `operation`
  ([ADR-0023](./0023-json-error-envelope.md)), since a wrapped error can quote
  a credential (#583), and the `auth doctor` hints. The envelope's `reasons`
  stay verbatim: they are API passthrough.

### Messages that quote their input

- **A credential value that fails to load is not echoed.** A value that is
  neither JSON nor a readable path is often a credential in the wrong shape
  (base64, quoted JSON, a leading byte-order mark), and `os.ReadFile` would
  quote all of it in its `*fs.PathError` ("open <value>: file name too
  long"). That message would reach stderr, the error envelope and the doctor
  checklist, and a base64 blob matches none of the redaction patterns. The
  user contract (what is shown instead) is DESIGN §1.
- **Only a `*fs.PathError` is unwrapped for its reason**, because its `Err`
  field is known not to hold the path. Any other error shape may quote its
  input, so it yields no reason at all.
- **A value is shown back only when it cannot be a credential.** A service
  account is over a kilobyte in any encoding, while a real path fits in
  `NAME_MAX` (255 bytes on common filesystems): that is the length bound.
- **The misshapen forms are diagnosed, never decoded.** Accepting base64 or a
  quoted blob would change the one detection rule DESIGN §1 documents.
- **The test that no command echoes a credential value looks for any
  16-byte window of the raw value**, besides PEM markers and a canary: that is
  what catches a base64 blob, which no redaction pattern matches.

### Requests to hosts outside the API

- **A URL the API hands back is fetched without credentials.** A Store
  image's preview `url` is downloaded with a client that carries no token, so
  the Account's token never reaches that host or any host it redirects to.
  That client keeps the authed client's deadline and `--retry`.
- **That client refuses anything but https**, for the first URL and every
  redirect.
- **The https guard is a `RoundTripper`**, because a client re-enters its
  transport on each redirect hop: the check covers a redirect to another
  scheme as well as the first URL.
- **The https guard sits innermost**, under the retry middleware: the retry
  middleware follows redirects with a client of its own, so a guard wrapped
  around it would never see the hops.
- **A refused non-https URL exits 1**, not the network's retry-safe 50: the
  same URL would be refused again.

### The redaction rules

- **The mask has a fixed length.** A run of asterisks sized to the input would
  leak the secret's length, which is itself a hint.
- **The rules are ordered from most to least specific**, each applied to the
  whole buffer in turn, and none is anchored to a line: a PEM block spans many
  lines, and error wrapping often folds one onto a single line, so `.` crosses
  newlines.
- **A JWT is matched by shape**: three base64url segments separated by dots,
  the first starting with `ey` because it is a JSON object (only the first is
  anchored, which errs toward masking). A length
  floor keeps dotted identifiers (a package name, a version) out.
- **Google's access and refresh tokens have their own rule** (the `ya29.`
  family and the refresh-token prefixes): they have no dots, so the JWT rule
  cannot see them.
- **A credential after an auth scheme name is masked without an
  `Authorization` header** (a proxy diagnostic, a curl-style line, a library
  error quoting only the header value). A 16-character floor keeps prose out:
  the longest word that can follow a scheme name in a sentence is 14
  characters ("authentication"), and a real token is always longer.
- **The generic key rule matches a closed list of words that only name a
  credential**, with a free prefix, so `client_secret`, `access_token` and
  `my_password` match without being listed. Bare `key` is not in it:
  `private_key` has its own rule, and "cache key" or "sort key" would blank
  real diagnostics.
- **A quoted value is masked only when non-empty**, so an empty-string field
  still reads as empty, which is a useful diagnostic.
- **The `key=value` rule takes the full word list**: nothing narrates with
  `=` (a query string, an environment dump, a flag echoed back).
- **The `key: value` rule drops the bare word `token`.** Go's error convention
  is `pkg: message`, `token` is one of gplay's packages, and the oauth2 library
  renders "cannot fetch token: 401 Unauthorized": masking there would blank
  the status code, the most useful thing in an auth failure. Compounds
  (`access_token:`) and `secret` or `password` keys never read as a package
  prefix and stay in.
- **The `key: value` rule runs last** because it is the broadest. Its value
  stops at whitespace or at a delimiter that cannot be part of a credential,
  so the rest of the line survives, and its character class excludes `"`, so
  it never re-matches what the quoted rule produced.
- **`client_email` is not a secret.** It is the single most useful thing in a
  misconfiguration report and survives redaction.

### The redacting writer

- **Each `Write` is filtered on its own**, without buffering to a newline. A
  secret split across two writes would slip through, but a buffering filter
  would hold back a partial line (a prompt, progress) the user needs now, and
  would need a `Close` nobody would remember to call. Every gplay stderr write
  is one whole message.
- **Nothing streams through it.** A child process's stderr, which `os/exec`
  delivers in 32 KiB blocks that cut messages anywhere, must never be piped
  through the writer: that would break the one-message-per-write invariant
  the trade rests on.
- **`Write` reports `len(p)`, not the masked length**: callers treat a short
  write as an error, and masking legitimately changes the byte count.

### At rest

- **The file backend tightens modes on every write.** `MkdirAll` and
  `OpenFile` apply a mode only when they create the path, so the directory
  and the key file are both chmod-ed explicitly: a root or key left loose by
  an older build, a backup restore or a manual copy is tightened on the next
  write (#795), and the file is tightened before the key bytes land in it. The
  test for it chmods its loose fixture after creating it, since the umask may
  have trimmed the requested mode and the test would then pass vacuously.
- **A key written to the file backend is announced at write time**, not
  behind `-v`: a private key in plaintext on disk (a locked keychain over SSH,
  a headless box) is something to learn at login, not during an offboarding
  audit.
- **Stray key files draw a warning** when the keyring is the active backend.
  An earlier process that could not reach the keyring wrote them, gplay no
  longer reads them, and nothing else would point at them. The warning is
  advice: a listing failure is ignored and never fails the command.

### The token exchange

- **The JWT carries the key id** (`private_key_id` as the header `kid`), so
  Google verifies against that key instead of trying each one.
- **Only a service-account key is accepted.** An `authorized_user` or
  `external_account` file carries no private key to sign with; the refusal
  `JWTConfigFromJSON` made is kept since that call was dropped
  ([ADR-0007](./0007-raw-http-not-google-go-sdk.md)).
- **The requested scope travels inside the signed assertion**, not as a
  form field (JWT bearer grant). Code that needs to know which scope was
  requested (the doctor's scope checks, their tests) decodes the assertion's
  `scope` claim.
- **A token-endpoint refusal is read from the body.** The jwt flow builds its
  `RetrieveError` without parsing the body, so `ErrorCode` stays empty and
  the RFC 6749 `error` parameter is read from the JSON body instead
  ([ADR-0058](./0058-api-failure-classification.md)).

## Consequences

- No log line has to remember to mask: a new stderr write is covered by
  construction. A new gplay-authored string on stdout does have to be masked
  where it is built.
- A new command that downloads from a URL returned by the API uses the
  credential-free client; using the authed one would send the token to that
  host.
