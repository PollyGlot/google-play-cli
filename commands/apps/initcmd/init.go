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
	"os"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
)

// Options injects file-system roots so tests can run hermetically against a
// t.TempDir. Production wiring leaves both empty and the command falls back
// to os.Getwd() / os.UserHomeDir() at invocation time.
type Options struct {
	RepoRoot string
	HomeDir  string
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
	return cmd
}

func run(cmd *cobra.Command, opts Options, pkg string) error {
	// --package has no repo-pin fallback here (init *creates* the pin), so a
	// missing value is CLI misuse. We validate in-band rather than via cobra's
	// MarkFlagRequired: that returns a plain error which exit.For maps to the
	// generic exit 1, whereas docs/DESIGN.md §9 classes a missing required flag
	// as exit 2. Returning exit.Usagef keeps us on that documented code and
	// matches how every other command reports a missing required value.
	if pkg == "" {
		return exit.Usagef("--package is required (e.g. --package com.example.myapp)")
	}
	repoRoot, home, err := resolveRoots(opts)
	if err != nil {
		return err
	}
	if err := config.Init(cmd.Context(), config.OSFS{}, repoRoot, home, pkg); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "✓ Pinned package %q for this repo (.gplay/config.json)\n", pkg)
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
		"  hint: run `gplay apps add %s` to register this package under the active Account.\n", pkg)
	return nil
}

func resolveRoots(opts Options) (repoRoot, home string, err error) {
	repoRoot = opts.RepoRoot
	if repoRoot == "" {
		repoRoot, err = os.Getwd()
		if err != nil {
			return "", "", err
		}
	}
	home = opts.HomeDir
	if home == "" {
		home, err = os.UserHomeDir()
		if err != nil {
			return "", "", err
		}
	}
	return repoRoot, home, nil
}
