// Package installskills implements `gplay install-skills`: a flat, category-3
// meta-command (DESIGN §0) that installs the companion agent skills. It exists
// so an agent told to "install gplay" can discover and pull the skills without
// reading the README first (ADR-0028).
//
// The installer runs no third-party package runner and no repository script:
// it asks `git` for one reviewed, pinned commit, copies the skill directories
// out of that checkout, verifies every installed file against it, and rolls the
// whole pack back if anything fails (ADR-0045, superseding the Node-based
// installer of ADR-0028 §2). `git` is the only runtime requirement, so the
// "no Node" pillar now holds for this command too.
//
// A test in this package greps these sources for package-runner names, so the
// "nothing else is executed" promise cannot quietly regress: keep them out.
package installskills

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// RunFunc executes name with args in dir and returns its combined output.
// The whole process boundary is behind this one function so tests drive a
// fixture repo (or an injected failure) instead of the network.
type RunFunc func(ctx context.Context, name string, args []string, dir string) (string, error)

// Options injects the process-boundary dependencies. Production wiring leaves
// them nil and the defaults do the real thing.
type Options struct {
	// LookPath resolves an executable on PATH. Defaults to exec.LookPath.
	LookPath func(file string) (string, error)
	// Run executes git. Defaults to a real exec.CommandContext run.
	Run RunFunc
	// Pin overrides the embedded supply-chain pin. Tests point it at a local
	// fixture repository; nothing in production sets it.
	Pin *Pin
	// HomeDir resolves the user's home directory, used to build the default
	// target. Defaults to os.UserHomeDir.
	HomeDir func() (string, error)
}

func (o Options) withDefaults() Options {
	if o.LookPath == nil {
		o.LookPath = exec.LookPath
	}
	if o.Run == nil {
		o.Run = defaultRun
	}
	if o.HomeDir == nil {
		o.HomeDir = os.UserHomeDir
	}
	return o
}

// defaultRun runs git for real. Output is captured rather than streamed: git's
// progress chatter is noise, and the captured text is what makes a failure
// message actionable.
func defaultRun(ctx context.Context, name string, args []string, dir string) (string, error) {
	c := exec.CommandContext(ctx, name, args...) // #nosec G204 -- name is a PATH-resolved git, args are built from the validated pin
	c.Dir = dir
	// Keep git non-interactive: a credential or SSH prompt on an agent's or a
	// CI's stdin would hang forever instead of failing.
	c.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=")
	out, err := c.CombinedOutput()
	return string(out), err
}

// errGitMissing is the concise one-liner main turns into `gplay: ...`; the
// actionable recipe is printed to stderr first (see run). exit.For maps it to 1
// (generic fallback: no Play-API code fits a missing local dependency).
var errGitMissing = errors.New("git not found on PATH")

// defaultSkillsDir is the standard agent-skills directory, relative to $HOME.
// Both Claude Code and the agents that follow its layout read it, and it is
// user-wide on purpose: skills drive the binary, so they are useful in every
// project, not just the current one (ADR-0028 §2).
var defaultSkillsDir = filepath.Join(".claude", "skills")

