// Package kernel owns the per-invocation boot sequence shared by every
// gplay command: build the Boot once in cmd/gplay/main.go, the cobra
// layer captures the request-shaped Inputs, and kernel.Run resolves
// Format and the Account *name* once before handing a populated
// RunContext to the command's business function. The credential bytes and
// the keystore backend are resolved lazily — only when a command asks for
// them via RunContext.EnsureAccount / Backend (both reached through
// AuthedClient) — so a pre-auth command never probes the OS keyring. See
// RunContext.Backend for why that deferral matters.
//
// Each command exposes
//
//	func Xxx(rc *kernel.RunContext, in XxxInput) (output.Renderable, error)
//
// which the cobra RunE invokes via kernel.RunCobra. The kernel renders
// the returned Renderable; commands with no payload (login, logout
// side-effects) return (nil, nil).
//
// RunContext is not safe for concurrent use — one cobra invocation,
// one RunContext.
package kernel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/transport"
)

// GroupRunE is the RunE for a grouping command that carries no business logic
// of its own (a pure noun like `apps`, `metadata`, `team users`). A bare
// invocation prints the group help; any leftover token is an unknown
// subcommand, surfaced as CLI misuse (exit 2 per docs/DESIGN.md §9).
//
// It is a RunE — not an Args validator — on purpose: cobra short-circuits a
// NON-runnable command (no Run/RunE) straight to its help text BEFORE arg
// validation runs, so a child group with only an Args hook would still
// silently print help (exit 0) on a mistyped or removed subcommand. Giving the
// group a RunE makes the rejection actually fire, so a typo — or a hard rename
// (ADR-0019) where a CI step still calls the old name — breaks loudly instead
// of "succeeding" with exit 0.
//
// cobra's default Args validator (legacyArgs) only rejects unknown commands
// for the ROOT, never for a child group, which is why every name-group needs
// this helper for a consistent UX. The root itself, to route through here
// rather than legacyArgs (which yields a plain error → exit 1, not exit 2),
// sets Args: cobra.ArbitraryArgs alongside this RunE — see cmd/gplay/main.go.
//
// Pair GroupRunE with SilenceUsage:true and SilenceErrors:true on the command
// so the failure surfaces as the single `gplay: ...` line main.go prints,
// without cobra's own "Error: ..." + usage dump on top.
func GroupRunE(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	return exit.Usagef("unknown command %q for %q", args[0], cmd.CommandPath())
}

// mutatingAnnotation is the cobra annotation key a write command sets to declare
// itself subject to the GPLAY_READONLY policy. One key, set in one place per
// command (MarkMutating), is the whole registry — there are no per-command
// readonly checks scattered through the business functions (ADR-0024).
const mutatingAnnotation = "gplay.mutating"

// MarkMutating declares cmd as a mutating (write) command and returns it, so
// it composes inline at registration: `releases.AddCommand(kernel.MarkMutating(
// upload.NewCommand(boot)))`. Under GPLAY_READONLY a marked command is refused
// (exit 4) unless invoked with --dry-run. Every new write command MUST be
// marked — see CONTRIBUTING.
func MarkMutating(cmd *cobra.Command) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[mutatingAnnotation] = "true"
	return cmd
}

// IsMutating reports whether cmd was declared mutating via MarkMutating.
func IsMutating(cmd *cobra.Command) bool {
	return cmd != nil && cmd.Annotations[mutatingAnnotation] == "true"
}

// scopeAnnotation is the cobra annotation key a command sets to request a
// non-default OAuth scope for its token exchange. Like mutatingAnnotation it is
// declared in one place per command (WithScope) and read in one place
// (FromCobra → Inputs.Scope), so the per-command least-privilege policy is a
// small registry, not a check scattered through business functions.
const scopeAnnotation = "gplay.scope"

// WithScope declares that cmd's API calls require oauthScope rather than the
// default androidpublisher scope, and returns it so it composes inline at
// registration: `vitals.AddCommand(kernel.WithScope(query.NewCommand(boot),
// token.ReportingScope))`. It is the reporting-scope analogue of MarkMutating.
// A command left unmarked requests the default androidpublisher scope (#49).
func WithScope(cmd *cobra.Command, oauthScope string) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[scopeAnnotation] = oauthScope
	return cmd
}

