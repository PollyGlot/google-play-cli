package main

// One constructor per top-level command group, in the order newRootCmd
// registers them. Each carries the per-leaf policy of its group
// (kernel.MarkMutating, kernel.WithScope, kernel.Experimental) and the design
// rationale behind it, so adding a leaf touches one function here plus the two
// registries in main_test.go, which stay the completeness guard.

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/apps/accessiblecmd"
	"github.com/PollyGlot/google-play-cli/commands/apps/addcmd"
	"github.com/PollyGlot/google-play-cli/commands/apps/auditcmd"
	"github.com/PollyGlot/google-play-cli/commands/apps/detailscmd"
	"github.com/PollyGlot/google-play-cli/commands/apps/initcmd"
	"github.com/PollyGlot/google-play-cli/commands/apps/listcmd"
	"github.com/PollyGlot/google-play-cli/commands/apps/removecmd"
	"github.com/PollyGlot/google-play-cli/commands/apps/viewcmd"
	appstoreapkupload "github.com/PollyGlot/google-play-cli/commands/appstore/apk/upload"
	appstorecatalogeventslist "github.com/PollyGlot/google-play-cli/commands/appstore/catalog/events/list"
	appstorecatalogview "github.com/PollyGlot/google-play-cli/commands/appstore/catalog/view"
	appstorecreate "github.com/PollyGlot/google-play-cli/commands/appstore/create"
	appstoreimageupload "github.com/PollyGlot/google-play-cli/commands/appstore/image/upload"
	appstorepolicyupload "github.com/PollyGlot/google-play-cli/commands/appstore/policy/upload"
	appstorepublishstatusset "github.com/PollyGlot/google-play-cli/commands/appstore/publishstatus/set"
	appstoresubmit "github.com/PollyGlot/google-play-cli/commands/appstore/submit"
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
	editsvalidate "github.com/PollyGlot/google-play-cli/commands/edits/validate"
	gamesachievementscreate "github.com/PollyGlot/google-play-cli/commands/games/achievements/create"
	gamesachievementslist "github.com/PollyGlot/google-play-cli/commands/games/achievements/list"
	gamesachievementsremove "github.com/PollyGlot/google-play-cli/commands/games/achievements/remove"
	gamesachievementsset "github.com/PollyGlot/google-play-cli/commands/games/achievements/set"
	gamesachievementsview "github.com/PollyGlot/google-play-cli/commands/games/achievements/view"
	gamesleaderboardscreate "github.com/PollyGlot/google-play-cli/commands/games/leaderboards/create"
	gamesleaderboardslist "github.com/PollyGlot/google-play-cli/commands/games/leaderboards/list"
	gamesleaderboardsremove "github.com/PollyGlot/google-play-cli/commands/games/leaderboards/remove"
	gamesleaderboardsset "github.com/PollyGlot/google-play-cli/commands/games/leaderboards/set"
	gamesleaderboardsview "github.com/PollyGlot/google-play-cli/commands/games/leaderboards/view"
	iapapply "github.com/PollyGlot/google-play-cli/commands/iap/apply"
	iappull "github.com/PollyGlot/google-play-cli/commands/iap/pull"
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
	artifactslist "github.com/PollyGlot/google-play-cli/commands/releases/artifacts/list"
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
	signingenroll "github.com/PollyGlot/google-play-cli/commands/signing/enroll"
	signingrotate "github.com/PollyGlot/google-play-cli/commands/signing/rotate"
	subscriptionsapply "github.com/PollyGlot/google-play-cli/commands/subscriptions/apply"
	subscriptionspricesconvert "github.com/PollyGlot/google-play-cli/commands/subscriptions/prices/convert"
	subscriptionspricesmigrate "github.com/PollyGlot/google-play-cli/commands/subscriptions/prices/migrate"
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
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

func newAuthGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Group("auth", "Manage gplay credentials",
		login.NewCommand(boot),
		logout.NewCommand(boot),
		status.NewCommand(boot),
		list.NewCommand(boot),
		doctor.NewCommand(boot),
	)
}

