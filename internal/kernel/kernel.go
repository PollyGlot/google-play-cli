// Package kernel owns the per-invocation boot sequence shared by every
// gplay command: build the Boot once in cmd/gplay/main.go, the cobra
// layer captures the request-shaped Inputs, and kernel.Run resolves
// Account / Project / Format once before handing a populated
// RunContext to the command's business function.
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
	"os"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
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

	// Account is the resolved credential. nil when nothing resolved
	// (status renders an informational message; doctor synthesises a
	// check-1 failure; login/logout don't need one).
	Account *serviceaccount.ServiceAccount

	// Format is the resolved output Format — never FormatAuto.
	Format output.Format

	// Resolved is the full cascade snapshot. Commands access the
	// Project pin via rc.Resolved.Pin and the accounts list via
	// rc.Resolved.Accounts.
	Resolved *config.Resolved

	// Keystore is the selected backend, resolved once per invocation.
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
	be, label, err := keystore.Select(ctx, keystore.SelectOptions{
		Keyring:  kr,
		FileRoot: boot.KeystoreRoot,
	})
	if err != nil {
		return nil, err
	}
	if in.Verbose {
		keystore.LogBackend(stderr, label)
	}

	// Account resolution is best-effort: any error (no source, missing
	// file, malformed JSON) leaves rc.Account = nil. Commands that
	// need a credential check rc.Account != nil; commands that don't
	// (login, logout, list) proceed unaffected. doctor surfaces the
	// failure via check #1 in its own diagnostic path.
	sa, _ := resolver.Resolve(ctx, resolver.Deps{Resolved: resolved, Keystore: be}, in.Resolver)

	return &RunContext{
		Ctx:           ctx,
		Account:       sa,
		Format:        format,
		Resolved:      resolved,
		Keystore:      be,
		KeystoreLabel: label,
		Stdout:        stdout,
		Stderr:        stderr,
		Stdin:         boot.Stdin,
		FS:            fsys,
		Verbose:       in.Verbose,
		ConfigPath:    boot.ConfigPath,
		KeystoreRoot:  boot.KeystoreRoot,
	}, nil
}

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