// ScopeFor returns the OAuth scope cmd declared via WithScope, or "" when it
// declared none — the sentinel the kernel resolves to the default
// androidpublisher scope (token.Source's zero-scope default).
func ScopeFor(cmd *cobra.Command) string {
	if cmd == nil {
		return ""
	}
	return cmd.Annotations[scopeAnnotation]
}

// EnvReadonly is the GPLAY_READONLY policy switch. It reports whether the env
// var holds a truthy value (1/true/yes/on, case-insensitive); anything else
// (unset, "0", "false", …) leaves writes allowed.
const EnvReadonly = "GPLAY_READONLY"

func envReadonlyEnforced() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvReadonly))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Boot carries the process-level wiring built once in cmd/gplay/main.go:
// IO streams, the FS seam, paths the commands read and write. Nothing
// here is per-invocation.
type Boot struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	// FS is the seam internal/config reads and writes through. nil
	// defaults to config.OSFS{}.
	FS config.FS

	// ConfigPath is the path to $XDG_CONFIG_HOME/gplay/config.json
	// (or platform equivalent).
	ConfigPath string

	// KeystoreRoot is the directory the file-backed keystore uses when
	// the OS keyring is unavailable.
	KeystoreRoot string

	// Keyring is the go-keyring adapter. nil uses the production one.
	Keyring keystore.KeyringAPI
}

// Inputs is the per-cobra-invocation surface: flag values cobra has
// parsed. The cobra wrapper builds it via FromCobra and hands it to Run.
type Inputs struct {
	// Ctx is the per-invocation context. nil falls back to
	// context.Background.
	Ctx context.Context

	// Format is the value of --output. FormatAuto (empty) is fine; the
	// kernel resolves it against TTY state + CI.
	Format output.Format

	// Resolver carries --service-account / --account flag values for
	// resolver.Resolve.
	Resolver resolver.Inputs

	// Verbose mirrors the --verbose persistent flag.
	Verbose bool

	// Timeout mirrors the global --timeout persistent flag: an explicit
	// per-request deadline applied to every API call (control-plane AND media
	// upload). Zero means "unset" — control-plane calls then fall back to the
	// 60s default while uploads stay unbounded. See RunContext.AuthedClient /
	// UploadClient.
	Timeout time.Duration

	// Retry mirrors the global --retry persistent flag: the number of automatic
	// retries (transport errors / 5xx / 429) layered onto the authed client.
	// Zero (the default) is today's behavior — no retry. When set, --timeout
	// becomes a per-attempt bound.
	Retry int

	// Mutating reports whether the invoked command is declared as a write
	// command (MarkMutating). Read commands leave it false. Used together with
	// Readonly to enforce the GPLAY_READONLY policy (ADR-0024).
	Mutating bool

	// DryRun mirrors the command's --dry-run flag (false when the command has
	// none). A --dry-run of a mutating command never writes, so it is exempt
	// from the read-only refusal.
	DryRun bool

	// Readonly is the resolved GPLAY_READONLY environment policy (FromCobra
	// reads the env). When true, a mutating, non-dry-run command is refused
	// before credential resolution and any network I/O.
	Readonly bool

	// Scope is the OAuth scope the invoked command requested via WithScope, or
	// "" for the default androidpublisher scope. AuthedClient threads it into
	// the token exchange so a `vitals` command mints a least-privilege
	// reporting-scoped token (#49).
	Scope string
}

