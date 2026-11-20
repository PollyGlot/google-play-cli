package installskills

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/gitenv"
)

type RunFunc func(ctx context.Context, name string, args []string, dir string) (string, error)

type Options struct {
	LookPath func(file string) (string, error)
	Run      RunFunc
	Pin      *Pin
	HomeDir  func() (string, error)
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

func defaultRun(ctx context.Context, name string, args []string, dir string) (string, error) {
	c := exec.CommandContext(ctx, name, args...) // #nosec G204 -- name is a PATH-resolved git, args are built from the validated pin
	c.Dir = dir
	c.Env = append(gitenv.Safe(os.Environ()), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=")
	out, err := c.CombinedOutput()
	return string(out), err
}

var errGitMissing = errors.New("git not found on PATH")

var defaultSkillsDir = filepath.Join(".claude", "skills")

var legacyFlags = []struct{ name, kind string }{
	{"agent", "string"},
	{"project", "bool"},
	{"global", "bool"},
	{"yes", "bool"},
}

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
pack ships with the next gplay release, so this pack can lag one release behind
the skills repository, whose README gives the install of the current skills.
Nothing else is executed:
no Node, no package runner, no script from the skills repository, and no git
hook (git runs with hooks and init templates disabled).

Skills are installed user-wide into ~/` + defaultSkillsDir + `, or into the
directory given by --dir. Only the skills of the pack are replaced; anything
else already in that directory is left untouched. Every installed file is
verified against the pinned checkout, and if any part of the install fails the
whole pack is rolled back to its previous state.

Requires git on PATH; nothing else.

The passthrough flags of the former installer (--agent, --project, --global,
--yes) are deprecated: they are accepted for compatibility, ignored, and warned
about on stderr. The pack is always installed whole, for every agent reading
the target directory.`,
		Example: `  # Install the pinned skill pack into ~/.claude/skills
  gplay install-skills

  # Install it into a project's own skills directory instead
  gplay install-skills --dir .claude/skills`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			for _, f := range legacyFlags {
				if cmd.Flags().Changed(f.name) {
					kernel.LoggerFor(cmd).Warnf(
						"--%s is deprecated and ignored: install-skills no longer forwards to the skills package runner (ADR-0045)", f.name)
				}
			}
			return run(cmd, opts, dir)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "target agent-skills directory (default ~/"+defaultSkillsDir+")")
	for _, f := range legacyFlags {
		if f.kind == "string" {
			cmd.Flags().String(f.name, "", "")
		} else {
			cmd.Flags().Bool(f.name, false, "")
		}
		_ = cmd.Flags().MarkHidden(f.name)
	}
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

	for _, e := range sweepOrphans(target) {
		kernel.LoggerFor(cmd).Warnf("%v", e)
	}

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
	stage, err := os.MkdirTemp(target, stagePrefix)
	if err != nil {
		return fmt.Errorf("create staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	backup, err := os.MkdirTemp(target, backupPrefix)
	if err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	sw := &swap{target: target, backup: backup}
	defer func() {
		if sw.incomplete {
			return
		}
		_ = os.RemoveAll(backup)
	}()

	for _, name := range pin.Skills {
		src, staged := filepath.Join(root, name), filepath.Join(stage, name)
		if err := copyTree(src, staged); err != nil {
			return fmt.Errorf("stage skill %s: %w", name, err)
		}
		if err := verifyTree(src, staged); err != nil {
			return fmt.Errorf("stage skill %s: %w", name, err)
		}
	}

	for _, name := range pin.Skills {
		if err := sw.install(name, filepath.Join(stage, name)); err != nil {
			return rolledBack(sw, err)
		}
	}
	for _, name := range pin.Skills {
		if err := verifyTree(filepath.Join(root, name), filepath.Join(target, name)); err != nil {
			return rolledBack(sw, fmt.Errorf("verify installed skill %s: %w", name, err))
		}
	}

	for _, name := range pin.Skills {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), filepath.Join(target, name))
	}
	kernel.LoggerFor(cmd).Logf("installed %d skills from %s@%s into %s",
		len(pin.Skills), pin.Repo, pin.Commit[:12], target)
	return nil
}

func rolledBack(sw *swap, cause error) error {
	errs := sw.rollback()
	if len(errs) == 0 {
		return fmt.Errorf("%w (previous skills left untouched)", cause)
	}
	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		msgs = append(msgs, e.Error())
	}
	return fmt.Errorf("%w (rollback incomplete: %s; your previous skills are kept in %s)",
		cause, strings.Join(msgs, "; "), sw.backup)
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
	if !errors.Is(err, exec.ErrNotFound) {
		return "", fmt.Errorf("resolve git on PATH: %w", err)
	}
	kernel.LoggerFor(cmd).Failf(
		"git was not found on PATH, and install-skills needs it to fetch the pinned skills.\n"+
			"Install git, then run:\n"+
			"    gplay install-skills\n"+
			"Or browse the skills: https://github.com/%s/tree/%s",
		pin.Repo, pin.Commit)
	return "", errGitMissing
}
