package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/apps/initcmd"
	helpexitcodes "github.com/PollyGlot/google-play-cli/commands/help/exitcodes"
	"github.com/PollyGlot/google-play-cli/commands/installskills"
	schemacmd "github.com/PollyGlot/google-play-cli/commands/schema"
	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/pathguard"
	"github.com/PollyGlot/google-play-cli/internal/redact"
)

// Build-time variables injected by GoReleaser via -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	// One redacting writer for the whole process (PRD #459 / slice #460): every
	// byte gplay writes to stderr passes through it, so credential material
	// cannot leak into a CI log a human or agent then pastes elsewhere. Stdout
	// is deliberately NOT wrapped: it mirrors API responses verbatim (ADR-0003),
	// and the API never returns gplay's own credentials. gplay's own text on
	// stdout (error envelope, doctor hints) is masked at its source (#583).
	stderr := redact.Writer(os.Stderr)

	// pathguard reports a path that left the tree under
	// GPLAY_ALLOW_EXTERNAL_SYMLINKS; point it at the same redacted stderr so
	// the NOTE lands with every other log line and never on stdout (ADR-0003).
	pathguard.SetNoteWriter(stderr)

	configDir, err := defaultConfigDir()
	if err != nil {
		fmt.Fprintf(stderr, "gplay: %v\n", err)
		os.Exit(1)
	}
	// Counted stdout: writeFailureEnvelope below needs to know whether a
	// failing command already wrote to it. SetOut hands the same writer down
	// the tree, like SetErr does for stderr.
	stdout := &stdoutTally{f: os.Stdout}
	boot := kernel.Boot{
		Stdout:       stdout,
		Stderr:       stderr,
		Stdin:        os.Stdin,
		ConfigPath:   filepath.Join(configDir, "config.json"),
		KeystoreRoot: filepath.Join(configDir, "accounts"),
		Keyring:      keystore.DefaultKeyring(),
	}

	root := newRootCmd(boot)
	// boot.Stderr alone is not enough: RunCobra re-reads the writer from cobra
	// (cmd.ErrOrStderr()), and several commands re-wire boot.Stderr the same way
	// before a sub-run. SetErr on the root makes the redacting writer the one
	// cobra hands down the whole command tree, including leaves added later.
	root.SetErr(stderr)
	root.SetOut(stdout)

	// A failure cobra raised before RunE (flag parse, argument count, unknown
	// subcommand) never met the kernel's JSON envelope: write it once Execute
	// fails, when nothing else reached stdout (ADR-0023, #593).
	envelope := func(err error) { writeFailureEnvelope(stdout, stdout.n > 0, os.Args[1:], err) }
	os.Exit(execute(context.Background(), root, stderr, envelope))
}

// execute runs root under a context that SIGINT and SIGTERM cancel, and
// returns the process exit code. On a failure that was not an interrupt it
// calls envelope (when non-nil) before the stderr line; an interrupt exits
// 50 whatever the error, so an envelope there could only contradict it.
//
// Cancellation is what lets an interrupted command clean up: every request
// fails fast on the canceled context and the Edit lifecycle discards the
// implicit Edit on its own short bound (internal/play/edits), where a plain
// kill used to leave it open server-side, blocking the next publish for up to
// 24h. A second signal is not caught: it terminates gplay at once.
func execute(parent context.Context, root *cobra.Command, stderr io.Writer, envelope func(error)) int {
	ctx, interrupted, stop := notifyInterrupt(parent)
	defer stop()
	err := root.ExecuteContext(ctx)
	if err == nil {
		// A command that finished despite a late signal did its work: its
		// success is the truth, and a retry would only redo it.
		return 0
	}
	sig := interrupted()
	if sig == nil && envelope != nil {
		envelope(err)
	}
	// Subcommands set SilenceErrors:true on their cobra Command so the
	// stack-trace-style "Error: ..." cobra would emit is suppressed, but we
	// still owe the user a one-line message before exiting, otherwise the only
	// signal is the exit code (which CI sees, but a human running gplay in a
	// terminal does not).
	fmt.Fprintln(stderr, "gplay:", err)
	if sig != nil {
		fmt.Fprintf(stderr, "gplay: interrupted by %s\n", signalName(sig))
		return exitInterrupted
	}
	return exit.For(err)
}