// RunContext is the post-resolution snapshot a command's business
// function receives.
type RunContext struct {
	Ctx context.Context

	// Account is the resolved credential. It is populated lazily by
	// EnsureAccount (which AuthedClient calls) — zero until a command asks
	// for it, so reading this field directly only sees a value after an
	// EnsureAccount / AuthedClient call. nil when nothing resolved (status
	// renders an informational message; doctor synthesises a check-1
	// failure; login/logout don't need one).
	Account *serviceaccount.ServiceAccount

	// AccountName is the local Account name the credential resolves to, or
	// "" when the credential came from an ad-hoc source (--service-account
	// flag or GPLAY_SERVICE_ACCOUNT env var). Unlike Account it is resolved
	// keystore-free at boot (resolver.ResolveName), so registry-scoping and
	// --dry-run readers get the name without provoking a keyring probe.
	// Commands writing to the registry must use this rather than
	// rc.Resolved.ConfigAccount, which only reflects the cascade layer and
	// ignores --account/--service-account/env overrides.
	AccountName string

	// Format is the resolved output Format — never FormatAuto.
	Format output.Format

	// Resolved is the full cascade snapshot. Commands access the
	// Project pin via rc.Resolved.Pin and the accounts list via
	// rc.Resolved.Accounts.
	Resolved *config.Resolved

	// Keystore is the selected backend and KeystoreLabel its stable label.
	// Both are populated lazily by Backend() (the first keystore use), not
	// at boot — read them via Backend()/EnsureAccount, not directly, on the
	// production path.
	Keystore      keystore.Backend
	KeystoreLabel string

	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	FS config.FS

	Verbose bool

	// Timeout is the resolved global --timeout (zero when unset). AuthedClient
	// and UploadClient read it to bound their requests; see those methods for
	// the control-plane default and the media-upload exemption.
	Timeout time.Duration

	// Retry is the resolved global --retry (zero when unset). AuthedClient /
	// UploadClient layer a retry transport when it is > 0.
	Retry int

	// Scope is the OAuth scope the command requested via WithScope ("" = the
	// default androidpublisher scope). authedClient passes it to token.Source so
	// a `vitals` command mints a least-privilege reporting-scoped token (#49).
	Scope string

	// ConfigPath, KeystoreRoot mirror Boot for commands that write to
	// them (login, logout) or compute the credential's on-disk path
	// (status).
	ConfigPath   string
	KeystoreRoot string

	// --- lazy keystore/credential resolution (production path) ---
	//
	// buildRunContext leaves Account, Keystore and KeystoreLabel zero and
	// arms these fields instead, so a pre-auth command (a failed --package
	// / --track / --confirm validation, a --dry-run, --help) never touches
	// the OS keyring — the probe that pops the macOS "keychain locked"
	// dialog is deferred until something actually needs a credential
	// (Backend / EnsureAccount, both reached via AuthedClient). AccountName
	// is the exception: it is resolved keystore-free up front (see
	// resolver.ResolveName) because registry scoping and the --dry-run
	// preview need the name without the bytes.
	//
	// A hand-built RunContext (NewForTest) leaves lazy=false with the
	// public fields pre-populated; the accessors then return them verbatim
	// and the test suite keeps assigning rc.Account / rc.Keystore directly.
	lazy           bool
	keyring        keystore.KeyringAPI
	resolverInputs resolver.Inputs
	backendDone    bool
	accountDone    bool
	// outCounter wraps Stdout on the production (kernel.Run) path so the JSON
	// error-envelope emitter can tell whether the command already wrote output
	// of its own (doctor's self-rendered checklist) before appending an
	// envelope. nil on the NewForTest path. See maybeWriteErrorEnvelope.
	outCounter *countingWriter
	// accountErr memoises the invalid-credential outcome of EnsureAccount
	// (ADR-0020). It is nil for both the valid and the benign-absent cases;
	// non-nil only when a credential was provided but unusable. Guarded by
	// accountDone so a repeat call returns the same result without a second
	// keyring probe.
	accountErr error
}

// NewForTest returns a base RunContext wired from boot+in with NO
// resolution performed. Tests assemble a RunContext by setting Account,
// Resolved, Keystore, etc. directly. Production code uses kernel.Run.
func NewForTest(ctx context.Context, boot Boot, in Inputs) *RunContext {
	fsys := boot.FS
	if fsys == nil {
		fsys = config.OSFS{}
	}
	stdout := boot.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	stderr := boot.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	return &RunContext{
		Ctx:          ctx,
		Stdout:       stdout,
		Stderr:       stderr,
		Stdin:        boot.Stdin,
		FS:           fsys,
		Verbose:      in.Verbose,
		Timeout:      in.Timeout,
		Retry:        in.Retry,
		Scope:        in.Scope,
		ConfigPath:   boot.ConfigPath,
		KeystoreRoot: boot.KeystoreRoot,
		Format:       in.Format,
	}
}