// `gplay apps init` duplicates the top-level `gplay init` (see newRootCmd) so
// both forms are discoverable.
func newAppsGroup(boot kernel.Boot) *cobra.Command {
	// Same command under a second path: its Example names the path it is
	// read from, so `gplay apps init --help` shows `gplay apps init`.
	appsInit := initcmd.NewCommand(initcmd.Options{})
	appsInit.Example = strings.ReplaceAll(appsInit.Example, "gplay init", "gplay apps init")
	return kernel.Group("apps", "Manage Android packages registered with gplay",
		appsInit,
		addcmd.NewCommand(boot),
		listcmd.NewCommand(boot),
		accessiblecmd.NewCommand(boot),
		viewcmd.NewCommand(boot),
		detailscmd.NewCommand(boot),
		removecmd.NewCommand(boot),
		// #449: a read-only consistency sweep. Experimental, not MarkMutating: it
		// only ever reads (the throwaway Edit it opens is always discarded), so a
		// GPLAY_READONLY environment must still be able to audit itself.
		kernel.Experimental(auditcmd.NewCommand(boot)),
	)
}

// Mutating release commands are marked so GPLAY_READONLY refuses them (exit 4)
// unless run with --dry-run (kernel.MarkMutating / ADR-0024). releases list is
// a read and stays unmarked.
func newReleasesGroup(boot kernel.Boot) *cobra.Command {
	// `gplay releases mappings`: ProGuard/R8 deobfuscation mappings, the
	// publisher-Edit side of vitals symbolication (CONTEXT.md / ADR-0027 /
	// #250). It lives under releases (not vitals) because a Mapping is an
	// androidpublisher Edit upload, not a read from the Reporting service.
	// Only the upload leaf mutates Play state.
	mappings := kernel.Group("mappings", "Manage ProGuard/R8 deobfuscation mappings (symbolicate Play vitals crash stacks)",
		kernel.MarkMutating(releasesmappings.NewCommand(boot)),
	)

	// `gplay releases sharing`: Internal App Sharing (internalappsharingartifacts).
	// A grouping noun under releases, mirroring `releases mappings`: a non-track
	// upload that bypasses the Edit lifecycle entirely and mints a private,
	// shareable install link (CONTEXT.md / ADR-0030 / PRD #243). The single
	// `upload` leaf mutates Play state (creates an artifact), so it is marked.
	// [experimental] (ADR-0010/ADR-0042): shipped in the #243 long-tail batch and
	// barely exercised: the shape of what it returns for a link is the part most
	// likely to move.
	sharing := kernel.Experimental(kernel.Group("sharing", "Upload builds to Internal App Sharing (private shareable links)",
		kernel.MarkMutating(sharingupload.NewCommand(boot)),
	))

	// `gplay releases expansion-files`: legacy OBB expansion files
	// (edits.expansionfiles). A grouping noun under releases, mirroring
	// `releases mappings`: an Edit artifact keyed by apkVersionCode (CONTEXT.md /
	// ADR-0030). upload (media) and set (PUT) mutate inside an Edit; view is a
	// read-only Edit. The expansion 'patch' type is a --type value, not the HTTP
	// PATCH method, which folds into set.
	// [experimental] (ADR-0010/ADR-0042): a legacy surface superseded by Play
	// Asset Delivery, shipped for coverage rather than demand: freezing flags
	// nobody has used yet buys nothing.
	expansionFiles := kernel.Experimental(kernel.Group("expansion-files", "Manage legacy OBB expansion files (superseded by Play Asset Delivery)",
		kernel.MarkMutating(expansionupload.NewCommand(boot)),
		kernel.MarkMutating(expansionset.NewCommand(boot)),
		expansionview.NewCommand(boot),
	))

	// `gplay releases generated`: the APKs Play generates and signs from an
	// uploaded AAB (generatedapks). A grouping noun under releases, mirroring
	// `releases sharing`: an application-scoped read that bypasses the Edit
	// lifecycle entirely (CONTEXT.md "Generated APK" / ADR-0034 / PRD #299). The
	// leaves are pure reads (no MarkMutating, not gated by GPLAY_READONLY):
	// `list` enumerates the artifacts, `download` fetches one to disk.
	// [experimental] (ADR-0010/ADR-0042): the download target (file vs stdout) and
	// the variant-selection flags are the parts most likely to move once real
	// device-targeted APK sets are pulled through it.
	generated := kernel.Experimental(kernel.Group("generated", "List and download the APKs Play generates from an AAB",
		generatedlist.NewCommand(boot),
		generateddownload.NewCommand(boot),
	))

	// `gplay releases artifacts`: the APKs and App Bundles uploaded to the app
	// (edits.apks.list / edits.bundles.list), i.e. the version codes a promote
	// or rollout can reference. Edit-scoped, so the leaf opens a read-only Edit
	// it always discards, or reads inside a pinned explicit one (#543). Pure
	// read: no MarkMutating, not gated by GPLAY_READONLY.
	// [experimental] (ADR-0010/ADR-0042): the merged-kind table and --kind are
	// new surface whose columns may still move.
	artifacts := kernel.Experimental(kernel.Group("artifacts", "List the APKs and App Bundles attached to an app",
		artifactslist.NewCommand(boot),
	))

	return kernel.Group("releases", "Manage app releases (upload, promote, rollout)",
		kernel.MarkMutating(upload.NewCommand(boot)),
		kernel.MarkMutating(promote.NewCommand(boot)),
		kernel.MarkMutating(rollout.NewRolloutCommand(boot)),
		kernel.MarkMutating(rollout.NewHaltCommand(boot)),
		kernel.MarkMutating(rollout.NewResumeCommand(boot)),
		kernel.MarkMutating(rollout.NewCompleteCommand(boot)),
		releaseslist.NewCommand(boot),
		mappings,
		sharing,
		expansionFiles,
		generated,
		artifacts,
	)
}

func newTracksGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Group("tracks", "Inspect and create release tracks (standard and custom closed)",
		trackslist.NewCommand(boot),
		tracksview.NewCommand(boot),
		kernel.MarkMutating(trackscreate.NewCommand(boot)),
		// `tracks availability` is read-only at the API level (only `view`, no
		// writer), so the group stays unmarked.
		tracksavailability.NewCommand(boot),
	)
}

// `gplay testers`: read and declare the Google Groups authorized to test a
// (custom closed) track. A top-level namespace parallel to `tracks`, mirroring
// the sibling API resources edits.tracks / edits.testers. See PRD #117 /
// docs/DESIGN.md §10.
func newTestersGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Group("testers", "Manage the Google Groups authorized to test a track",
		testerslist.NewCommand(boot),
		kernel.MarkMutating(testersset.NewCommand(boot)),
	)
}

// `gplay device-tiers`: Device Tier Configs (applications.deviceTierConfigs),
// device-targeting for tiered content delivery. A new top-level namespace,
// app-scoped and OUTSIDE the Edit lifecycle (no editId), not under `tracks`
// (an Edit surface) nor `apps` (the local registry). The resource is immutable
// (create/get/list only), so `create` is the only mutating leaf. See ADR-0030 /
// PRD #243 / CONTEXT.md (Device tier config).
//
// [experimental] (ADR-0010/ADR-0042): #243 long-tail, and the config resource
// is immutable server-side, if the create surface needs a different shape,
// there is no in-place fix, only a new flag contract.
func newDeviceTiersGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Experimental(kernel.Group("device-tiers", "Manage device tier configs (device-targeting for tiered delivery)",
		kernel.MarkMutating(devicetierscreate.NewCommand(boot)),
		devicetiersview.NewCommand(boot),
		devicetierslist.NewCommand(boot),
	))
}

