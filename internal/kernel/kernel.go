// Package kernel owns the per-invocation boot sequence shared by every
// gplay command: build the Boot once in cmd/gplay/main.go, the cobra
// layer captures the request-shaped Inputs, and kernel.Run resolves
// Account / Project / Format / HTTP once before handing a populated
// RunContext to the command's business function.
//
// Each command exposes
//
//	func Xxx(rc *kernel.RunContext, in XxxInput) (output.Renderable, error)
//
// which the cobra RunE invokes inside kernel.Run via a closure. The
// kernel renders the returned Renderable; commands with no payload
// (login, logout side-effects) return (nil, nil).
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

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// Logger is the verbosity surface a command sees. Verbose runs flush
// info lines to stderr; quiet runs (the default) drop them. The kernel
// builds the right Logger from Inputs.Verbose.
type Logger interface {
	Infof(format string, args ...any)
}

// Boot carries the process-level wiring built once in cmd/gplay/main.go:
// IO streams, the FS seam, factories for transports gplay layers itself
// on top of. Nothing here depends on cobra and nothing here is
// per-invocation.
type Boot struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	// FS is the filesystem seam used by internal/config. nil defaults
	// to config.OSFS{}.
	FS config.FS

	// TTYDetect reports whether a file descriptor is a terminal. nil
	// uses the term.IsTerminal hook installed in internal/output.
	TTYDetect func(*os.File) bool

	// HTTPFactory builds the *http.Client gplay uses for API calls.
	// It receives the resolved service account so it can compose
	// oauth2.NewClient(ctx, tokenSource) + transport middleware
	// (e.g. scope observer). nil falls back to a base client without
	// OAuth2 wrapping; commands that need a token build it themselves.
	HTTPFactory func(ctx context.Context, sa *serviceaccount.ServiceAccount) (*http.Client, error)

	// ConfigPath is the path to $XDG_CONFIG_HOME/gplay/config.json
	// (or the platform-equivalent).
	ConfigPath string

	// KeystoreRoot is the directory the file-backed keystore uses when
	// the OS keyring is unavailable.
	KeystoreRoot string

	// Keyring is the go-keyring adapter. nil uses the production one.
	Keyring keystore.KeyringAPI
}

// Inputs is the per-cobra-invocation surface: flags + persistent flag
// values cobra has parsed. The cobra wrapper builds an Inputs and
// passes it to kernel.Run; the kernel resolves the Format, the Account,
// and (if HTTPFactory is set) the HTTP client.
type Inputs struct {
	// Ctx is the per-invocation context. Falls back to context.Background
	// when zero. The kernel propagates it through rc.Ctx, the resolver,
	// and the HTTPFactory.
	Ctx context.Context

	// Format is the value of --output. FormatAuto (empty) is fine; the
	// kernel resolves it against TTY state + CI.
	Format output.Format

	// Resolver carries --service-account / --account flag values for
	// resolver.Resolve.
	Resolver resolver.Inputs

	// Verbose mirrors the --verbose persistent flag.
	Verbose bool

	// RequireAccount tells the kernel to bail with the resolver error
	// when no credential resolves. status keeps RequireAccount=false
	// and renders an informational "no active account" message; doctor
	// catches the synthetic check-1 failure itself; login/logout
	// don't need an Account at all.
	RequireAccount bool
}

// RunContext is the post-resolution snapshot a command's business
// function receives.
type RunContext struct {
	Ctx context.Context

	// Account is the resolved credential (per docs/DESIGN.md §1). nil
	// when no credential resolved AND Inputs.RequireAccount was false.
	Account *serviceaccount.ServiceAccount

	// Project is the Android package pinned by the repo's
	// .gplay/config.json, or empty when no pin was found.
	Project string

	// Format is the resolved output Format — never FormatAuto.
	Format output.Format

	// Resolved is the full cascade snapshot for commands that need
	// more than Account/Project (e.g. list reads Resolved.Accounts).
	Resolved *config.Resolved

	// Keystore is the selected backend. label is the renderable name
	// ("keyring" or "file"). Both are resolved once per invocation.
	Keystore      keystore.Backend
	KeystoreLabel string

	// IO streams + transport.
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
	HTTP   *http.Client

	// Log is the verbosity surface. In quiet runs it drops every Info call.
	Log Logger

	// FS is the seam config reads and writes through.
	FS config.FS

	// Verbose mirrors Inputs.Verbose so commands can branch on it
	// without re-reading the flag (e.g. progress bars).
	Verbose bool

	// ConfigPath, KeystoreRoot mirror Boot for commands that write to
	// them (login, logout).
	ConfigPath   string
	KeystoreRoot string
}

// New returns a base RunContext wired from boot+in with NO resolution
// performed. Tests use it to compose a RunContext by hand (set Account,
// Resolved, Keystore, etc. directly) and call a command's Run
// function. Production code uses kernel.Run instead, which performs
// the full resolution.
func New(ctx context.Context, boot Boot, in Inputs) *RunContext {
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
		Log:          newLogger(stderr, in.Verbose),
		Format:       in.Format,
	}
}