// NewCommand returns the cobra command for `gplay install-skills`.
func NewCommand(opts Options) *cobra.Command {
	opts = opts.withDefaults()
	var dir string

	pinNote := "the commit pinned in this binary"
	if p, err := embeddedPin(); err == nil {
		pinNote = fmt.Sprintf("%s@%s", p.Repo, p.Commit)
	}

	cmd := &cobra.Command{
		Use:   "install-skills",
		Short: "Install the gplay companion agent skills (requires git)",
		Long: `Install the companion agent skills that drive gplay from natural-language
prompts.

The skills are fetched with git from ` + pinNote + `

The commit is baked into this binary, never resolved from a branch or a tag, so
two runs of the same gplay version always install the same reviewed files. A new
pack ships through a normal reviewed gplay release. Nothing else is executed:
no Node, no package runner, no script from the skills repository.

Skills are installed user-wide into ~/` + defaultSkillsDir + `, or into the
directory given by --dir. Only the skills of the pack are replaced; anything
else already in that directory is left untouched. Every installed file is
verified against the pinned checkout, and if any part of the install fails the
whole pack is rolled back to its previous state.

Requires git on PATH; nothing else.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd, opts, dir)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "target agent-skills directory (default ~/"+defaultSkillsDir+")")
	return cmd
}

func run(cmd *cobra.Command, opts Options, dir string) error {
	pin, err := resolvePin(opts)
	if err != nil {
		return err
	}
	gitPath, err := lookGit(cmd, opts, pin)
	if err != nil {
		return err
	}
	target, err := resolveTarget(opts, dir)
	if err != nil {
		return err
	}

	// The checkout is disposable and lives in the OS temp dir; only the staging
	// area has to sit next to the target (see stage below).
	work, err := os.MkdirTemp("", "gplay-skills-")
	if err != nil {
		return fmt.Errorf("create work directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	root, err := fetchPinned(cmd.Context(), opts.Run, gitPath, pin, work)
	if err != nil {
		return err
	}
	if err := verifyPack(root, pin); err != nil {
		return err
	}

	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", target, err)
	}
	// Staging and backup directories are created *inside* the target so every
	// move below is a rename within one filesystem: a cross-device rename would
	// fall back to a copy, which is exactly the non-atomic step this design is
	// trying to avoid.
	stage, err := os.MkdirTemp(target, ".gplay-stage-")
	if err != nil {
		return fmt.Errorf("create staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	backup, err := os.MkdirTemp(target, ".gplay-backup-")
	if err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(backup) }()

	// Copy and verify everything before touching the target: a pack that cannot
	// be staged intact never displaces the installed one.
	for _, name := range pin.Skills {
		src, staged := filepath.Join(root, name), filepath.Join(stage, name)
		if err := copyTree(src, staged); err != nil {
			return fmt.Errorf("stage skill %s: %w", name, err)
		}
		if err := verifyTree(src, staged); err != nil {
			return fmt.Errorf("stage skill %s: %w", name, err)
		}
	}

	sw := &swap{target: target, backup: backup}
	for _, name := range pin.Skills {
		if err := sw.install(name, filepath.Join(stage, name)); err != nil {
			return rolledBack(sw, err)
		}
	}
	// Post-install verification, on the real installed paths: the staged copy
	// being right does not prove the swap put it where it belongs.
	for _, name := range pin.Skills {
		if err := verifyTree(filepath.Join(root, name), filepath.Join(target, name)); err != nil {
			return rolledBack(sw, fmt.Errorf("verify installed skill %s: %w", name, err))
		}
	}

	// The files are on disk: a broken stdout (a closed pipe) does not undo the
	// install, so reporting it is best-effort and the exit code stays 0.
	for _, name := range pin.Skills {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), filepath.Join(target, name))
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "installed %d skills from %s@%s into %s\n",
		len(pin.Skills), pin.Repo, pin.Commit[:12], target)
	return nil
}

// rolledBack undoes a failed install and folds any rollback trouble into the
// reported error: a user whose previous skills could not be restored must hear
// about it in the same breath as the failure that caused it.
func rolledBack(sw *swap, cause error) error {
	errs := sw.rollback()
	if len(errs) == 0 {
		return fmt.Errorf("%w (previous skills left untouched)", cause)
	}
	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		msgs = append(msgs, e.Error())
	}
	return fmt.Errorf("%w (rollback incomplete: %s)", cause, strings.Join(msgs, "; "))
}

func resolvePin(opts Options) (Pin, error) {
	if opts.Pin != nil {
		if err := opts.Pin.validate(); err != nil {
			return Pin{}, fmt.Errorf("skills pin: %w", err)
		}
		return *opts.Pin, nil
	}
	return embeddedPin()
}

func resolveTarget(opts Options, dir string) (string, error) {
	if dir != "" {
		return filepath.Clean(dir), nil
	}
	home, err := opts.HomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory (pass --dir): %w", err)
	}
	return filepath.Join(home, defaultSkillsDir), nil
}

func lookGit(cmd *cobra.Command, opts Options, pin Pin) (string, error) {
	gitPath, err := opts.LookPath("git")
	if err == nil {
		return gitPath, nil
	}
	// Only a true "not found" means the dependency is absent. Any other lookup
	// failure (git present but not executable, say) would be misdescribed by the
	// "install git" recipe, so surface the real cause instead.
	if !errors.Is(err, exec.ErrNotFound) {
		return "", fmt.Errorf("resolve git on PATH: %v", err)
	}
	// Leave the agent with what to do, not a dead end. Best-effort write: the
	// exit code is the load-bearing signal.
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
		"git was not found on PATH, and install-skills needs it to fetch the pinned skills.\n"+
			"Install git, then run:\n"+
			"    gplay install-skills\n"+
			"Or browse the skills: https://github.com/%s/tree/%s\n",
		pin.Repo, pin.Commit)
	return "", errGitMissing
}