// Run resolves Format, Account, Project, Keystore from boot+in, then
// invokes fn with the populated RunContext. The returned Renderable
// (if any) is rendered to rc.Stdout in rc.Format. fn's error surfaces
// verbatim so the caller's exit.For mapping stays intact.
//
// fn is responsible for rendering its own partial-payload output when
// it returns both a Renderable and an error (e.g. doctor's checklist
// alongside a failing check). The kernel only renders on the success
// path so the default contract (errors → stderr only, data → stdout)
// holds for commands that don't opt in.
func Run(boot Boot, in Inputs, fn func(*RunContext) (output.Renderable, error)) error {
	rc, err := buildRunContext(boot, in)
	if err != nil {
		return err
	}
	// GPLAY_READONLY policy (ADR-0024): refuse a mutating, non-dry-run command
	// here — after the local config/format resolution buildRunContext does, but
	// BEFORE fn runs, so before any credential bytes are loaded (EnsureAccount
	// is lazy, reached only via fn → AuthedClient) and before any network I/O.
	// The authority boundary is the environment, not the flags fn would parse.
	if in.Readonly && in.Mutating && !in.DryRun {
		refusal := exit.Policyf(
			"refused by %s: this command mutates Google Play state and is blocked by the read-only environment policy. This is not resolvable by adding a flag — unset %s to allow writes, or run with --dry-run to preview.",
			EnvReadonly, EnvReadonly)
		rc.maybeWriteErrorEnvelope(refusal)
		return refusal
	}
	payload, runErr := fn(rc)
	if runErr != nil {
		// Under --output json, serialize the failure as a structured envelope
		// on stdout so an agent/CI consumer can branch without scraping stderr
		// (ADR-0023). main still prints the human line to stderr and exits with
		// exit.For(runErr) — exit codes and stderr are unchanged.
		rc.maybeWriteErrorEnvelope(runErr)
		return runErr
	}
	if payload == nil {
		return nil
	}
	return output.Render(rc.Stdout, rc.Format, payload.Renderers())
}

// Confirmf emits a single success-confirmation line on stderr, prefixed with
// the ✓ marker and terminated by a newline (DESIGN §8). It is the one funnel
// every payload-bearing mutation routes its committed-success line through, so
// a future --quiet flag can suppress them all in one place. Callers pass the
// message body only — no ✓, no trailing newline — and lean on canonical terms
// (track, status, versionCode, userFraction), never naming the Play "release"
// object. It is never called on a --dry-run: ✓ means the change was committed.
//
// The write is best-effort (the command's exit code and stdout payload are the
// authoritative signals) and a nil Stderr — possible on a hand-built
// RunContext — is a no-op rather than a panic.
func (rc *RunContext) Confirmf(format string, args ...any) {
	if rc.Stderr == nil {
		return
	}
	_, _ = fmt.Fprintf(rc.Stderr, "✓ "+format+"\n", args...)
}

// GplayDir returns the project's .gplay/ directory — the one found via walk-up
// that holds config.json — and ok=true when a project was resolved. It is the
// home of the explicit-Edit pin (.gplay/edit-<pkg>.json) and is gitignored for
// edit-*.json by `gplay init`, so a pin written here never lands in version
// control. ok=false means no .gplay/ was found (no `gplay init`); callers that
// must persist a pin treat that as a usage error, while read paths fall back to
// implicit mode.
func (rc *RunContext) GplayDir() (string, bool) {
	if rc.Resolved == nil || rc.Resolved.ProjectSharedPath == "" {
		return "", false
	}
	return filepath.Dir(rc.Resolved.ProjectSharedPath), true
}

// ExplicitEditID returns the open explicit-Edit ID pinned for pkg in the
// project's .gplay/edit-<pkg>.json, or "" when none is pinned (or no project
// was resolved) — the signal a write command uses to reuse an open Edit instead
// of opening its own (docs/DESIGN.md §4). A corrupt pin file is an error so a
// broken pin surfaces rather than silently opening a conflicting Edit.
func (rc *RunContext) ExplicitEditID(pkg string) (string, error) {
	gplayDir, ok := rc.GplayDir()
	if !ok {
		return "", nil
	}
	pin, found, err := editpin.Lookup(rc.FS, gplayDir, pkg)
	if err != nil || !found {
		return "", err
	}
	return pin.EditID, nil
}