func newRootCmd(boot kernel.Boot) *cobra.Command {
	root := &cobra.Command{
		Use:   "gplay",
		Short: "Google Play Developer CLI",
		Long: `gplay: fast, lightweight CLI for the Google Play Developer API.

Reads service-account credentials, mints OAuth2 tokens, and drives the
publishing surface (releases, tracks, reviews, metadata, compliance,
team). Designed to replace Fastlane on Android CI pipelines.`,
		// The root is a grouping noun like any other (kernel.GroupRunE): bare
		// `gplay` prints help, `gplay <unknown>` is CLI misuse (exit 2), one
		// clean `gplay: ...` line (SilenceErrors). Args:ArbitraryArgs routes
		// the unknown-command case through GroupRunE rather than cobra's
		// legacyArgs, which would emit a plain error (exit 1) and a second
		// "Error: ..." line: the inconsistency this harmonisation removes.
		Args:          cobra.ArbitraryArgs,
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Flag-parse failures (unknown flag, bad value) reach us as plain cobra
	// errors, which exit.For would map to the generic exit 1, but
	// docs/DESIGN.md §9 classes them as CLI misuse (exit 2), the same bucket
	// as the unknown-subcommand case GroupRunE already covers. Wrapping the
	// error in exit.Usagef (a *exit.UsageError, ExitCode()=2) at the root fixes
	// the whole tree at once: cobra's FlagErrorFunc is inherited down the parent
	// chain, so every leaf's parse error routes through here. SilenceErrors on
	// the root keeps the output to main's single "gplay: ..." line.
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return exit.Usagef("%s", err)
	})

	// Persistent credential-resolution flags (docs/DESIGN.md §1). Every
	// subcommand inherits these via the cobra parent chain: login reads
	// the same --service-account everyone else does, so the contract stays
	// consistent across the binary.
	var (
		serviceAccountFlag string
		accountFlag        string
		verbose            bool
	)
	root.PersistentFlags().StringVar(&serviceAccountFlag, "service-account", "",
		"path to a service-account JSON, or inline JSON content (overrides --account, env, and active Account)")
	root.PersistentFlags().StringVar(&accountFlag, "account", "",
		"name of a stored Account to use (overrides env and active Account)")
	// Persistent verbosity flag (docs/DESIGN.md §8). Subcommands read it via
	// the inherited PersistentFlags so a single `-v` works at any position:
	//   gplay -v auth status   (CI-friendly: option before subcommand)
	//   gplay auth status -v   (interactive-friendly: option after)
	root.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "log flow steps to stderr (info level)")

	// Global per-request timeout (docs/DESIGN.md §8). Zero (the default) means
	// the kernel applies a 60s deadline to control-plane API calls and leaves
	// media uploads unbounded; an explicit value bounds every request,
	// including uploads. The kernel reads it via FromCobra → Inputs.Timeout.
	root.PersistentFlags().Duration("timeout", 0,
		"per-request API timeout, e.g. 30s or 2m (default: 60s for control-plane calls, none for uploads)")

	// Global opt-in retry (docs/CI_CD.md §4). Default 0 = today's behavior (no
	// retry). When > 0 the kernel layers a retry transport that retries
	// transport errors / 5xx / 429 (honoring Retry-After up to the max delay)
	// with exponential backoff + jitter, replaying only idempotent requests
	// (#575); --timeout then bounds each attempt. The kernel reads it via
	// FromCobra → Inputs.Retry.
	root.PersistentFlags().Int("retry", 0,
		"retry transient failures (transport errors, 5xx, 429) up to N times with exponential backoff, replaying only idempotent requests; Retry-After is capped at the 30s max delay (default: 0, no retry)")

	root.AddCommand(
		newAuthGroup(boot),
		// `gplay init` at the top level: pins a package to the current repo.
		// Also wired as `gplay apps init` so both forms are discoverable.
		initcmd.NewCommand(initcmd.Options{}),
		newAppsGroup(boot),
		newReleasesGroup(boot),
		newTracksGroup(boot),
		newTestersGroup(boot),
		newDeviceTiersGroup(boot),
		newRecoveryGroup(boot),
		newSigningGroup(boot),
		newTeamGroup(boot),
		newCustomAppsGroup(boot),
		newAppStoreGroup(boot),
		newEditsGroup(boot),
		newGamesGroup(boot),
		newOrdersGroup(boot),
		newSubscriptionsGroup(boot),
		newIAPGroup(boot),
		newReviewsGroup(boot),
		newVitalsGroup(boot),
		newMetadataGroup(boot),
		newComplianceGroup(boot),
		// `gplay schema`: OFFLINE, no-auth, `[experimental]` introspection of
		// the Android Publisher API surface from an embedded Schema index
		// (ADR-0022). A top-level reference/diagnostic meta-command (ADR-0019),
		// not keyed by a package or the Developer account. See PRD #199.
		// [experimental] (ADR-0010/ADR-0042): its --output json projection of
		// the index is a shape gplay invented, not one Google owns, and it is the
		// likeliest to change.
		kernel.Experimental(schemacmd.NewCommand(boot)),
		// `gplay exit-codes` / `gplay help exit-codes`: the semantic exit-code
		// taxonomy (docs/DESIGN.md §9), built from internal/exit so it cannot
		// drift from the codes the binary actually returns.
		helpexitcodes.NewCommand(),
		// `gplay install-skills`: install the companion agent skills from the
		// git commit pinned in this binary (ADR-0045, superseding the
		// package-runner installer of ADR-0028 / #266). A flat category-3
		// meta-command; `git` is its only runtime requirement, so the "no Node"
		// pillar holds without a carve-out. Surfaced in root --help so an agent
		// told to "install gplay" can discover it.
		installskills.NewCommand(installskills.Options{}),
		newVersionCmd(),
	)

	// Third and last door into CLI misuse: a wrong number of POSITIONAL
	// arguments. cobra runs a command's Args validator inside execute() and
	// hands its error straight back: it never passes the FlagErrorFunc above,
	// so it reached exit.For untyped and fell back to the generic exit 1 while
	// the other two doors returned the documented exit 2 (#426). One walk over
	// the assembled tree re-types every Args rejection as usage, so the whole
	// binary (including leaves added later) agrees on docs/DESIGN.md §9. It is
	// the LAST statement on purpose: it wraps what is registered when it runs.
	//
	// cobra materialises its default `help` and `completion` commands lazily
	// inside Execute (after this function returns), which would leave their
	// own Args validators outside the walk (`gplay completion bash extra`
	// stuck on exit 1). Materialise them first; both calls are no-ops when the
	// command already exists, so Execute's own late init stays harmless. The
	// hidden `__complete` plumbing command has no pre-Execute hook and stays
	// outside the walk: shell-integration plumbing, not command surface.
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()

	// Fourth door: the SAME single-value flag passed twice. pflag's default is
	// last-wins and silent, which for an agent-assembled argv means a mis-ship
	// nobody sees (PRD #446). RejectRepeatedFlags re-types it as a parse error,
	// which the FlagErrorFunc above already maps to exit 2, so the four misuse
	// doors finally agree. Like WrapArgErrors it walks the assembled tree, so it
	// belongs here at the end, and genuine repeatable flags (pflag.SliceValue)
	// are left alone.
	return kernel.WrapArgErrors(kernel.RejectRepeatedFlags(root))
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print gplay version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			info, ok := debug.ReadBuildInfo()
			v, c, d := resolveVersion(version, commit, date, info, ok)
			fmt.Fprintf(cmd.OutOrStdout(), "gplay %s (%s, %s)\n", v, c, d)
		},
	}
}

