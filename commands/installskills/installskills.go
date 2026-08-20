// Package installskills implements `gplay install-skills`: a flat, category-3
// meta-command (DESIGN §0) that installs the companion agent skills by shelling
// out to `npx skills add` (ADR-0028). It exists so an agent told to "install
// gplay" can discover and pull the skills without reading the README first.
//
// This is the ONE gplay command that needs Node/npx. That does not breach the
// "no Node" pillar: the pillar is scoped to the CI/runtime path (driving the
// Play API needs zero runtime deps), and `install-skills` is a dev-workstation
// setup convenience that never runs in CI. Node was already required to install
// skills via `npx skills add` regardless: this wraps that one dependency, it
// adds none (ADR-0028 §3). The `--help` text states the requirement plainly.
package installskills

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"

	"github.com/spf13/cobra"
)

// skillsRepo is the companion-skills source the recipe installs from.
const skillsRepo = "PollyGlot/google-play-cli-skills"

// skillsBrowseURL is where an agent without Node can read the skills by hand.
const skillsBrowseURL = "https://github.com/" + skillsRepo

// recipeArgs is the opinionated, non-interactive npx argv a bare
// `gplay install-skills` runs (ADR-0028 §2). It is returned fresh each call so
// appending passthrough args never aliases a shared backing array.
//
//		npx --yes skills add PollyGlot/google-play-cli-skills --global --agent '*' --yes
//
//	  - --global    skills drive the binary, useful in every project, not just CWD
//	  - --agent '*' all detected agents: gplay is agent-agnostic (CLAUDE.md + AGENTS.md)
//	  - --yes (x2)  non-interactive, required by DESIGN's no-prompts rule
func recipeArgs() []string {
	return []string{
		"--yes", "skills", "add", skillsRepo,
		"--global", "--agent", "*", "--yes",
	}
}

// RunFunc executes the resolved npx with argv, wiring the command's streams.
type RunFunc func(ctx context.Context, npxPath string, args []string, stdin io.Reader, stdout, stderr io.Writer) error

// Options injects the two process-boundary dependencies so tests run without a
// real npx or network (ADR-0028 acceptance: mock exec/LookPath).
// Production wiring leaves both nil and the defaults shell out for real.
type Options struct {
	// LookPath resolves an executable on PATH. Defaults to exec.LookPath.
	LookPath func(file string) (string, error)
	// Run executes npx. Defaults to a real exec.CommandContext run.
	Run RunFunc
}

func (o Options) withDefaults() Options {
	if o.LookPath == nil {
		o.LookPath = exec.LookPath
	}
	if o.Run == nil {
		o.Run = defaultRun
	}
	return o
}

// defaultRun runs npx for real, streaming the child's stdio through gplay's so
// the user sees the skills CLI's progress and prompts-free output live.
func defaultRun(ctx context.Context, npxPath string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	c := exec.CommandContext(ctx, npxPath, args...)
	c.Stdin = stdin
	c.Stdout = stdout
	c.Stderr = stderr
	return c.Run()
}

// errNpxMissing is the concise one-liner main turns into `gplay: ...`; the rich,
// actionable recipe is printed to stderr first (see run). exit.For maps it to 1
// (generic fallback: no Play-API code fits a missing local dep, ADR-0028 §4).
var errNpxMissing = errors.New("npx (Node.js) not found on PATH")

// NewCommand returns the cobra command for `gplay install-skills`.
func NewCommand(opts Options) *cobra.Command {
	opts = opts.withDefaults()
	return &cobra.Command{
		Use:   "install-skills",
		Short: "Install the gplay companion agent skills (requires Node/npx)",
		Long: `Install the companion agent skills that drive gplay from natural-language
prompts, by shelling out to the skills CLI:

    npx skills add ` + skillsRepo + ` --global --agent '*' --yes

Skills are installed --global (they drive the binary, useful in every project)
and for every detected agent. Extra arguments pass through to ` + "`npx skills add`" + `
for overrides, e.g.:

    gplay install-skills --agent claude     # one agent only
    gplay install-skills --project          # this repo only, not --global

Requires Node.js / npx on PATH: this is the one gplay command that does. It is
a dev-workstation convenience and never runs on the CI/runtime path, so the
"no Node" promise for driving the Play API still holds (ADR-0028).`,
		// Everything after `install-skills` passes through verbatim to
		// `npx skills add`. DisableFlagParsing stops cobra from claiming flags
		// like --agent/--project as its own; -h/--help is honored by hand below
		// (cobra does not auto-handle it once flag parsing is off).
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		SilenceUsage:       true,
		SilenceErrors:      true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if wantsHelp(args) {
				return cmd.Help()
			}
			return run(cmd, opts, args)
		},
	}
}

// wantsHelp reports whether the passthrough args ask for help. With flag parsing
// off, -h/--help reach us as plain args; we intercept them so the help text
// (which states the Node/npx requirement) stays reachable.
func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}

func run(cmd *cobra.Command, opts Options, passthrough []string) error {
	npxPath, err := opts.LookPath("npx")
	if err != nil {
		// Only a true "not found" means the dependency is absent. Any other
		// lookup failure (e.g. npx present but not executable) is a different
		// problem: the "install Node.js" recipe would mislead, so surface the
		// real cause instead (still a generic exit 1, no Coder in the chain).
		if !errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("resolve npx on PATH: %v", err)
		}
		// npx absent: print the manual recipe so an agent leaves with what to do,
		// then exit 1 (ADR-0028 §4). The stderr write is best-effort: the exit
		// code is the load-bearing signal.
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"npx requires Node.js, which was not found.\n"+
				"Install Node.js, then run:\n"+
				"    npx skills add %s --global --agent '*' --yes\n"+
				"Or browse the skills: %s\n",
			skillsRepo, skillsBrowseURL)
		return errNpxMissing
	}
	args := append(recipeArgs(), passthrough...)
	if err := opts.Run(cmd.Context(), npxPath, args, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
		// npx/skills failures are opaque; npx already wrote its own diagnostics to
		// the wired stderr. Surface a generic exit 1 (ADR-0028 §4).
		//
		// %v, not %w, is deliberate: a real failure is an *exec.ExitError, which
		// promotes ExitCode() from os.ProcessState and so satisfies exit.Coder.
		// Wrapping with %w would let exit.For find it and leak the child's exit
		// code (e.g. 7) as gplay's. Breaking the chain keeps the message but maps
		// to the documented generic 1.
		return fmt.Errorf("npx skills add failed: %v", err)
	}
	return nil
}