// maybeWriteErrorEnvelope writes the JSON error envelope to stdout when the
// resolved format is JSON AND the failing command produced no stdout of its
// own. The byte-count guard is what keeps a self-rendering command (doctor
// writes its checklist then returns an error) from getting a second JSON
// object appended — there it has already emitted to stdout, so the envelope is
// suppressed. The write is best-effort: the authoritative signal is the error
// main prints to stderr and the exit code, both unaffected by a failure here.
func (rc *RunContext) maybeWriteErrorEnvelope(err error) {
	if rc.Format != output.FormatJSON {
		return
	}
	if rc.outCounter != nil && rc.outCounter.n > 0 {
		return
	}
	_ = output.WriteErrorEnvelope(rc.Stdout, err)
}

// countingWriter forwards every write to w while tallying the bytes written —
// the kernel uses the tally to decide whether a failing command already
// produced stdout (see maybeWriteErrorEnvelope).
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// RunCobra is the canonical entrypoint for a cobra RunE: it copies
// boot, points its writers at cmd, builds Inputs from the persistent
// flags, and dispatches to Run. The 5 auth commands collapse to a
// single RunCobra call.
func RunCobra(cmd *cobra.Command, boot Boot, outputFlag string, fn func(*RunContext) (output.Renderable, error)) error {
	boot.Stdout = cmd.OutOrStdout()
	boot.Stderr = cmd.ErrOrStderr()
	return Run(boot, FromCobra(cmd, outputFlag), fn)
}

func buildRunContext(boot Boot, in Inputs) (*RunContext, error) {
	fsys := boot.FS
	if fsys == nil {
		fsys = config.OSFS{}
	}
	ctx := in.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	stdout := boot.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	stderr := boot.Stderr
	if stderr == nil {
		stderr = io.Discard
	}

	format, err := output.Resolve(in.Format, stdout)
	if err != nil {
		return nil, err
	}

	resolved, err := config.LoadFromEnv(ctx, fsys, boot.ConfigPath)
	if err != nil {
		return nil, err
	}

	kr := boot.Keyring
	if kr == nil {
		kr = keystore.DefaultKeyring()
	}

	// Keystore selection (the probe) and credential loading are deferred:
	// see RunContext.Backend / EnsureAccount. Only the Account *name* is
	// resolved now, and ResolveName is keystore-free — so a pre-auth
	// command never touches the keyring, while registry-scoping commands
	// (apps add/list/remove) and the --dry-run preview still get the name
	// for free. The credential bytes (and the probe) wait until a command
	// asks for a token.
	accountName := resolver.ResolveName(resolver.Deps{Resolved: resolved}, in.Resolver)

	// Wrap stdout in a byte counter so the JSON error-envelope emitter can tell
	// whether a failing command already wrote its own output (ADR-0023).
	cw := &countingWriter{w: stdout}

	return &RunContext{
		Ctx:            ctx,
		AccountName:    accountName,
		Format:         format,
		Resolved:       resolved,
		Stdout:         cw,
		outCounter:     cw,
		Stderr:         stderr,
		Stdin:          boot.Stdin,
		FS:             fsys,
		Verbose:        in.Verbose,
		Timeout:        in.Timeout,
		Retry:          in.Retry,
		Scope:          in.Scope,
		ConfigPath:     boot.ConfigPath,
		KeystoreRoot:   boot.KeystoreRoot,
		lazy:           true,
		keyring:        kr,
		resolverInputs: in.Resolver,
	}, nil
}

// Backend lazily selects the credential keystore backend, running the OS
// keyring probe at most once per invocation and memoising the result into
// rc.Keystore / rc.KeystoreLabel. The verbose "keystore: using <label>"
// line is emitted here — when the backend is first actually used — rather
// than unconditionally at boot, so `-v` on a pre-auth command stays quiet
// and probe-free.
//
// A hand-built RunContext (lazy=false) returns the pre-set rc.Keystore
// unchanged, so login/logout tests keep injecting a backend directly.
func (rc *RunContext) Backend() (keystore.Backend, error) {
	if !rc.lazy || rc.backendDone {
		return rc.Keystore, nil
	}
	be, label, err := keystore.Select(rc.Ctx, keystore.SelectOptions{
		Keyring:  rc.keyring,
		FileRoot: rc.KeystoreRoot,
	})
	if err != nil {
		return nil, err
	}
	rc.Keystore = be
	rc.KeystoreLabel = label
	rc.backendDone = true
	if rc.Verbose {
		keystore.LogBackend(rc.Stderr, label)
	}
	return be, nil
}

