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
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

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
	payload, runErr := fn(rc)
	if runErr != nil {
		return runErr
	}
	if payload == nil {
		return nil
	}
	return output.Render(rc.Stdout, rc.Format, payload.Renderers())
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

	return &RunContext{
		Ctx:            ctx,
		AccountName:    accountName,
		Format:         format,
		Resolved:       resolved,
		Stdout:         stdout,
		Stderr:         stderr,
		Stdin:          boot.Stdin,
		FS:             fsys,
		Verbose:        in.Verbose,
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
// all. Resolution is best-effort and idempotent: any error (no source,
// missing key, malformed JSON) leaves rc.Account nil — the signal commands
// key off (AuthedClient maps it to exit 10, status/doctor render their own
// "no active account" path). rc.AccountName is NOT touched; it was already
// resolved keystore-free at boot.
//
// A hand-built RunContext (lazy=false) is a no-op: rc.Account is whatever
// the test assigned.
func (rc *RunContext) EnsureAccount() {
	if !rc.lazy || rc.accountDone {
		return
	}
	rc.accountDone = true
	// resolverKeystore defers Backend() (and its probe) until the resolver
	// actually reaches a stored layer and calls Load — inline layers never
	// touch it.
	sa, _ := resolver.Resolve(rc.Ctx, resolver.Deps{
		Resolved: rc.Resolved,
		Keystore: resolverKeystore{rc: rc},
	}, rc.resolverInputs)
	rc.Account = sa
}

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

// AuthedClient builds the authenticated *http.Client every API-touching
// command uses to reach the Google Play Developer API. It performs the
// auth handshake that was previously copy-pasted at each call site: it
// requires a resolved Account, mints an OAuth2 token source from it, and
// returns an oauth2-wrapped client.
//
// The base transport is read from rc.Ctx's oauth2.HTTPClient value (falling
// back to http.DefaultClient), and that same value is threaded into the
// context oauth2.NewClient receives — so a single test-injected RoundTripper
// covers BOTH the /token exchange and the subsequent androidpublisher calls.
// This is the test seam the command tests rely on.
//
// A nil Account yields an *authError (exit code 10 per docs/DESIGN.md §9).
// Callers with a no-network path (--dry-run, --no-verify) must gate this
// call behind that branch themselves.
func (rc *RunContext) AuthedClient() (*http.Client, error) {
	// First call that genuinely needs a credential: this is where the
	// keyring probe + Load finally happen (see EnsureAccount), not at boot.
	rc.EnsureAccount()
	if rc.Account == nil {
		return nil, &authError{msg: "no Account resolved; run gplay auth login or set GPLAY_SERVICE_ACCOUNT"}
	}
	// Thread the base client into the context BEFORE building the token
	// source: jwt.Config.TokenSource captures the context it is given and
	// reuses it for the /token mint/refresh calls. Deriving ctx first means
	// one injected client (the test seam, or http.DefaultClient in prod)
	// covers both the /token exchange and the androidpublisher calls.
	ctx := context.WithValue(rc.Ctx, oauth2.HTTPClient, baseHTTPClient(rc.Ctx))
	ts, err := token.Source(ctx, rc.Account)
	if err != nil {
		return nil, &authError{msg: "could not build token source: " + err.Error()}
	}
	return oauth2.NewClient(ctx, ts), nil
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
	return Inputs{
		Ctx:     cmd.Context(),
		Format:  output.Format(format),
		Verbose: verbose,
		Resolver: resolver.Inputs{
			ServiceAccountFlag: saFlag,
			AccountFlag:        acctFlag,
			EnvServiceAccount:  os.Getenv(resolver.EnvServiceAccount),
			EnvAccount:         os.Getenv(resolver.EnvAccount),
		},
	}
}
