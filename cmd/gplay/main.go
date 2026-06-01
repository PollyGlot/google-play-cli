package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/apps/addcmd"
	"github.com/PollyGlot/google-play-cli/commands/apps/infocmd"
	"github.com/PollyGlot/google-play-cli/commands/apps/initcmd"
	"github.com/PollyGlot/google-play-cli/commands/apps/listcmd"
	"github.com/PollyGlot/google-play-cli/commands/apps/removecmd"
	"github.com/PollyGlot/google-play-cli/commands/auth/doctor"
	"github.com/PollyGlot/google-play-cli/commands/auth/list"
	"github.com/PollyGlot/google-play-cli/commands/auth/login"
	"github.com/PollyGlot/google-play-cli/commands/auth/logout"
	"github.com/PollyGlot/google-play-cli/commands/auth/status"
	metadataapply "github.com/PollyGlot/google-play-cli/commands/metadata/apply"
	metadatalist "github.com/PollyGlot/google-play-cli/commands/metadata/list"
	metadatapull "github.com/PollyGlot/google-play-cli/commands/metadata/pull"
	metadatavalidate "github.com/PollyGlot/google-play-cli/commands/metadata/validate"
	releaseslist "github.com/PollyGlot/google-play-cli/commands/releases/list"
	"github.com/PollyGlot/google-play-cli/commands/releases/promote"
	"github.com/PollyGlot/google-play-cli/commands/releases/rollout"
	"github.com/PollyGlot/google-play-cli/commands/releases/upload"
	reviewslist "github.com/PollyGlot/google-play-cli/commands/reviews/list"
	reviewsreply "github.com/PollyGlot/google-play-cli/commands/reviews/reply"
	trackslist "github.com/PollyGlot/google-play-cli/commands/tracks/list"
	tracksstatus "github.com/PollyGlot/google-play-cli/commands/tracks/status"
	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// Build-time variables injected by GoReleaser via -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	configDir, err := defaultConfigDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gplay: %v\n", err)
		os.Exit(1)
	}
	boot := kernel.Boot{
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
		Stdin:        os.Stdin,
		ConfigPath:   filepath.Join(configDir, "config.json"),
		KeystoreRoot: filepath.Join(configDir, "accounts"),
		Keyring:      keystore.DefaultKeyring(),
	}

	if err := newRootCmd(boot).Execute(); err != nil {
		// Subcommands set SilenceErrors:true on their cobra Command so the
		// stack-trace-style "Error: ..." cobra would emit is suppressed —
		// but we still owe the user a one-line message before exiting,
		// otherwise the only signal is the exit code (which CI sees, but
		// a human running gplay in a terminal does not).
		fmt.Fprintln(os.Stderr, "gplay:", err)
		os.Exit(exit.For(err))
	}
}

func newRootCmd(boot kernel.Boot) *cobra.Command {
	root := &cobra.Command{
		Use:   "gplay",
		Short: "Google Play Developer CLI",
		Long: `gplay — fast, lightweight CLI for the Google Play Developer API.

Reads service-account credentials, mints OAuth2 tokens, and drives the
publishing surface (releases, tracks, reviews, vitals). Designed to
replace Fastlane on Android CI pipelines.`,
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	// Persistent credential-resolution flags (docs/DESIGN.md §1). Every
	// subcommand inherits these via the cobra parent chain — login reads
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

	auth := &cobra.Command{
		Use:   "auth",
		Short: "Manage gplay credentials",
	}
	auth.AddCommand(login.NewCommand(boot))
	auth.AddCommand(logout.NewCommand(boot))
	auth.AddCommand(status.NewCommand(boot))
	auth.AddCommand(list.NewCommand(boot))
	auth.AddCommand(doctor.NewCommand(boot))
	root.AddCommand(auth)

	// `gplay init` at the top level — pins a package to the current repo.
	// Also wired as `gplay apps init` below so both forms are discoverable.
	root.AddCommand(initcmd.NewCommand(initcmd.Options{}))

	apps := &cobra.Command{
		Use:   "apps",
		Short: "Manage Android packages registered with gplay",
	}
	apps.AddCommand(initcmd.NewCommand(initcmd.Options{}))
	apps.AddCommand(addcmd.NewCommand(boot))
	apps.AddCommand(listcmd.NewCommand(boot))
	apps.AddCommand(infocmd.NewCommand(boot))
	apps.AddCommand(removecmd.NewCommand(boot))
	root.AddCommand(apps)

	releases := &cobra.Command{
		Use:   "releases",
		Short: "Manage app releases (upload, promote, rollout)",
	}
	releases.AddCommand(upload.NewCommand(boot))
	releases.AddCommand(promote.NewCommand(boot))
	releases.AddCommand(rollout.NewRolloutCommand(boot))
	releases.AddCommand(rollout.NewHaltCommand(boot))
	releases.AddCommand(rollout.NewResumeCommand(boot))
	releases.AddCommand(rollout.NewCompleteCommand(boot))
	releases.AddCommand(releaseslist.NewCommand(boot))
	root.AddCommand(releases)

	tracks := &cobra.Command{
		Use:   "tracks",
		Short: "Inspect release tracks (standard and custom closed)",
	}
	tracks.AddCommand(trackslist.NewCommand(boot))
	tracks.AddCommand(tracksstatus.NewCommand(boot))
	root.AddCommand(tracks)

	reviews := &cobra.Command{
		Use:   "reviews",
		Short: "Read and reply to user reviews",
	}
	reviews.AddCommand(reviewslist.NewCommand(boot))
	reviews.AddCommand(reviewsreply.NewCommand(boot))
	root.AddCommand(reviews)

	// `gplay metadata` — Store front Listings (per-locale text), the
	// fastlane-supply text side. See PRD #50 / ADR-0011.
	metadata := &cobra.Command{
		Use:   "metadata",
		Short: "Manage Store front Listings (per-locale title/description/video)",
	}
	metadata.AddCommand(metadatalist.NewCommand(boot))
	metadata.AddCommand(metadatapull.NewCommand(boot))
	metadata.AddCommand(metadatavalidate.NewCommand(boot))
	metadata.AddCommand(metadataapply.NewCommand(boot))
	root.AddCommand(metadata)

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print gplay version",
		Run: func(cmd *cobra.Command, _ []string) {
			info, ok := debug.ReadBuildInfo()
			v, c, d := resolveVersion(version, commit, date, info, ok)
			fmt.Fprintf(cmd.OutOrStdout(), "gplay %s (%s, %s)\n", v, c, d)
		},
	})

	return root
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
	// "(devel)" is what BuildInfo reports for unversioned local builds — not useful.
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
//     ~/Library/Application Support or %AppData% — gplay sits next to
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
