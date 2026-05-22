// Package kernel owns the per-invocation boot sequence shared by every
// gplay command: building the Boot once, threading it into cobra's
// RunE, and lazily resolving config + keystore + account when a
// command asks for them.
//
// Each command exposes a pure business function with signature
//
//	func Xxx(rc *kernel.RunContext, in XxxInput) (Payload, error)
//
// that the cobra RunE invokes via kernel.FromCobra. RunContext is not
// safe for concurrent use — one cobra invocation, one RunContext.
package kernel

import (
	"context"
	"io"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
)

// Boot carries the process-level wiring built once in cmd/gplay/main.go:
// IO streams, the HTTP client, the keystore adapter, and the paths the
// commands read and write. Nothing here depends on cobra.
type Boot struct {
	// Stdout, Stderr, Stdin default to the process streams. Tests pass
	// bytes.Buffer to capture output.
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	// HTTP is the base client commands use for API calls. nil means
	// "the kernel will use http.DefaultClient when needed"; doctor and
	// future API-touching commands compose oauth2.NewClient(ctx, ts) on
	// top of it.
	HTTP *http.Client

	// ConfigPath is the path to $XDG_CONFIG_HOME/gplay/config.json
	// (or the platform-equivalent).
	ConfigPath string

	// KeystoreRoot is the directory the file-backed keystore uses when
	// the OS keyring is unavailable.
	KeystoreRoot string

	// Keyring is the go-keyring adapter. nil means the default real
	// adapter is used; tests pass a fake so the OS keystore stays out
	// of unit runs.
	Keyring keystore.KeyringAPI

	// FS is the filesystem seam used by internal/config. nil defaults
	// to config.OSFS{}; tests pass an in-memory fake (config.NewMemFS)
	// to keep t.TempDir() out of unit runs.
	FS config.FS
}

// RunContext is the per-invocation snapshot a command's business
// function receives. The kernel populates IO streams and verbose state
// from Boot and lazily resolves Config / Backend / Account on first use.
//
// Lazy resolution keeps commands that don't need a credential (auth list,
// init) from paying for the keystore probe, while commands that do need
// one (login, logout, status, doctor) get the cached value on subsequent
// calls.
type RunContext struct {
	Ctx context.Context

	// IO streams — wired from Boot, overridable by cobra's SetOut/SetErr
	// (the kernel does not bridge cobra itself; the command's RunE pulls
	// cmd.OutOrStdout() / cmd.ErrOrStderr() and assigns them here).
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	// HTTP is the base client; commands that need an oauth2-wrapped one
	// compose it themselves from rc.HTTP and a token source.
	HTTP *http.Client

	// Verbose mirrors --verbose; the kernel uses it to log the keystore
	// label once per process.
	Verbose bool

	// ConfigPath, KeystoreRoot mirror Boot for commands that write to
	// them (login, logout) or need to compute the on-disk credential
	// path (status).
	ConfigPath   string
	KeystoreRoot string

	// FS is the seam config reads and writes through.
	FS config.FS

	// keyring + lazily resolved state.
	keyring      keystore.KeyringAPI
	resolved     *config.Resolved
	backend      keystore.Backend
	backendLabel string
}

// New returns a fresh RunContext wired from boot and ctx. The function
// does no I/O — the cascade and the keystore are loaded only when the
// command asks for them.
func New(ctx context.Context, boot Boot, verbose bool) *RunContext {
	fsys := boot.FS
	if fsys == nil {
		fsys = config.OSFS{}
	}
	return &RunContext{
		Ctx:          ctx,
		Stdout:       boot.Stdout,
		Stderr:       boot.Stderr,
		Stdin:        boot.Stdin,
		HTTP:         boot.HTTP,
		Verbose:      verbose,
		ConfigPath:   boot.ConfigPath,
		KeystoreRoot: boot.KeystoreRoot,
		FS:           fsys,
		keyring:      boot.Keyring,
	}
}

// Config returns the resolved cascade, loaded lazily on first call.
func (rc *RunContext) Config() (*config.Resolved, error) {
	if rc.resolved != nil {
		return rc.resolved, nil
	}
	r, err := config.LoadFromEnv(rc.Ctx, rc.FS, rc.ConfigPath)
	if err != nil {
		return nil, err
	}
	rc.resolved = r
	return r, nil
}

// Backend returns the keystore backend, selected lazily on first call.
// Subsequent calls reuse the cached backend. When Verbose is true the
// label is logged to Stderr at the moment of selection (i.e. exactly
// once per RunContext).
func (rc *RunContext) Backend() (keystore.Backend, string, error) {
	if rc.backend == nil {
		kr := rc.keyring
		if kr == nil {
			kr = keystore.DefaultKeyring()
		}
		be, label, err := keystore.Select(keystore.SelectOptions{
			Keyring:  kr,
			FileRoot: rc.KeystoreRoot,
		})
		if err != nil {
			return nil, "", err
		}
		rc.backend = be
		rc.backendLabel = label
		if rc.Verbose {
			keystore.LogBackend(rc.Stderr, label)
		}
	}
	return rc.backend, rc.backendLabel, nil
}

// ResolveAccount returns the credential per docs/DESIGN.md §1. Both
// Config and Backend are loaded lazily under the hood.
func (rc *RunContext) ResolveAccount(in resolver.Inputs) (*serviceaccount.ServiceAccount, error) {
	resolved, err := rc.Config()
	if err != nil {
		return nil, err
	}
	be, _, err := rc.Backend()
	if err != nil {
		return nil, err
	}
	return resolver.Resolve(rc.Ctx, resolver.Deps{Resolved: resolved, Keystore: be}, in)
}

// FromCobra returns a RunContext wired from cmd's context, the
// inherited --verbose persistent flag, and cmd.OutOrStdout / ErrOrStderr.
// Every command's RunE collapses to this single call instead of
// re-typing the same four lines per command.
func FromCobra(cmd *cobra.Command, boot Boot) *RunContext {
	verbose, _ := cmd.Flags().GetBool("verbose")
	rc := New(cmd.Context(), boot, verbose)
	rc.Stdout = cmd.OutOrStdout()
	rc.Stderr = cmd.ErrOrStderr()
	return rc
}