// `gplay recovery`: App Recovery (apprecovery): targeted incident-response
// remediation that pushes users impacted by a bad release back to a safe app
// version. A new top-level namespace, app-scoped and OUTSIDE the Edit
// lifecycle (own appRecoveryId, draft→active→canceled): the inverse of why
// `releases mappings` lives under releases. The draft/read leaves are here;
// the production-impacting lifecycle leaves (deploy/cancel/add-targeting)
// require --confirm. See ADR-0030 / PRD #243 / CONTEXT.md (Recovery).
//
// [experimental] (ADR-0010/ADR-0042): incident-response surface shipped in the
// #243 batch, with three new domain verbs (deploy/cancel/add-targeting) that
// have never been driven through a real incident.
func newRecoveryGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Experimental(kernel.Group("recovery", "Manage app recovery actions (incident-response remediation for a bad release)",
		kernel.MarkMutating(recoverycreate.NewCommand(boot)),
		recoverylist.NewCommand(boot),
		// The production-impacting lifecycle leaves (deploy/cancel/add-targeting)
		// each require --confirm (exit 3 if missing): new domain verbs admitted
		// under ADR-0019 §2 / recorded in ADR-0030.
		kernel.MarkMutating(recoverydeploy.NewCommand(boot)),
		kernel.MarkMutating(recoverycancel.NewCommand(boot)),
		kernel.MarkMutating(recoveryaddtargeting.NewCommand(boot)),
	))
}

// `gplay signing`: Play App Signing with a self-hosted Google Cloud KMS key
// (appsigning). A top-level namespace rather than a leaf under `apps`: it
// addresses the app-signing resource, app-scoped and OUTSIDE the Edit
// lifecycle (POST custom verbs on /applications/{name}/appSigning). Both
// leaves are enterprise-only: standard, Google-managed enrollment cannot be
// done via API at all, and rotating a Google-managed key goes through the
// Play Console. Each swaps the live signing key of a real app, so each
// requires --confirm (exit 3 if missing) per ADR-0043's criterion. See
// PRD #476 / ADR-0026 / CONTEXT.md.
//
// [experimental] (ADR-0010/ADR-0042): a low-traffic enterprise surface whose
// flag shape has never been exercised against a real Cloud KMS key.
func newSigningGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Experimental(kernel.Group("signing", "Manage Play App Signing with a self-hosted Cloud KMS key (enterprise key custody)",
		kernel.MarkMutating(signingenroll.NewCommand(boot)),
		kernel.MarkMutating(signingrotate.NewCommand(boot)),
	))
}

// `gplay team`: manage the Developer account's members (Users) and their
// per-app access (Grants). The first gplay surface keyed by the Developer
// account rather than a package (ADR-0015). `team`, `users`, and `grants` are
// grouping nouns (kernel.Group: bare prints help, an unknown subcommand is
// exit-2 misuse); the named `team` (not `users`/`access`) avoids colliding
// with gplay's Account. See PRD #147 / ADR-0015/0016/0017 / CONTEXT.md.
func newTeamGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Group("team", "Manage the Developer account's members and permissions (users, grants)",
		teampermissions.NewCommand(boot),
		kernel.Group("users", "List and manage the Developer account's members",
			teamuserslist.NewCommand(boot),
			teamusersview.NewCommand(boot),
			kernel.MarkMutating(teamusersadd.NewCommand(boot)),
			kernel.MarkMutating(teamusersset.NewCommand(boot)),
			kernel.MarkMutating(teamusersremove.NewCommand(boot)),
		),
		kernel.Group("grants", "List and manage members' per-app access",
			teamgrantslist.NewCommand(boot),
			kernel.MarkMutating(teamgrantsset.NewCommand(boot)),
			kernel.MarkMutating(teamgrantsremove.NewCommand(boot)),
		),
	)
}

