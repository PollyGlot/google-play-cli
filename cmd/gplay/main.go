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

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	stderr := redact.Writer(os.Stderr)

	pathguard.SetNoteWriter(stderr)

	configDir, err := defaultConfigDir()
	if err != nil {
		fmt.Fprintf(stderr, "gplay: %v\n", err)
		os.Exit(1)
	}
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
	root.SetErr(stderr)
	root.SetOut(stdout)

	envelope := func(err error) { writeFailureEnvelope(stdout, stdout.n > 0, os.Args[1:], err) }
	prepareCompletion(root, os.Args[1:])
	kernel.SetArgs(root, os.Args[1:])
	os.Exit(execute(context.Background(), root, stderr, envelope))
}

func execute(parent context.Context, root *cobra.Command, stderr io.Writer, envelope func(error)) int {
	ctx, interrupted, stop := notifyInterrupt(parent)
	defer stop()
	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	sig := interrupted()
	if sig == nil && envelope != nil {
		envelope(err)
	}
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
		Args:          cobra.ArbitraryArgs,
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.SetFlagErrorFunc(kernel.FlagErrorFunc)

	var (
		serviceAccountFlag string
		accountFlag        string
		verbose            bool
	)
	root.PersistentFlags().StringVar(&serviceAccountFlag, "service-account", "",
		"path to a service-account JSON, or inline JSON content (overrides --account, env, and active Account)")
	root.PersistentFlags().StringVar(&accountFlag, "account", "",
		"name of a stored Account to use (overrides env and active Account)")
	root.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "log flow steps to stderr (info level)")

	root.PersistentFlags().Duration("timeout", 0,
		"per-request API timeout, e.g. 30s or 2m (default: 60s for control-plane calls, none for uploads)")

	root.PersistentFlags().Int("retry", 0,
		"retry transient failures (transport errors, 5xx, 429) up to N times with exponential backoff, replaying only idempotent requests; Retry-After is capped at the 30s max delay (default: 0, no retry)")

	root.AddCommand(
		newAuthGroup(boot),
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
		newReportsGroup(boot),
		newVitalsGroup(boot),
		newMetadataGroup(boot),
		newComplianceGroup(boot),
		kernel.Experimental(schemacmd.NewCommand(boot)),
		helpexitcodes.NewCommand(),
		installskills.NewCommand(installskills.Options{}),
		newVersionCmd(),
	)

	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()

	return kernel.WrapArgErrors(kernel.RejectRepeatedFlags(root))
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Print gplay version",
		Example: "  gplay version",
		Args:    cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			info, ok := debug.ReadBuildInfo()
			v, c, d := resolveVersion(version, commit, date, info, ok)
			fmt.Fprintf(cmd.OutOrStdout(), "gplay %s (%s, %s)\n", v, c, d)
		},
	}
}

func resolveVersion(ldVersion, ldCommit, ldDate string, info *debug.BuildInfo, infoOK bool) (string, string, string) {
	if ldVersion != "dev" {
		return ldVersion, ldCommit, ldDate
	}
	if !infoOK || info == nil {
		return ldVersion, ldCommit, ldDate
	}
	v, c, d := ldVersion, ldCommit, ldDate
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

func prepareCompletion(root *cobra.Command, args []string) {
	if len(args) > 0 && (args[0] == cobra.ShellCompRequestCmd || args[0] == cobra.ShellCompNoDescRequestCmd) {
		kernel.AllowRepeatedFlags(root)
	}
}
