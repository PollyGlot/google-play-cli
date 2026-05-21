// Package initcmd implements `gplay init`: pin a package name to the
// current repo by writing .gplay/config.json (committed) and an adjacent
// .gplay/.gitignore. Subsequent gplay invocations anywhere inside the
// tree resolve --package automatically via walk-up.
//
// The command is wired both as `gplay init` at the top level (matching
// other CLIs' conventions) and reachable from `gplay apps init`.
package initcmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/config"
)

// Options injects file-system roots so the test fixtures can run
// hermetically against a t.TempDir without touching the user's real home.
type Options struct {
	// RepoRoot is the directory in which to write .gplay/config.json.
	// Production wiring sets this to cwd; tests inject a tempdir.
	RepoRoot string
	// HomeDir is the user's home directory. The command refuses to write
	// when RepoRoot == HomeDir.
	HomeDir string
}

// NewCommand returns the cobra command for `gplay init` and `gplay apps init`.
func NewCommand(opts Options) *cobra.Command {
	var pkg string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Pin a Google Play package to the current repo",
		Long: `Write .gplay/config.json with the given Android package name so every
subsequent gplay command in this directory tree picks it up automatically
via walk-up. Also creates .gplay/.gitignore so per-developer overrides
(config.local.json) and transient edit-ID files stay out of git.

Run from the repo root.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd, opts, pkg)
		},
	}
	cmd.Flags().StringVar(&pkg, "package", "", "Android package name (e.g. com.example.myapp). Required.")
	_ = cmd.MarkFlagRequired("package")
	return cmd
}

func run(cmd *cobra.Command, opts Options, pkg string) error {
	if err := config.Init(opts.RepoRoot, opts.HomeDir, pkg); err != nil {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "✓ Pinned package %q for this repo (.gplay/config.json)\n", pkg)

	// Registry membership hint. Until `gplay apps add` lands, the registry
	// is always empty, so the hint always fires — that's intentional and
	// fine for the first slice. ([slice 2] makes the registry populatable.)
	fmt.Fprintf(cmd.ErrOrStderr(),
		"  hint: run `gplay apps add %s` to register this package under the active Account.\n", pkg)
	return nil
}