// `gplay customapps`: managed Google Play private app creation
// (playcustomapp.accounts.customApps.create). Like `team` it is keyed by the
// Developer account, not a package (ADR-0015): the app does not yet exist to
// be keyed by package. The whole upstream surface is one method (no read, no
// delete), so `create` is the only leaf; it is the destructive/irreversible
// tier (--confirm, exit 3 if missing) and MarkMutating for GPLAY_READONLY.
// See ADR-0032 / PRD #242 / CONTEXT.md (Custom app).
//
// [experimental] (ADR-0010/ADR-0042): one irreversible create against an
// organisation-scoped API with no read to verify the result: the surface
// least able to prove it got the shape right.
func newCustomAppsGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Experimental(kernel.Group("customapps", "Create managed Google Play private apps (organisation-scoped)",
		kernel.MarkMutating(customappscreate.NewCommand(boot)),
	))
}

// `gplay appstore`: the surface for the operator of an ALTERNATIVE APP STORE,
// not the app-developer persona the rest of gplay serves. Addressing rides the
// app store package name (--store-package / the GPLAY_APP_STORE_PACKAGE env
// var), a distinct axis from the Android package of the repo's own app, so
// there is no project-pin cascade. `appstore` and `catalog` are grouping nouns
// (kernel.Group: bare prints help, an unknown subcommand is exit-2 misuse). The
// namespace carries the two sibling surfaces of that persona: everything under
// `catalog` is read-only (the Catalog Export for app stores, PRD #396) and
// `create` (#378, PRD #377) opens the hosted app review path: the mandatory
// first call for any hosted app, MarkMutating so GPLAY_READONLY refuses it
// (exit 4). All of it is Edit-free. See CONTEXT.md ("Catalog app view",
// "Hosted app").
//
// [experimental] (ADR-0010/ADR-0042): a brand-new namespace serving a persona
// gplay has never served, whose addressing axis (the app store package name)
// has not yet been exercised against a real enrolled app store.
func newAppStoreGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Experimental(kernel.Group("appstore", "Alternative app store operations (catalog export, hosted app review)",
		kernel.MarkMutating(appstorecreate.NewCommand(boot)),
		kernel.Group("catalog", "Read Google Play's catalog export for app stores",
			appstorecatalogview.NewCommand(boot),
			// `events` is a grouping noun under `catalog`: the update-event feed is
			// its own resource (a time-ranged list of changes), not a mode of
			// `catalog view` (a new noun, never a flag: ADR-0019).
			kernel.Group("events", "Read the catalog update-event feed (incremental catalog sync)",
				appstorecatalogeventslist.NewCommand(boot),
			),
		),
		// The three media endpoints (#379) are one grouping noun each, noun
		// before verb like `releases mappings upload` (ADR-0019; 2.0.0 retired
		// the verb-first `upload` group, #597). Each leaf hands Google a file and
		// gets back a tracking id; nothing is distributed until `appstore submit`
		// cites those ids, so the uploads are inert writes: MarkMutating (they do
		// create server-side artifacts) but no confirmation gate, per the
		// ADR-0043 criterion (irreversible AND externally visible gates;
		// irreversible-but-inert does not).
		kernel.Group("apk", "Upload hosted app APKs",
			kernel.MarkMutating(appstoreapkupload.NewCommand(boot)),
		),
		kernel.Group("image", "Upload hosted app listing images",
			kernel.MarkMutating(appstoreimageupload.NewCommand(boot)),
		),
		kernel.Group("policy", "Upload hosted app policy declaration documents",
			kernel.MarkMutating(appstorepolicyupload.NewCommand(boot)),
		),
		// `publish-status set` (#380) flips the app in or out of the store: an
		// external effect, but a reversible one (the opposite call puts it back),
		// so it takes the ordinary write safeguards and no gate. `publish-status`
		// is a grouping noun and the write is ADR-0019's `set`.
		kernel.Group("publish-status", "Manage a hosted app's storefront visibility (published or unpublished)",
			kernel.MarkMutating(appstorepublishstatusset.NewCommand(boot)),
		),
		// `submit` (#381) is the one command in the namespace that sends to
		// Google's review, immediately and irrevocably: it carries the --confirm
		// gate (exit 3). ADR-0043 §2 placed the gate here; it names the flag
		// `--yes`, amended to `--confirm` for consistency with every other gate in
		// the CLI. A domain verb, not `set`: `set` would hide that each call
		// starts an irrevocable review (DESIGN section 0).
		kernel.MarkMutating(appstoresubmit.NewCommand(boot)),
	))
}

