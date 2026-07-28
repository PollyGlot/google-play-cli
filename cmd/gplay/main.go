package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/apps/accessiblecmd"
	"github.com/PollyGlot/google-play-cli/commands/apps/addcmd"
	"github.com/PollyGlot/google-play-cli/commands/apps/detailscmd"
	"github.com/PollyGlot/google-play-cli/commands/apps/initcmd"
	"github.com/PollyGlot/google-play-cli/commands/apps/listcmd"
	"github.com/PollyGlot/google-play-cli/commands/apps/removecmd"
	"github.com/PollyGlot/google-play-cli/commands/apps/viewcmd"
	"github.com/PollyGlot/google-play-cli/commands/auth/doctor"
	"github.com/PollyGlot/google-play-cli/commands/auth/list"
	"github.com/PollyGlot/google-play-cli/commands/auth/login"
	"github.com/PollyGlot/google-play-cli/commands/auth/logout"
	"github.com/PollyGlot/google-play-cli/commands/auth/status"
	compliancedatasafetyset "github.com/PollyGlot/google-play-cli/commands/compliance/datasafety/set"
	compliancedatasafetyvalidate "github.com/PollyGlot/google-play-cli/commands/compliance/datasafety/validate"
	customappscreate "github.com/PollyGlot/google-play-cli/commands/customapps/create"
	devicetierscreate "github.com/PollyGlot/google-play-cli/commands/device-tiers/create"
	devicetierslist "github.com/PollyGlot/google-play-cli/commands/device-tiers/list"
	devicetiersview "github.com/PollyGlot/google-play-cli/commands/device-tiers/view"
	editsbegin "github.com/PollyGlot/google-play-cli/commands/edits/begin"
	editscommit "github.com/PollyGlot/google-play-cli/commands/edits/commit"
	editsdiscard "github.com/PollyGlot/google-play-cli/commands/edits/discard"
	editsstatus "github.com/PollyGlot/google-play-cli/commands/edits/status"
	gamesachievementscreate "github.com/PollyGlot/google-play-cli/commands/games/achievements/create"
	gamesachievementsdelete "github.com/PollyGlot/google-play-cli/commands/games/achievements/delete"
	gamesachievementslist "github.com/PollyGlot/google-play-cli/commands/games/achievements/list"
	gamesachievementsupdate "github.com/PollyGlot/google-play-cli/commands/games/achievements/update"
	gamesachievementsview "github.com/PollyGlot/google-play-cli/commands/games/achievements/view"
	gamesleaderboardscreate "github.com/PollyGlot/google-play-cli/commands/games/leaderboards/create"
	gamesleaderboardsdelete "github.com/PollyGlot/google-play-cli/commands/games/leaderboards/delete"
	gamesleaderboardslist "github.com/PollyGlot/google-play-cli/commands/games/leaderboards/list"
	gamesleaderboardsupdate "github.com/PollyGlot/google-play-cli/commands/games/leaderboards/update"
	gamesleaderboardsview "github.com/PollyGlot/google-play-cli/commands/games/leaderboards/view"
	helpexitcodes "github.com/PollyGlot/google-play-cli/commands/help/exitcodes"
	"github.com/PollyGlot/google-play-cli/commands/installskills"
	metadataapply "github.com/PollyGlot/google-play-cli/commands/metadata/apply"
	metadataimagesapply "github.com/PollyGlot/google-play-cli/commands/metadata/images/apply"
	metadataimageslist "github.com/PollyGlot/google-play-cli/commands/metadata/images/list"
	metadataimagespull "github.com/PollyGlot/google-play-cli/commands/metadata/images/pull"
	metadataimagesvalidate "github.com/PollyGlot/google-play-cli/commands/metadata/images/validate"
	metadatalist "github.com/PollyGlot/google-play-cli/commands/metadata/list"
	metadatapull "github.com/PollyGlot/google-play-cli/commands/metadata/pull"
	metadatavalidate "github.com/PollyGlot/google-play-cli/commands/metadata/validate"
	ordersrefund "github.com/PollyGlot/google-play-cli/commands/orders/refund"
	ordersview "github.com/PollyGlot/google-play-cli/commands/orders/view"
	recoveryaddtargeting "github.com/PollyGlot/google-play-cli/commands/recovery/add-targeting"
	recoverycancel "github.com/PollyGlot/google-play-cli/commands/recovery/cancel"
	recoverycreate "github.com/PollyGlot/google-play-cli/commands/recovery/create"
	recoverydeploy "github.com/PollyGlot/google-play-cli/commands/recovery/deploy"
	recoverylist "github.com/PollyGlot/google-play-cli/commands/recovery/list"
	expansionset "github.com/PollyGlot/google-play-cli/commands/releases/expansion-files/set"
	expansionupload "github.com/PollyGlot/google-play-cli/commands/releases/expansion-files/upload"
	expansionview "github.com/PollyGlot/google-play-cli/commands/releases/expansion-files/view"
	generateddownload "github.com/PollyGlot/google-play-cli/commands/releases/generated/download"
	generatedlist "github.com/PollyGlot/google-play-cli/commands/releases/generated/list"
	releaseslist "github.com/PollyGlot/google-play-cli/commands/releases/list"
	releasesmappings "github.com/PollyGlot/google-play-cli/commands/releases/mappings"
	"github.com/PollyGlot/google-play-cli/commands/releases/promote"
	"github.com/PollyGlot/google-play-cli/commands/releases/rollout"
	sharingupload "github.com/PollyGlot/google-play-cli/commands/releases/sharing/upload"
	"github.com/PollyGlot/google-play-cli/commands/releases/upload"
	reviewshistory "github.com/PollyGlot/google-play-cli/commands/reviews/history"
	reviewslist "github.com/PollyGlot/google-play-cli/commands/reviews/list"
	reviewsreply "github.com/PollyGlot/google-play-cli/commands/reviews/reply"
	reviewsview "github.com/PollyGlot/google-play-cli/commands/reviews/view"
	schemacmd "github.com/PollyGlot/google-play-cli/commands/schema"
	subscriptionsapply "github.com/PollyGlot/google-play-cli/commands/subscriptions/apply"
	subscriptionspull "github.com/PollyGlot/google-play-cli/commands/subscriptions/pull"
	teamgrantslist "github.com/PollyGlot/google-play-cli/commands/team/grants/list"
	teamgrantsremove "github.com/PollyGlot/google-play-cli/commands/team/grants/remove"
	teamgrantsset "github.com/PollyGlot/google-play-cli/commands/team/grants/set"
	teampermissions "github.com/PollyGlot/google-play-cli/commands/team/permissions"
	teamusersadd "github.com/PollyGlot/google-play-cli/commands/team/users/add"
	teamuserslist "github.com/PollyGlot/google-play-cli/commands/team/users/list"
	teamusersremove "github.com/PollyGlot/google-play-cli/commands/team/users/remove"
	teamusersset "github.com/PollyGlot/google-play-cli/commands/team/users/set"
	teamusersview "github.com/PollyGlot/google-play-cli/commands/team/users/view"
	testerslist "github.com/PollyGlot/google-play-cli/commands/testers/list"
	testersset "github.com/PollyGlot/google-play-cli/commands/testers/set"
	tracksavailability "github.com/PollyGlot/google-play-cli/commands/tracks/availability"
	trackscreate "github.com/PollyGlot/google-play-cli/commands/tracks/create"
	trackslist "github.com/PollyGlot/google-play-cli/commands/tracks/list"
	tracksview "github.com/PollyGlot/google-play-cli/commands/tracks/view"
	vitalsanomalies "github.com/PollyGlot/google-play-cli/commands/vitals/anomaliescmd"
	vitalserrors "github.com/PollyGlot/google-play-cli/commands/vitals/errorscmd"
	vitalsquery "github.com/PollyGlot/google-play-cli/commands/vitals/query"
	"github.com/PollyGlot/google-play-cli/commands/vitals/vitalscmd"
	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
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
publishing surface (releases, tracks, reviews, metadata, compliance,
team). Designed to replace Fastlane on Android CI pipelines.`,
		// The root is a grouping noun like any other (kernel.GroupRunE): bare
		// `gplay` prints help, `gplay <unknown>` is CLI misuse (exit 2), one
		// clean `gplay: ...` line (SilenceErrors). Args:ArbitraryArgs routes
		// the unknown-command case through GroupRunE rather than cobra's
		// legacyArgs, which would emit a plain error (exit 1) and a second
		// "Error: ..." line — the inconsistency this harmonisation removes.
		Args:          cobra.ArbitraryArgs,
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Flag-parse failures (unknown flag, bad value) reach us as plain cobra
	// errors, which exit.For would map to the generic exit 1 — but
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

	// Global per-request timeout (docs/DESIGN.md §8). Zero (the default) means
	// the kernel applies a 60s deadline to control-plane API calls and leaves
	// media uploads unbounded; an explicit value bounds every request,
	// including uploads. The kernel reads it via FromCobra → Inputs.Timeout.
	root.PersistentFlags().Duration("timeout", 0,
		"per-request API timeout, e.g. 30s or 2m (default: 60s for control-plane calls, none for uploads)")

	// Global opt-in retry (docs/CI_CD.md §4). Default 0 = today's behavior (no
	// retry). When > 0 the kernel layers a retry transport that retries
	// transport errors / 5xx / 429 (honoring Retry-After) with exponential
	// backoff + jitter; --timeout then bounds each attempt. The kernel reads it
	// via FromCobra → Inputs.Retry.
	root.PersistentFlags().Int("retry", 0,
		"retry transient failures (transport errors, 5xx, 429) up to N times with exponential backoff (default: 0, no retry)")

	auth := &cobra.Command{
		Use:           "auth",
		Short:         "Manage gplay credentials",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
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
		Use:           "apps",
		Short:         "Manage Android packages registered with gplay",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	apps.AddCommand(initcmd.NewCommand(initcmd.Options{}))
	apps.AddCommand(addcmd.NewCommand(boot))
	apps.AddCommand(listcmd.NewCommand(boot))
	apps.AddCommand(accessiblecmd.NewCommand(boot))
	apps.AddCommand(viewcmd.NewCommand(boot))
	apps.AddCommand(detailscmd.NewCommand(boot))
	apps.AddCommand(removecmd.NewCommand(boot))
	root.AddCommand(apps)

	releases := &cobra.Command{
		Use:           "releases",
		Short:         "Manage app releases (upload, promote, rollout)",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	// Mutating release commands are marked so GPLAY_READONLY refuses them
	// (exit 4) unless run with --dry-run (kernel.MarkMutating / ADR-0024).
	// releases list is a read and stays unmarked.
	releases.AddCommand(kernel.MarkMutating(upload.NewCommand(boot)))
	releases.AddCommand(kernel.MarkMutating(promote.NewCommand(boot)))
	releases.AddCommand(kernel.MarkMutating(rollout.NewRolloutCommand(boot)))
	releases.AddCommand(kernel.MarkMutating(rollout.NewHaltCommand(boot)))
	releases.AddCommand(kernel.MarkMutating(rollout.NewResumeCommand(boot)))
	releases.AddCommand(kernel.MarkMutating(rollout.NewCompleteCommand(boot)))
	releases.AddCommand(releaseslist.NewCommand(boot))

	// `gplay releases mappings` — ProGuard/R8 deobfuscation mappings, the
	// publisher-Edit side of vitals symbolication (CONTEXT.md / ADR-0027 /
	// #250). It lives under releases (not vitals) because a Mapping is an
	// androidpublisher Edit upload, not a read from the Reporting service.
	// Only the upload leaf mutates Play state.
	mappings := &cobra.Command{
		Use:           "mappings",
		Short:         "Manage ProGuard/R8 deobfuscation mappings (symbolicate Play vitals crash stacks)",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	mappings.AddCommand(kernel.MarkMutating(releasesmappings.NewCommand(boot)))
	releases.AddCommand(mappings)

	// `gplay releases sharing` — Internal App Sharing (internalappsharingartifacts).
	// A grouping noun under releases, mirroring `releases mappings`: a non-track
	// upload that bypasses the Edit lifecycle entirely and mints a private,
	// shareable install link (CONTEXT.md / ADR-0030 / PRD #243). The single
	// `upload` leaf mutates Play state (creates an artifact), so it is marked.
	sharing := &cobra.Command{
		Use:           "sharing",
		Short:         "Upload builds to Internal App Sharing (private shareable links)",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	sharing.AddCommand(kernel.MarkMutating(sharingupload.NewCommand(boot)))
	releases.AddCommand(sharing)

	// `gplay releases expansion-files` — legacy OBB expansion files
	// (edits.expansionfiles). A grouping noun under releases, mirroring
	// `releases mappings`: an Edit artifact keyed by apkVersionCode (CONTEXT.md /
	// ADR-0030). upload (media) and set (PUT) mutate inside an Edit; view is a
	// read-only Edit. The expansion 'patch' type is a --type value, not the HTTP
	// PATCH method, which folds into set.
	expansionFiles := &cobra.Command{
		Use:           "expansion-files",
		Short:         "Manage legacy OBB expansion files (superseded by Play Asset Delivery)",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	expansionFiles.AddCommand(kernel.MarkMutating(expansionupload.NewCommand(boot)))
	expansionFiles.AddCommand(kernel.MarkMutating(expansionset.NewCommand(boot)))
	expansionFiles.AddCommand(expansionview.NewCommand(boot))
	releases.AddCommand(expansionFiles)

	// `gplay releases generated` — the APKs Play generates and signs from an
	// uploaded AAB (generatedapks). A grouping noun under releases, mirroring
	// `releases sharing`: an application-scoped read that bypasses the Edit
	// lifecycle entirely (CONTEXT.md "Generated APK" / ADR-0034 / PRD #299). The
	// leaves are pure reads (no MarkMutating, not gated by GPLAY_READONLY):
	// `list` enumerates the artifacts, `download` fetches one to disk.
	generated := &cobra.Command{
		Use:           "generated",
		Short:         "List and download the APKs Play generates from an AAB",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	generated.AddCommand(generatedlist.NewCommand(boot))
	generated.AddCommand(generateddownload.NewCommand(boot))
	releases.AddCommand(generated)
	root.AddCommand(releases)

	tracks := &cobra.Command{
		Use:           "tracks",
		Short:         "Inspect and create release tracks (standard and custom closed)",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	tracks.AddCommand(trackslist.NewCommand(boot))
	tracks.AddCommand(tracksview.NewCommand(boot))
	tracks.AddCommand(kernel.MarkMutating(trackscreate.NewCommand(boot)))
	// `tracks availability` is read-only at the API level (only `view`, no
	// writer), so the group stays unmarked.
	tracks.AddCommand(tracksavailability.NewCommand(boot))
	root.AddCommand(tracks)

	// `gplay testers` — read and declare the Google Groups authorized to
	// test a (custom closed) track. A top-level namespace parallel to
	// `tracks`, mirroring the sibling API resources edits.tracks /
	// edits.testers. See PRD #117 / docs/DESIGN.md §10.
	testers := &cobra.Command{
		Use:           "testers",
		Short:         "Manage the Google Groups authorized to test a track",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	testers.AddCommand(testerslist.NewCommand(boot))
	testers.AddCommand(kernel.MarkMutating(testersset.NewCommand(boot)))
	root.AddCommand(testers)

	// `gplay device-tiers` — Device Tier Configs (applications.deviceTierConfigs),
	// device-targeting for tiered content delivery. A new top-level namespace,
	// app-scoped and OUTSIDE the Edit lifecycle (no editId) — not under `tracks`
	// (an Edit surface) nor `apps` (the local registry). The resource is
	// immutable (create/get/list only), so `create` is the only mutating leaf.
	// See ADR-0030 / PRD #243 / CONTEXT.md (Device tier config).
	deviceTiers := &cobra.Command{
		Use:           "device-tiers",
		Short:         "Manage device tier configs (device-targeting for tiered delivery)",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	deviceTiers.AddCommand(kernel.MarkMutating(devicetierscreate.NewCommand(boot)))
	deviceTiers.AddCommand(devicetiersview.NewCommand(boot))
	deviceTiers.AddCommand(devicetierslist.NewCommand(boot))
	root.AddCommand(deviceTiers)

	// `gplay recovery` — App Recovery (apprecovery): targeted incident-response
	// remediation that pushes users impacted by a bad release back to a safe app
	// version. A new top-level namespace, app-scoped and OUTSIDE the Edit
	// lifecycle (own appRecoveryId, draft→active→canceled) — the inverse of why
	// `releases mappings` lives under releases. The draft/read leaves are here;
	// the production-impacting lifecycle leaves (deploy/cancel/add-targeting)
	// require --confirm. See ADR-0030 / PRD #243 / CONTEXT.md (Recovery).
	recoveryGroup := &cobra.Command{
		Use:           "recovery",
		Short:         "Manage app recovery actions (incident-response remediation for a bad release)",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	recoveryGroup.AddCommand(kernel.MarkMutating(recoverycreate.NewCommand(boot)))
	recoveryGroup.AddCommand(recoverylist.NewCommand(boot))
	// The production-impacting lifecycle leaves (deploy/cancel/add-targeting)
	// each require --confirm (exit 3 if missing) — new domain verbs admitted
	// under ADR-0019 §2 / recorded in ADR-0030.
	recoveryGroup.AddCommand(kernel.MarkMutating(recoverydeploy.NewCommand(boot)))
	recoveryGroup.AddCommand(kernel.MarkMutating(recoverycancel.NewCommand(boot)))
	recoveryGroup.AddCommand(kernel.MarkMutating(recoveryaddtargeting.NewCommand(boot)))
	root.AddCommand(recoveryGroup)

	// `gplay team` — manage the Developer account's members (Users) and their
	// per-app access (Grants). The first gplay surface keyed by the Developer
	// account rather than a package (ADR-0015). `team`, `users`, and `grants`
	// are grouping nouns (kernel.GroupRunE — bare prints help, an unknown
	// subcommand is exit-2 misuse); the named `team` (not `users`/`access`)
	// avoids colliding with gplay's Account. See PRD #147 / ADR-0015/0016/0017
	// / CONTEXT.md.
	team := &cobra.Command{
		Use:           "team",
		Short:         "Manage the Developer account's members and permissions (users, grants)",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	team.AddCommand(teampermissions.NewCommand(boot))

	teamUsers := &cobra.Command{
		Use:           "users",
		Short:         "List and manage the Developer account's members",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	teamUsers.AddCommand(teamuserslist.NewCommand(boot))
	teamUsers.AddCommand(teamusersview.NewCommand(boot))
	teamUsers.AddCommand(kernel.MarkMutating(teamusersadd.NewCommand(boot)))
	teamUsers.AddCommand(kernel.MarkMutating(teamusersset.NewCommand(boot)))
	teamUsers.AddCommand(kernel.MarkMutating(teamusersremove.NewCommand(boot)))
	team.AddCommand(teamUsers)

	teamGrants := &cobra.Command{
		Use:           "grants",
		Short:         "List and manage members' per-app access",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	teamGrants.AddCommand(teamgrantslist.NewCommand(boot))
	teamGrants.AddCommand(kernel.MarkMutating(teamgrantsset.NewCommand(boot)))
	teamGrants.AddCommand(kernel.MarkMutating(teamgrantsremove.NewCommand(boot)))
	team.AddCommand(teamGrants)

	root.AddCommand(team)

	// `gplay customapps` — managed Google Play private app creation
	// (playcustomapp.accounts.customApps.create). Like `team` it is keyed by the
	// Developer account, not a package (ADR-0015) — the app does not yet exist to
	// be keyed by package. The whole upstream surface is one method (no read, no
	// delete), so `create` is the only leaf; it is the destructive/irreversible
	// tier (--confirm, exit 3 if missing) and MarkMutating for GPLAY_READONLY.
	// See ADR-0032 / PRD #242 / CONTEXT.md (Custom app).
	customapps := &cobra.Command{
		Use:           "customapps",
		Short:         "Create managed Google Play private apps (organisation-scoped)",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	customapps.AddCommand(kernel.MarkMutating(customappscreate.NewCommand(boot)))
	root.AddCommand(customapps)

	// `gplay edits` — the EXPLICIT Edit lifecycle (begin/commit/discard/status,
	// docs/DESIGN.md §4 / CONTEXT.md "Edit"). Most write commands run their own
	// implicit Edit (open → mutate → commit) per invocation; `edits begin` opens
	// one and pins its ID to .gplay/edit-<package>.json so subsequent writes
	// batch into it instead of opening their own — committed or discarded
	// explicitly by the user. begin/commit/discard mutate Play state (insert /
	// commit / delete), so they are MarkMutating (GPLAY_READONLY refuses them,
	// exit 4); status is a local-pin read and stays unmarked. See #48.
	editsGroup := &cobra.Command{
		Use:           "edits",
		Short:         "Manage explicit Edit transactions (begin, commit, discard, status)",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	editsGroup.AddCommand(kernel.MarkMutating(editsbegin.NewCommand(boot)))
	editsGroup.AddCommand(kernel.MarkMutating(editscommit.NewCommand(boot)))
	editsGroup.AddCommand(kernel.MarkMutating(editsdiscard.NewCommand(boot)))
	editsGroup.AddCommand(editsstatus.NewCommand(boot))
	root.AddCommand(editsGroup)

	// `gplay games` — Play Games Services configuration (gamesConfiguration): a
	// game's achievement and leaderboard configurations. A DISTINCT Google
	// service (its own host, the shared androidpublisher scope — so no WithScope)
	// addressed by the numeric Play Games application ID, its own ID space rather
	// than the Android package (ADR-0033). `games`, `achievements`, and
	// `leaderboards` are grouping nouns (kernel.GroupRunE). Writes affect the
	// editable draft (there is no publish method — publishing to players is
	// Console-only); list/view are reads, create/update are routine writes, and
	// delete is the destructive tier (--confirm, exit 3 if missing). See PRD #241
	// / ADR-0033 / CONTEXT.md (Play Games Services Publishing API).
	games := &cobra.Command{
		Use:           "games",
		Short:         "Configure a game's Play Games Services resources (achievements, leaderboards)",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	gamesAchievements := &cobra.Command{
		Use:           "achievements",
		Short:         "Manage a game's achievement configurations (list/view/create/update/delete)",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	gamesAchievements.AddCommand(gamesachievementslist.NewCommand(boot))
	gamesAchievements.AddCommand(gamesachievementsview.NewCommand(boot))
	gamesAchievements.AddCommand(kernel.MarkMutating(gamesachievementscreate.NewCommand(boot)))
	gamesAchievements.AddCommand(kernel.MarkMutating(gamesachievementsupdate.NewCommand(boot)))
	gamesAchievements.AddCommand(kernel.MarkMutating(gamesachievementsdelete.NewCommand(boot)))
	games.AddCommand(gamesAchievements)

	gamesLeaderboards := &cobra.Command{
		Use:           "leaderboards",
		Short:         "Manage a game's leaderboard configurations (list/view/create/update/delete)",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	gamesLeaderboards.AddCommand(gamesleaderboardslist.NewCommand(boot))
	gamesLeaderboards.AddCommand(gamesleaderboardsview.NewCommand(boot))
	gamesLeaderboards.AddCommand(kernel.MarkMutating(gamesleaderboardscreate.NewCommand(boot)))
	gamesLeaderboards.AddCommand(kernel.MarkMutating(gamesleaderboardsupdate.NewCommand(boot)))
	gamesLeaderboards.AddCommand(kernel.MarkMutating(gamesleaderboardsdelete.NewCommand(boot)))
	games.AddCommand(gamesLeaderboards)
	root.AddCommand(games)

	// `gplay orders` — look up Google Play orders by order ID (orders.get /
	// orders.batchget) and refund them (orders.refund). The admin-side commerce
	// surface: orders ride the package/app axis like releases/metadata
	// (applications/{packageName}/orders/...), NOT the developer-account axis —
	// a human or agent holds an order ID from a complaint or payout report (no
	// device token), distinct from runtime purchase-token verification, which
	// gplay does not wrap (ADR-0031 / CONTEXT.md "Order"). `view` is a pure read
	// (single #282 + batch #283) — not marked mutating, not gated by
	// GPLAY_READONLY. `refund` (#284) is the money-moving, irreversible write —
	// MarkMutating so GPLAY_READONLY refuses it (exit 4), and it requires
	// --confirm at the command layer (exit 3 if missing).
	ordersGroup := &cobra.Command{
		Use:           "orders",
		Short:         "Look up and refund Google Play orders by order ID (admin commerce surface)",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	ordersGroup.AddCommand(ordersview.NewCommand(boot))
	ordersGroup.AddCommand(kernel.MarkMutating(ordersrefund.NewCommand(boot)))
	root.AddCommand(ordersGroup)

	// `gplay subscriptions` — the declarative Monetization catalog, subscription
	// side (PRD #51, walking skeleton #367 / ADR-0041). Package axis, Edit-free
	// (applications/{packageName}/subscriptions/..., like device-tiers and
	// orders). `pull` mirrors the live catalog into <productId>.json files —
	// it writes only locally, so it is not marked mutating. `apply` reconciles
	// the live catalog to the files (create/patch/delete) — MarkMutating so
	// GPLAY_READONLY refuses it (exit 4); a plan containing a delete
	// additionally requires --confirm at the command layer (exit 3 if missing).
	// Subscriber price migration is deliberately NOT reachable from apply — it
	// arrives as its own gated command (slice #370).
	subscriptionsGroup := &cobra.Command{
		Use:           "subscriptions",
		Short:         "Pull and apply the app's subscription catalog as files (declarative)",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	subscriptionsGroup.AddCommand(subscriptionspull.NewCommand(boot))
	subscriptionsGroup.AddCommand(kernel.MarkMutating(subscriptionsapply.NewCommand(boot)))
	root.AddCommand(subscriptionsGroup)

	reviews := &cobra.Command{
		Use:           "reviews",
		Short:         "Read and reply to user reviews",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	reviews.AddCommand(reviewslist.NewCommand(boot))
	reviews.AddCommand(kernel.MarkMutating(reviewsreply.NewCommand(boot)))
	reviews.AddCommand(reviewsview.NewCommand(boot))
	// `reviews history` reads the monthly CSV reports from the developer's
	// Reporting bucket over the GCS API — a third Google service, so its leaf
	// mints a least-privilege devstorage.read_only token via WithScope (ADR-0037).
	reviews.AddCommand(kernel.WithScope(reviewshistory.NewCommand(boot), token.StorageReadOnlyScope))
	root.AddCommand(reviews)

	// `gplay vitals` — read-only post-launch quality signals (crashes/ANR and
	// the other metric sets) from the Play Developer Reporting API. A DISTINCT
	// Google service: every leaf is wrapped with kernel.WithScope so it mints a
	// least-privilege playdeveloperreporting-scoped token, never androidpublisher
	// (ADR-0027 / #49). The whole namespace is read-only, so nothing is marked
	// mutating.
	vitals := &cobra.Command{
		Use:           "vitals",
		Short:         "Read post-launch quality signals (crashes, ANRs, …) from Play vitals",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	vitals.AddCommand(kernel.WithScope(vitalsquery.NewCommand(boot), token.ReportingScope))
	// Opinionated metric-set presets (vitals crashes, vitals anr, …) — built
	// from a data list so the family stays consistent; each requests the
	// least-privilege reporting scope like every other vitals leaf.
	for _, spec := range vitalscmd.Presets {
		vitals.AddCommand(kernel.WithScope(vitalscmd.NewPresetCommand(boot, spec), token.ReportingScope))
	}
	// `vitals errors` — counts / issues / reports. The group's leaves carry the
	// reporting scope themselves (see errorscmd.NewCommand), so it is added bare.
	vitals.AddCommand(vitalserrors.NewCommand(boot))
	vitals.AddCommand(kernel.WithScope(vitalsanomalies.NewCommand(boot), token.ReportingScope))
	root.AddCommand(vitals)

	// `gplay metadata` — Store front Listings (per-locale text), the
	// fastlane-supply text side. See PRD #50 / ADR-0011.
	metadata := &cobra.Command{
		Use:           "metadata",
		Short:         "Manage Store front Listings (per-locale title/description/video)",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	metadata.AddCommand(metadatalist.NewCommand(boot))
	metadata.AddCommand(metadatapull.NewCommand(boot))
	metadata.AddCommand(metadatavalidate.NewCommand(boot))
	metadata.AddCommand(kernel.MarkMutating(metadataapply.NewCommand(boot)))

	// `gplay metadata images` — Store images (per-locale icon / feature
	// graphic / screenshots), the fastlane-supply image side. See PRD #112 /
	// ADR-0013.
	metadataImages := &cobra.Command{
		Use:           "images",
		Short:         "Manage Store images (per-locale icon, feature graphic, screenshots)",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	metadataImages.AddCommand(metadataimageslist.NewCommand(boot))
	metadataImages.AddCommand(metadataimagespull.NewCommand(boot))
	metadataImages.AddCommand(metadataimagesvalidate.NewCommand(boot))
	metadataImages.AddCommand(kernel.MarkMutating(metadataimagesapply.NewCommand(boot)))
	metadata.AddCommand(metadataImages)
	root.AddCommand(metadata)

	// `gplay compliance` — regulatory declarations that gate publication
	// (Data Safety today; content rating and other "App content" surfaces in
	// future scope). App-keyed, write-heavy, outside the Edits model — a
	// distinct family from the store-presence namespaces. See PRD #114 /
	// ADR-0014.
	compliance := &cobra.Command{
		Use:           "compliance",
		Short:         "Manage an app's regulatory declarations (Data Safety, ...)",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	datasafety := &cobra.Command{
		Use:           "datasafety",
		Short:         "Push and validate the app's Data Safety declaration (write-only)",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	datasafety.AddCommand(compliancedatasafetyvalidate.NewCommand(boot))
	datasafety.AddCommand(kernel.MarkMutating(compliancedatasafetyset.NewCommand(boot)))
	compliance.AddCommand(datasafety)
	root.AddCommand(compliance)

	// `gplay schema` — OFFLINE, no-auth, `[experimental]` introspection of the
	// Android Publisher API surface from an embedded Schema index (ADR-0022).
	// A top-level reference/diagnostic meta-command (ADR-0019), not keyed by a
	// package or the Developer account. See PRD #199.
	root.AddCommand(schemacmd.NewCommand(boot))

	// `gplay exit-codes` / `gplay help exit-codes` — the semantic exit-code
	// taxonomy (docs/DESIGN.md §9), built from internal/exit so it cannot
	// drift from the codes the binary actually returns.
	root.AddCommand(helpexitcodes.NewCommand())

	// `gplay install-skills` — install the companion agent skills via
	// `npx skills add` (ADR-0028 / #266). A flat category-3 meta-command; the
	// one gplay command that needs Node/npx (a dev-workstation convenience, off
	// the CI/runtime path, so the "no Node" pillar holds). Surfaced in root
	// --help so an agent told to "install gplay" can discover it.
	root.AddCommand(installskills.NewCommand(installskills.Options{}))

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