// resolveVersion picks the best (version, commit, date) triple to print.
//
// GoReleaser injects ldflag values at build time. `go install <module>@<tag>`
// skips those ldflags, so we fall back to debug.BuildInfo: Main.Version carries
// the module pseudo/tagged version, and the vcs.* settings carry the commit and
// commit time. Ldflag-injected values stay authoritative when present so
// GoReleaser and Homebrew builds aren't affected.
func resolveVersion(ldVersion, ldCommit, ldDate string, info *debug.BuildInfo, infoOK bool) (string, string, string) {
	if ldVersion != "dev" {
		return ldVersion, ldCommit, ldDate
	}
	if !infoOK || info == nil {
		return ldVersion, ldCommit, ldDate
	}
	v, c, d := ldVersion, ldCommit, ldDate
	// "(devel)" is what BuildInfo reports for unversioned local builds, not useful.
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		v = info.Main.Version
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if s.Value != "" {
				c = s.Value
			}
		case "vcs.time":
			if s.Value != "" {
				d = s.Value
			}
		}
	}
	return v, c, d
}

// defaultConfigDir returns the canonical gplay config directory per the PRD:
//
//   - Linux:        $XDG_CONFIG_HOME/gplay (or ~/.config/gplay)
//   - macOS, Win:   ~/.gplay (deliberately NOT os.UserConfigDir's
//     ~/Library/Application Support or %AppData%: gplay sits next to
//     other dotfile-style dev tooling)
func defaultConfigDir() (string, error) {
	if runtime.GOOS == "linux" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "gplay"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gplay"), nil
}