// EnsureAccount lazily resolves the active credential into rc.Account,
// running the keyring probe + Load only when the resolved precedence layer
// is a stored Account. An inline credential (--service-account /
// GPLAY_SERVICE_ACCOUNT) loads from the flag/env with no keyring access at
// all. rc.AccountName is NOT touched; it was already resolved keystore-free
// at boot.
//
// Resolution splits two states (ADR-0020, DESIGN §1):
//
//   - Absent — resolution fails with resolver.ErrNoSource or
//     keystore.ErrNotFound (no source configured, or the active/named
//     Account has no key in the store: logged out, or never stored).
//     EnsureAccount returns nil and leaves rc.Account nil — the benign
//     signal status/doctor/apps list key off.
//   - Invalid — any other resolution error (malformed JSON, a missing
//     required field, an unreadable file, a non-NotFound keystore/IO
//     error). EnsureAccount wraps it in a credentialError (ExitCode 10,
//     "could not read credential: <cause>") and returns it; rc.Account
//     stays nil. This is the single chokepoint for the exit-10 guarantee.
//
// The outcome is memoised on rc.accountErr, guarded by accountDone, so
// repeat calls (a command calling it directly, then AuthedClient) return
// the same result with no extra keyring probe.
//
// A hand-built RunContext (lazy=false) is a no-op returning nil: rc.Account
// is whatever the test assigned.
func (rc *RunContext) EnsureAccount() error {
	if !rc.lazy || rc.accountDone {
		return rc.accountErr
	}
	rc.accountDone = true
	// resolverKeystore defers Backend() (and its probe) until the resolver
	// actually reaches a stored layer and calls Load — inline layers never
	// touch it.
	sa, err := resolver.Resolve(rc.Ctx, resolver.Deps{
		Resolved: rc.Resolved,
		Keystore: resolverKeystore{rc: rc},
	}, rc.resolverInputs)
	if err != nil {
		// Absent bucket: no credential is configured at all, or the
		// named/active Account has no key in the store. Benign — leave
		// rc.Account nil and return nil so the no-account paths still fire.
		if errors.Is(err, resolver.ErrNoSource) || errors.Is(err, keystore.ErrNotFound) {
			return nil
		}
		// Invalid bucket: a credential was provided but its bytes are
		// unusable. Wrap once, here, so the exit-10 guarantee lives in one
		// place rather than scattered across serviceaccount/keystore.
		rc.accountErr = &credentialError{cause: err}
		return rc.accountErr
	}
	rc.Account = sa
	return nil
}

// credentialError is the kernel-level chokepoint for the "credential was
// provided but is unusable" state (ADR-0020). It carries ExitCode()=10
// (DESIGN §9, "SA invalid") and the message "could not read credential:
// <cause>", wrapping the cause with Unwrap so the typed error underneath
// (e.g. serviceaccount.MissingFieldError and its field name) stays
// reachable via errors.As. The exit-10 guarantee for an invalid credential
// lives here and only here — serviceaccount and keystore do NOT each carry
// their own Coder for this.
type credentialError struct{ cause error }

func (e *credentialError) Error() string { return "could not read credential: " + e.cause.Error() }
func (e *credentialError) ExitCode() int { return 10 }
func (e *credentialError) Unwrap() error { return e.cause }

// resolverKeystore is the seam that keeps the resolver's keyring access
// lazy: it forwards each Backend call through rc.Backend(), so Select (and
// its probe) fires only when the resolver loads a stored Account. The
// resolver only ever calls Load today; Save/Delete/List are implemented
// for interface completeness.
type resolverKeystore struct{ rc *RunContext }

func (k resolverKeystore) Load(ctx context.Context, name string) ([]byte, error) {
	be, err := k.rc.Backend()
	if err != nil {
		return nil, err
	}
	return be.Load(ctx, name)
}