// `gplay edits`: the EXPLICIT Edit lifecycle (begin/commit/discard/status,
// docs/DESIGN.md §4 / CONTEXT.md "Edit"). Most write commands run their own
// implicit Edit (open → mutate → commit) per invocation; `edits begin` opens
// one and pins its ID to .gplay/edit-<package>.json so subsequent writes batch
// into it instead of opening their own: committed or discarded explicitly by
// the user. begin/commit/discard mutate Play state (insert / commit / delete),
// so they are MarkMutating (GPLAY_READONLY refuses them, exit 4); status is a
// local-pin read (a GET with --live) and validate runs Google's checks without
// changing anything server-side (edits.validate), so both stay unmarked. See
// #48 and #544.
func newEditsGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Group("edits", "Manage explicit Edit transactions (begin, validate, commit, discard, status)",
		kernel.MarkMutating(editsbegin.NewCommand(boot)),
		kernel.MarkMutating(editscommit.NewCommand(boot)),
		kernel.MarkMutating(editsdiscard.NewCommand(boot)),
		editsstatus.NewCommand(boot),
		editsvalidate.NewCommand(boot),
	)
}

// `gplay games`: Play Games Services configuration (gamesConfiguration): a
// game's achievement and leaderboard configurations. A DISTINCT Google service
// (its own host, the shared androidpublisher scope, so no WithScope) addressed
// by the numeric Play Games application ID, its own ID space rather than the
// Android package (ADR-0033). `games`, `achievements`, and `leaderboards` are
// grouping nouns (kernel.Group). Writes affect the editable draft (there is no
// publish method: publishing to players is Console-only); list/view are reads,
// create/set are routine writes, and remove is the destructive tier
// (--confirm, exit 3 if missing). The write verbs follow ADR-0019 (`set`, one
// delete verb `remove`), renamed from update/delete in 2.0.0 (#597). See PRD #241 / ADR-0033 / CONTEXT.md (Play
// Games Services Publishing API).
//
// [experimental] (ADR-0010/ADR-0042): a second Google service on its own ID
// space, whose draft-only write model (no publish method) may well need a
// different command shape once someone runs a real game config through it.
func newGamesGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Experimental(kernel.Group("games", "Configure a game's Play Games Services resources (achievements, leaderboards)",
		kernel.Group("achievements", "Manage a game's achievement configurations (list/view/create/set/remove)",
			gamesachievementslist.NewCommand(boot),
			gamesachievementsview.NewCommand(boot),
			kernel.MarkMutating(gamesachievementscreate.NewCommand(boot)),
			kernel.MarkMutating(gamesachievementsset.NewCommand(boot)),
			kernel.MarkMutating(gamesachievementsremove.NewCommand(boot)),
		),
		kernel.Group("leaderboards", "Manage a game's leaderboard configurations (list/view/create/set/remove)",
			gamesleaderboardslist.NewCommand(boot),
			gamesleaderboardsview.NewCommand(boot),
			kernel.MarkMutating(gamesleaderboardscreate.NewCommand(boot)),
			kernel.MarkMutating(gamesleaderboardsset.NewCommand(boot)),
			kernel.MarkMutating(gamesleaderboardsremove.NewCommand(boot)),
		),
	))
}

