package main

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
	ordersvoided "github.com/PollyGlot/google-play-cli/commands/orders/voided"
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
	systemapkscreate "github.com/PollyGlot/google-play-cli/commands/releases/system-apks/create"
	systemapksdownload "github.com/PollyGlot/google-play-cli/commands/releases/system-apks/download"
	systemapkslist "github.com/PollyGlot/google-play-cli/commands/releases/system-apks/list"
	systemapksview "github.com/PollyGlot/google-play-cli/commands/releases/system-apks/view"
	"github.com/PollyGlot/google-play-cli/commands/releases/upload"
	reportsfamily "github.com/PollyGlot/google-play-cli/commands/reports/familycmd"
	reportslist "github.com/PollyGlot/google-play-cli/commands/reports/listcmd"
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
	vitalsreleases "github.com/PollyGlot/google-play-cli/commands/vitals/releasescmd"
	"github.com/PollyGlot/google-play-cli/commands/vitals/vitalscmd"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/reports"
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

func newAppsGroup(boot kernel.Boot) *cobra.Command {
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
		kernel.Experimental(auditcmd.NewCommand(boot)),
	)
}

func newReleasesGroup(boot kernel.Boot) *cobra.Command {
	mappings := kernel.Group("mappings", "Manage ProGuard/R8 deobfuscation mappings (symbolicate Play vitals crash stacks)",
		kernel.MarkMutating(releasesmappings.NewCommand(boot)),
	)

	sharing := kernel.Experimental(kernel.Group("sharing", "Upload builds to Internal App Sharing (private shareable links)",
		kernel.MarkMutating(sharingupload.NewCommand(boot)),
	))

	expansionFiles := kernel.Experimental(kernel.Group("expansion-files", "Manage legacy OBB expansion files (superseded by Play Asset Delivery)",
		kernel.MarkMutating(expansionupload.NewCommand(boot)),
		kernel.MarkMutating(expansionset.NewCommand(boot)),
		expansionview.NewCommand(boot),
	))

	generated := kernel.Experimental(kernel.Group("generated", "List and download the APKs Play generates from an AAB",
		generatedlist.NewCommand(boot),
		generateddownload.NewCommand(boot),
	))

	systemApks := kernel.Experimental(kernel.Group("system-apks", "Create, list and download system APK variants (device system images)",
		kernel.MarkMutating(systemapkscreate.NewCommand(boot)),
		systemapkslist.NewCommand(boot),
		systemapksview.NewCommand(boot),
		systemapksdownload.NewCommand(boot),
	))

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
		systemApks,
		artifacts,
	)
}

func newTracksGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Group("tracks", "Inspect and create release tracks (standard and custom closed)",
		trackslist.NewCommand(boot),
		tracksview.NewCommand(boot),
		kernel.MarkMutating(trackscreate.NewCommand(boot)),
		tracksavailability.NewCommand(boot),
	)
}

func newTestersGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Group("testers", "Manage the Google Groups authorized to test a track",
		testerslist.NewCommand(boot),
		kernel.MarkMutating(testersset.NewCommand(boot)),
	)
}

func newDeviceTiersGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Experimental(kernel.Group("device-tiers", "Manage device tier configs (device-targeting for tiered delivery)",
		kernel.MarkMutating(devicetierscreate.NewCommand(boot)),
		devicetiersview.NewCommand(boot),
		devicetierslist.NewCommand(boot),
	))
}

func newRecoveryGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Experimental(kernel.Group("recovery", "Manage app recovery actions (incident-response remediation for a bad release)",
		kernel.MarkMutating(recoverycreate.NewCommand(boot)),
		recoverylist.NewCommand(boot),
		kernel.MarkMutating(recoverydeploy.NewCommand(boot)),
		kernel.MarkMutating(recoverycancel.NewCommand(boot)),
		kernel.MarkMutating(recoveryaddtargeting.NewCommand(boot)),
	))
}

func newSigningGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Experimental(kernel.Group("signing", "Manage Play App Signing with a self-hosted Cloud KMS key (enterprise key custody)",
		kernel.MarkMutating(signingenroll.NewCommand(boot)),
		kernel.MarkMutating(signingrotate.NewCommand(boot)),
	))
}

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

func newCustomAppsGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Experimental(kernel.Group("customapps", "Create managed Google Play private apps (organisation-scoped)",
		kernel.MarkMutating(customappscreate.NewCommand(boot)),
	))
}

func newAppStoreGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Experimental(kernel.Group("appstore", "Alternative app store operations (catalog export, hosted app review)",
		kernel.MarkMutating(appstorecreate.NewCommand(boot)),
		kernel.Group("catalog", "Read Google Play's catalog export for app stores",
			appstorecatalogview.NewCommand(boot),
			kernel.Group("events", "Read the catalog update-event feed (incremental catalog sync)",
				appstorecatalogeventslist.NewCommand(boot),
			),
		),
		kernel.Group("apk", "Upload hosted app APKs",
			kernel.MarkMutating(appstoreapkupload.NewCommand(boot)),
		),
		kernel.Group("image", "Upload hosted app listing images",
			kernel.MarkMutating(appstoreimageupload.NewCommand(boot)),
		),
		kernel.Group("policy", "Upload hosted app policy declaration documents",
			kernel.MarkMutating(appstorepolicyupload.NewCommand(boot)),
		),
		kernel.Group("publish-status", "Manage a hosted app's storefront visibility (published or unpublished)",
			kernel.MarkMutating(appstorepublishstatusset.NewCommand(boot)),
		),
		kernel.MarkMutating(appstoresubmit.NewCommand(boot)),
	))
}

func newEditsGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Group("edits", "Manage explicit Edit transactions (begin, validate, commit, discard, status)",
		kernel.MarkMutating(editsbegin.NewCommand(boot)),
		kernel.MarkMutating(editscommit.NewCommand(boot)),
		kernel.MarkMutating(editsdiscard.NewCommand(boot)),
		editsstatus.NewCommand(boot),
		editsvalidate.NewCommand(boot),
	)
}

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

func newOrdersGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Experimental(kernel.Group("orders", "Look up, refund and reconcile Google Play orders (admin commerce surface)",
		ordersview.NewCommand(boot),
		ordersvoided.NewCommand(boot),
		kernel.MarkMutating(ordersrefund.NewCommand(boot)),
	))
}

func newSubscriptionsGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Experimental(kernel.Group("subscriptions", "Pull and apply the app's subscription catalog as files (declarative)",
		subscriptionspull.NewCommand(boot),
		kernel.MarkMutating(subscriptionsapply.NewCommand(boot)),
		kernel.Group("prices", "Pricing helpers for the subscription catalog",
			subscriptionspricesconvert.NewCommand(boot),
			kernel.MarkMutating(subscriptionspricesmigrate.NewCommand(boot)),
		),
	))
}

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
		kernel.Experimental(kernel.WithScope(reviewshistory.NewCommand(boot), token.StorageReadOnlyScope)),
	)
}

func newReportsGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Experimental(kernel.Group("reports", "Read the monthly Bulk reports (installs, ...) from the Reporting bucket",
		kernel.WithScope(reportslist.NewCommand(boot), token.StorageReadOnlyScope),
		kernel.WithScope(reportsfamily.NewCommand(boot, reports.Installs), token.StorageReadOnlyScope),
		kernel.WithScope(reportsfamily.NewCommand(boot, reports.Ratings), token.StorageReadOnlyScope),
		kernel.WithScope(reportsfamily.NewCommand(boot, reports.Crashes), token.StorageReadOnlyScope),
		kernel.WithScope(reportsfamily.NewCommand(boot, reports.StorePerformance), token.StorageReadOnlyScope),
		kernel.WithScope(reportsfamily.NewCommand(boot, reports.SubscriptionStats), token.StorageReadOnlyScope),
		kernel.WithScope(reportsfamily.NewCommand(boot, reports.Earnings), token.StorageReadOnlyScope),
		kernel.WithScope(reportsfamily.NewCommand(boot, reports.Sales), token.StorageReadOnlyScope),
	))
}

func newVitalsGroup(boot kernel.Boot) *cobra.Command {
	leaves := []*cobra.Command{kernel.WithScope(vitalsquery.NewCommand(boot), token.ReportingScope)}
	for _, spec := range vitalscmd.Presets {
		leaves = append(leaves, kernel.WithScope(vitalscmd.NewPresetCommand(boot, spec), token.ReportingScope))
	}
	leaves = append(leaves,
		vitalserrors.NewCommand(boot),
		kernel.WithScope(vitalsanomalies.NewCommand(boot), token.ReportingScope),
		kernel.Experimental(kernel.WithScope(vitalsreleases.NewCommand(boot), token.ReportingScope)),
	)
	return kernel.Group("vitals", "Read post-launch quality signals (crashes, ANRs, …) from Play vitals", leaves...)
}

func newMetadataGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Group("metadata", "Manage Store front Listings (per-locale title/description/video)",
		metadatalist.NewCommand(boot),
		metadatapull.NewCommand(boot),
		metadatavalidate.NewCommand(boot),
		kernel.MarkMutating(metadataapply.NewCommand(boot)),
		kernel.Group("images", "Manage Store images (per-locale icon, feature graphic, screenshots)",
			metadataimageslist.NewCommand(boot),
			metadataimagespull.NewCommand(boot),
			metadataimagesvalidate.NewCommand(boot),
			kernel.MarkMutating(metadataimagesapply.NewCommand(boot)),
		),
	)
}

func newComplianceGroup(boot kernel.Boot) *cobra.Command {
	return kernel.Group("compliance", "Manage an app's regulatory declarations (Data Safety, ...)",
		kernel.Group("datasafety", "Push and validate the app's Data Safety declaration (write-only)",
			compliancedatasafetyvalidate.NewCommand(boot),
			kernel.MarkMutating(compliancedatasafetyset.NewCommand(boot)),
		),
	)
}