func (k resolverKeystore) Save(ctx context.Context, name string, data []byte) error {
	be, err := k.rc.Backend()
	if err != nil {
		return err
	}
	return be.Save(ctx, name, data)
}

func (k resolverKeystore) Delete(ctx context.Context, name string) error {
	be, err := k.rc.Backend()
	if err != nil {
		return err
	}
	return be.Delete(ctx, name)
}

func (k resolverKeystore) List(ctx context.Context) ([]string, error) {
	be, err := k.rc.Backend()
	if err != nil {
		return nil, err
	}
	return be.List(ctx)
}

// defaultControlPlaneTimeout bounds a single control-plane API request (and the
// OAuth2 token exchange it triggers). 60s is generous for the small JSON calls
// the Edits / tracks / reviews / team surfaces make, yet turns a hung
// connection into a fast exit 50 (network) instead of stalling a CI job until
// the runner-level kill. Media uploads are exempt (see UploadClient); the
// global --timeout overrides both (RunContext.Timeout). See docs/DESIGN.md §8.
const defaultControlPlaneTimeout = 60 * time.Second

// AuthedClient builds the authenticated *http.Client every control-plane
// command uses to reach the Google Play Developer API. It performs the auth
// handshake that was previously copy-pasted at each call site: it requires a
// resolved Account, mints an OAuth2 token source from it, and returns an
// oauth2-wrapped client.
//
// The returned client carries a per-request deadline: rc.Timeout when the
// global --timeout was set, else defaultControlPlaneTimeout. A hung connection
// therefore fails the request once the deadline elapses; the play modules wrap
// that transport-level error as api.Error{StatusCode:0}, which maps to exit 50.
//
// The base transport is read from rc.Ctx's oauth2.HTTPClient value (falling
// back to http.DefaultClient), and that same value (wrapped with the deadline)
// is threaded into the context oauth2.NewClient receives — so a single
// test-injected RoundTripper covers BOTH the /token exchange and the
// subsequent androidpublisher calls, and the deadline bounds both. This is the
// test seam the command tests rely on.
//
// An invalid credential propagates the credentialError verbatim (real
// cause + exit 10). An absent one yields an *authError (exit code 10 per
// docs/DESIGN.md §9). Callers with a no-network path (--dry-run,
// --no-verify) must gate this call behind that branch themselves.
func (rc *RunContext) AuthedClient() (*http.Client, error) {
	return rc.authedClient(rc.controlPlaneTimeout())
}

// UploadClient is AuthedClient for media-upload commands (bundles, images):
// a multi-hundred-MB transfer must not be killed by the short control-plane
// default, so the returned client carries NO deadline — UNLESS the global
// --timeout was set explicitly (rc.Timeout), which then bounds every request
// including the upload. Same auth handshake and test seam as AuthedClient.
func (rc *RunContext) UploadClient() (*http.Client, error) {
	// rc.Timeout is 0 (no deadline) unless --timeout was passed; uploads honor
	// only the explicit override, never the 60s control-plane default.
	return rc.authedClient(rc.Timeout)
}

// scopes returns the OAuth scope list to mint a token for: a one-element slice
// when the command requested a non-default scope via WithScope, else nil — the
// signal token.Source reads as "default to androidpublisher". Keeping the empty
// case nil means the publishing path passes exactly what it always did.
func (rc *RunContext) scopes() []string {
	if rc.Scope == "" {
		return nil
	}
	return []string{rc.Scope}
}

// controlPlaneTimeout resolves the deadline for a control-plane request: the
// explicit --timeout when set, else the 60s default.
func (rc *RunContext) controlPlaneTimeout() time.Duration {
	if rc.Timeout > 0 {
		return rc.Timeout
	}
	return defaultControlPlaneTimeout
}