// `gplay orders`: look up Google Play orders by order ID (orders.get /
// orders.batchget) and refund them (orders.refund). The admin-side commerce
// surface: orders ride the package/app axis like releases/metadata
// (applications/{packageName}/orders/...), NOT the developer-account axis: a
// human or agent holds an order ID from a complaint or payout report (no
// device token), distinct from runtime purchase-token verification, which
// gplay does not wrap (ADR-0031 / CONTEXT.md "Order"). `view` is a pure read
// (single #282 + batch #283), not marked mutating, not gated by
// GPLAY_READONLY. `refund` (#284) is the money-moving, irreversible write:
// MarkMutating so GPLAY_READONLY refuses it (exit 4), and it requires
// --confirm at the command layer (exit 3 if missing).
//
// [experimental] (ADR-0010/ADR-0042): money-moving, and the batch-vs-single
// shape of `view` is still unproven against real support workflows.
func newOrdersGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Experimental(kernel.Group("orders", "Look up and refund Google Play orders by order ID (admin commerce surface)",
		ordersview.NewCommand(boot),
		kernel.MarkMutating(ordersrefund.NewCommand(boot)),
	))
}

// `gplay subscriptions`: the declarative Monetization catalog, subscription
// side (PRD #51, walking skeleton #367 / ADR-0041). Package axis, Edit-free
// (applications/{packageName}/subscriptions/..., like device-tiers and orders).
// `pull` mirrors the live catalog into <productId>.json files: it writes only
// locally, so it is not marked mutating. `apply` reconciles the live catalog
// to the files (create/patch/delete): MarkMutating so GPLAY_READONLY refuses it
// (exit 4); a plan containing a delete additionally requires --confirm at the
// command layer (exit 3 if missing). Subscriber price migration is
// deliberately NOT reachable from apply: it arrives as its own gated command
// (slice #370).
//
// [experimental] (ADR-0010/ADR-0042): the whole declarative catalog shipped in
// v0.18.0, days before the 1.0 cut, and it is the most opinionated surface in
// the CLI: a file schema, a reconciliation model, and the v2-vs-legacy
// decision are all contract. It graduates once a real catalog has
// round-tripped through pull → apply.
func newSubscriptionsGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Experimental(kernel.Group("subscriptions", "Pull and apply the app's subscription catalog as files (declarative)",
		subscriptionspull.NewCommand(boot),
		kernel.MarkMutating(subscriptionsapply.NewCommand(boot)),
		// `prices` is a grouping noun; `convert` (monetization.convertRegionPrices)
		// is a pure computation (derives regional prices, writes nothing), so it
		// is not marked mutating. Domain verb admitted under ADR-0019 §2
		// (ADR-0041 §9).
		kernel.Group("prices", "Pricing helpers for the subscription catalog",
			subscriptionspricesconvert.NewCommand(boot),
			// `migrate` (basePlans.migratePrices) reprices EXISTING subscribers:
			// the sole imperative escape hatch of the catalog (ADR-0041 §4):
			// money-moving, --confirm-gated (exit 3), MarkMutating, never
			// reachable from apply.
			kernel.MarkMutating(subscriptionspricesmigrate.NewCommand(boot)),
		),
	))
}

// `gplay iap`: the one-time-product side of the Monetization catalog (slices
// #371–#372 / ADR-0041 §8). Same declarative pull/apply pair and the same axis
// as `subscriptions`; pull unions the v2 model with the read-only legacy
// inappproducts (never written in place), apply writes v2 only: MarkMutating
// so GPLAY_READONLY refuses it (exit 4), deletes gated by --confirm at the
// command layer (exit 3 if missing).
//
// [experimental] (ADR-0010/ADR-0042): same v0.18.0 batch and same file-schema
// exposure as `subscriptions`, plus the one-way legacy→v2 `--migrate`
// promotion: they graduate together.
func newIAPGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Experimental(kernel.Group("iap", "Pull and apply the app's one-time-product catalog as files (declarative)",
		iappull.NewCommand(boot),
		kernel.MarkMutating(iapapply.NewCommand(boot)),
	))
}

func newReviewsGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Group("reviews", "Read and reply to user reviews",
		reviewslist.NewCommand(boot),
		kernel.MarkMutating(reviewsreply.NewCommand(boot)),
		reviewsview.NewCommand(boot),
		// `reviews history` reads the monthly CSV reports from the developer's
		// Reporting bucket over the GCS API: a third Google service, so its leaf
		// mints a least-privilege devstorage.read_only token via WithScope
		// (ADR-0037).
		// [experimental] (ADR-0010/ADR-0042): the only leaf of a frozen namespace
		// that is not frozen: it reads UTF-16 CSVs out of a GCS bucket whose
		// layout Google owns and can change without an API version, so promising
		// its shape would be promising something gplay does not control.
		kernel.Experimental(kernel.WithScope(reviewshistory.NewCommand(boot), token.StorageReadOnlyScope)),
	)
}

// `gplay vitals`: read-only post-launch quality signals (crashes/ANR and the
// other metric sets) from the Play Developer Reporting API. A DISTINCT Google
// service: every leaf is wrapped with kernel.WithScope so it mints a
// least-privilege playdeveloperreporting-scoped token, never androidpublisher
// (ADR-0027 / #49). The whole namespace is read-only, so nothing is marked
// mutating.
func newVitalsGroup(boot kernel.Boot) *cobra.Command {
	leaves := []*cobra.Command{kernel.WithScope(vitalsquery.NewCommand(boot), token.ReportingScope)}
	// Opinionated metric-set presets (vitals crashes, vitals anr, …): built from
	// a data list so the family stays consistent; each requests the
	// least-privilege reporting scope like every other vitals leaf.
	for _, spec := range vitalscmd.Presets {
		leaves = append(leaves, kernel.WithScope(vitalscmd.NewPresetCommand(boot, spec), token.ReportingScope))
	}
	leaves = append(leaves,
		// `vitals errors`: counts / issues / reports. The group's leaves carry
		// the reporting scope themselves (see errorscmd.NewCommand), so it is
		// added bare.
		vitalserrors.NewCommand(boot),
		kernel.WithScope(vitalsanomalies.NewCommand(boot), token.ReportingScope),
	)
	return kernel.Group("vitals", "Read post-launch quality signals (crashes, ANRs, …) from Play vitals", leaves...)
}

// `gplay metadata`: Store front Listings (per-locale text), the fastlane-supply
// text side. See PRD #50 / ADR-0011.
func newMetadataGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Group("metadata", "Manage Store front Listings (per-locale title/description/video)",
		metadatalist.NewCommand(boot),
		metadatapull.NewCommand(boot),
		metadatavalidate.NewCommand(boot),
		kernel.MarkMutating(metadataapply.NewCommand(boot)),
		// `gplay metadata images`: Store images (per-locale icon / feature
		// graphic / screenshots), the fastlane-supply image side. See PRD #112 /
		// ADR-0013.
		kernel.Group("images", "Manage Store images (per-locale icon, feature graphic, screenshots)",
			metadataimageslist.NewCommand(boot),
			metadataimagespull.NewCommand(boot),
			metadataimagesvalidate.NewCommand(boot),
			kernel.MarkMutating(metadataimagesapply.NewCommand(boot)),
		),
	)
}

// `gplay compliance`: regulatory declarations that gate publication (Data
// Safety today; content rating and other "App content" surfaces in future
// scope). App-keyed, write-heavy, outside the Edits model: a distinct family
// from the store-presence namespaces. See PRD #114 / ADR-0014.
func newComplianceGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Group("compliance", "Manage an app's regulatory declarations (Data Safety, ...)",
		kernel.Group("datasafety", "Push and validate the app's Data Safety declaration (write-only)",
			compliancedatasafetyvalidate.NewCommand(boot),
			kernel.MarkMutating(compliancedatasafetyset.NewCommand(boot)),
		),
	)
}