// Run resolves Format, Account, Project, HTTP from boot+in, then invokes
// fn with the populated RunContext. If fn returns a non-nil Renderable,
// the kernel renders it to rc.Stdout in rc.Format. fn's error (if any)
// surfaces verbatim so the caller's exit.For mapping stays intact.
func Run(boot Boot, in Inputs, fn func(*RunContext) (output.Renderable, error)) error {
	rc, err := buildRunContext(boot, in)
	if err != nil {
		return err
	}
	payload, runErr := fn(rc)
	// Render even when fn returns an error: doctor wants to print the
	// checklist alongside the failing check. The fn error wins over a
	// render error so exit.For sees the original cause.
	if payload != nil {
		if rerr := output.Render(rc.Stdout, rc.Format, payload.Renderers()); rerr != nil && runErr == nil {
			return rerr
		}
	}
	return runErr
}

// buildRunContext does the work of Run before fn is invoked. Separated
// so command tests can build a RunContext directly when they want to
// skip the rendering step.
func buildRunContext(boot Boot, in Inputs) (*RunContext, error) {
	fsys := boot.FS
	if fsys == nil {
		fsys = config.OSFS{}
	}
	ctx := in.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	rc := &RunContext{
		Ctx:          ctx,
		Stdout:       boot.Stdout,
		Stderr:       boot.Stderr,
		Stdin:        boot.Stdin,
		FS:           fsys,
		Verbose:      in.Verbose,
		ConfigPath:   boot.ConfigPath,
		KeystoreRoot: boot.KeystoreRoot,
		Log:          newLogger(boot.Stderr, in.Verbose),
	}

	// Resolve Format against TTY state + CI.
	format, err := output.Resolve(in.Format, boot.Stdout)
	if err != nil {
		return nil, err
	}
	rc.Format = format

	// Load the cascade — drives Project pin and Account resolution.
	resolved, err := config.LoadFromEnv(ctx, fsys, boot.ConfigPath)
	if err != nil {
		return nil, err
	}
	rc.Resolved = resolved
	rc.Project = resolved.Pin

	// Select the keystore.
	kr := boot.Keyring
	if kr == nil {
		kr = keystore.DefaultKeyring()
	}
	be, label, err := keystore.Select(keystore.SelectOptions{
		Keyring:  kr,
		FileRoot: boot.KeystoreRoot,
	})
	if err != nil {
		return nil, err
	}
	rc.Keystore = be
	rc.KeystoreLabel = label
	if in.Verbose {
		keystore.LogBackend(boot.Stderr, label)
	}

	// Resolve Account. ErrNoSource is tolerated when the command opts
	// out via RequireAccount=false (status / list / login / logout).
	sa, err := resolver.Resolve(ctx, resolver.Deps{Resolved: resolved, Keystore: be}, in.Resolver)
	if err != nil {
		if !in.RequireAccount && errors.Is(err, resolver.ErrNoSource) {
			rc.Account = nil
		} else if in.RequireAccount {
			return nil, err
		}
	} else {
		rc.Account = sa
	}

	// Build HTTP via the factory if one is wired AND we have an Account.
	// Commands without an Account (login, list) get rc.HTTP = nil.
	if boot.HTTPFactory != nil && rc.Account != nil {
		hc, err := boot.HTTPFactory(ctx, rc.Account)
		if err != nil {
			return nil, err
		}
		rc.HTTP = hc
	}

	return rc, nil
}

// FromCobra builds an Inputs from a cobra.Command's flag set, then
// returns a closure-compatible RunE body that wires `fn` into kernel.Run.
// Concrete commands compose this with their own Input-builder:
//
//	RunE: kernel.FromCobra(boot, kernel.Inputs{...}, func(rc) (...) { ... })
//
// For now, FromCobra is the low-level: each command builds Inputs
// itself and calls kernel.Run directly.
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
		},
	}
}

// OAuth2HTTPFactory is the default HTTPFactory: mint a token source from
// sa and return oauth2.NewClient on top of it. Commands that need
// extra middleware (e.g. doctor's transport.ScopeObserver) compose it
// themselves on top of the returned client.
func OAuth2HTTPFactory(ctx context.Context, sa *serviceaccount.ServiceAccount) (*http.Client, error) {
	ts, err := token.Source(ctx, sa)
	if err != nil {
		return nil, err
	}
	return oauth2.NewClient(ctx, ts), nil
}

// --- Logger implementations --------------------------------------------

type stderrLogger struct{ w io.Writer }

func (s stderrLogger) Infof(format string, args ...any) {
	fmt.Fprintf(s.w, format, args...)
}

type quietLogger struct{}

func (quietLogger) Infof(string, ...any) {}

func newLogger(w io.Writer, verbose bool) Logger {
	if verbose {
		return stderrLogger{w: w}
	}
	return quietLogger{}
}