// authedClient is the shared body of AuthedClient / UploadClient. timeout==0
// yields a client with no deadline; timeout>0 bounds both the /token exchange
// and the API request by it.
func (rc *RunContext) authedClient(timeout time.Duration) (*http.Client, error) {
	// First call that genuinely needs a credential: this is where the
	// keyring probe + Load finally happen (see EnsureAccount), not at boot.
	// A present-but-invalid credential surfaces its real cause here; an
	// absent one falls through to the authError login hint below.
	if err := rc.EnsureAccount(); err != nil {
		return nil, err
	}
	if rc.Account == nil {
		return nil, &authError{msg: "no Account resolved; run gplay auth login or set GPLAY_SERVICE_ACCOUNT"}
	}
	// The /token exchange runs through the context's HTTP client (jwt.Config
	// captures the context it is given), so bound it with the same deadline by
	// wrapping the base client — WITHOUT mutating the shared base or
	// http.DefaultClient — before threading it into the context the token
	// source captures. timeout==0 leaves the wrapper deadline-free.
	base := baseHTTPClient(rc.Ctx)
	timedBase := &http.Client{Transport: base.Transport, Timeout: timeout}
	ctx := context.WithValue(rc.Ctx, oauth2.HTTPClient, timedBase)
	// rc.Scope is "" for the default androidpublisher surface; a vitals command
	// (WithScope) passes the reporting scope. token.Source treats no scope as the
	// androidpublisher default, so the publishing path is unchanged.
	ts, err := token.Source(ctx, rc.Account, rc.scopes()...)
	if err != nil {
		return nil, &authError{msg: "could not build token source: " + err.Error()}
	}
	// oauth2.NewClient sets the returned client's Base to the (unwrapped) base
	// transport.
	client := oauth2.NewClient(ctx, ts)
	if rc.Retry > 0 {
		// --retry: a transport middleware retries transport errors / 5xx / 429
		// (honoring Retry-After) with exponential backoff + jitter. It owns the
		// per-ATTEMPT deadline (so a retry sequence can outlast a single
		// timeout), so the per-request client.Timeout stays unset here.
		client.Transport = transport.WithRetry(client.Transport, transport.RetryOptions{
			MaxRetries: rc.Retry,
			Timeout:    timeout,
		})
		return client, nil
	}
	// No retry: the per-request deadline lives on the client (the oauth2
	// client's Base is the unwrapped base transport, so it can't live there).
	client.Timeout = timeout
	return client, nil
}

// baseHTTPClient extracts the transport used as the underlying client for
// both the OAuth2 /token exchange and the androidpublisher API calls. Tests
// inject a RoundTripper via ctx.Value(oauth2.HTTPClient); production falls
// back to http.DefaultClient.
func baseHTTPClient(ctx context.Context) *http.Client {
	if v := ctx.Value(oauth2.HTTPClient); v != nil {
		if c, ok := v.(*http.Client); ok && c != nil {
			return c
		}
	}
	return http.DefaultClient
}

// authError signals an auth failure surfaced by AuthedClient (no resolved
// Account, or the token source could not be built); ExitCode()=10 per
// docs/DESIGN.md §9.
type authError struct{ msg string }

func (e *authError) Error() string { return e.msg }
func (e *authError) ExitCode() int { return 10 }

// FromCobra builds an Inputs from cmd's persistent flag values
// (--verbose, --service-account, --account), the credential env vars,
// and cmd.Context(). Reading os.Getenv here (once per invocation) is
// the kernel's single concession to process state — the resolver
// itself stays pure.
func FromCobra(cmd *cobra.Command, format string) Inputs {
	verbose, _ := cmd.Flags().GetBool("verbose")
	saFlag, _ := cmd.Flags().GetString("service-account")
	acctFlag, _ := cmd.Flags().GetString("account")
	timeout, _ := cmd.Flags().GetDuration("timeout")
	retry, _ := cmd.Flags().GetInt("retry")
	// GetBool returns (false, err) when the command has no --dry-run flag; the
	// error is ignored on purpose — "no dry-run flag" means "not a dry run".
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	return Inputs{
		Ctx:      cmd.Context(),
		Format:   output.Format(format),
		Verbose:  verbose,
		Timeout:  timeout,
		Retry:    retry,
		Mutating: IsMutating(cmd),
		DryRun:   dryRun,
		Readonly: envReadonlyEnforced(),
		Scope:    ScopeFor(cmd),
		Resolver: resolver.Inputs{
			ServiceAccountFlag: saFlag,
			AccountFlag:        acctFlag,
			EnvServiceAccount:  os.Getenv(resolver.EnvServiceAccount),
			EnvAccount:         os.Getenv(resolver.EnvAccount),
		},
	}
}
