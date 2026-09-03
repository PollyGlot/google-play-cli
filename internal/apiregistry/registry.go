// Package apiregistry declares, in one place, every Google Developer API
// method a shipped `gplay` command calls, mapped to the command that calls it.
//
// The CLI hand-rolls its HTTP calls package by package (ADR-0007), so before
// this registry existed there was no single answer to "which API methods does
// gplay depend on?". The Monday Discovery refresh could therefore delete or
// deprecate a method under a shipped command and CI would stay green.
//
// The registry closes that hole: registry_test.go anchors every entry to the
// offline Discovery artefacts (the embedded Schema index, `docs/discovery/
// paths.txt` and the snapshots), so a removed or deprecated method turns the
// refresh PR red, naming the method id AND the command that breaks. That is the
// deterministic evidence the Discovery triage bot cites before any model is
// involved (PRD #501).
//
// It is declarative on purpose: no call site imports it, nothing here is
// compiled into a request. Adding a command that calls a new method means
// adding one line here, and the COVERAGE cross-check below fails until you do.
//
// Scope: Google Discovery-documented APIs only. `gplay reviews history` also
// reads the Cloud Storage JSON API (`storage.objects.list`/`get`), which has no
// snapshot under docs/discovery/ and therefore nothing to anchor to.
package apiregistry

// Entry is one API method the CLI calls, with the leaf commands that call it.
// MethodID is Google's native RPC id as keyed by the Schema index and
// paths.txt (service prefix included, e.g.
// `androidpublisher.edits.tracks.update`); Commands are `gplay` command paths
// without the binary name (e.g. "tracks list").
type Entry struct {
	MethodID string
	Commands []string
	// Note carries the non-obvious "why" of an entry (a method shared by
	// several flows, a custom verb, an upload host). Empty when the mapping
	// speaks for itself.
	Note string
}

// Entries returns the registry. It is a function rather than an exported slice
// so no caller can mutate the shared backing array.
func Entries() []Entry {
	out := make([]Entry, len(entries))
	copy(out, entries)
	return out
}

// entries is the registry itself, ordered by service then by method id so a
// diff on this file reads like a diff on paths.txt.
var entries = []Entry{
	// --- androidpublisher: Edit lifecycle -------------------------------
	{
		MethodID: "androidpublisher.edits.insert",
		Commands: []string{"edits begin"},
		Note:     "also opened implicitly by every Edit-scoped command (implicit Edit mode, docs/DESIGN.md)",
	},
	{
		MethodID: "androidpublisher.edits.commit",
		Commands: []string{"edits commit"},
		Note:     "also called implicitly at the end of every mutating Edit-scoped command",
	},
	{
		MethodID: "androidpublisher.edits.delete",
		Commands: []string{"edits discard"},
		Note:     "also called to drop the read-only Edit opened by list/view commands",
	},

	// --- androidpublisher: Edit-scoped resources ------------------------
	{MethodID: "androidpublisher.edits.apks.upload", Commands: []string{"releases upload"}, Note: "an .apk payload rides releases upload (ADR-0036)"},
	{MethodID: "androidpublisher.edits.bundles.upload", Commands: []string{"releases upload"}, Note: "the .aab path, resumable upload host"},
	{MethodID: "androidpublisher.edits.countryavailability.get", Commands: []string{"tracks availability"}},
	{MethodID: "androidpublisher.edits.deobfuscationfiles.upload", Commands: []string{"releases mappings upload"}},
	{MethodID: "androidpublisher.edits.details.get", Commands: []string{"apps details view", "apps view", "apps audit"}},
	{MethodID: "androidpublisher.edits.details.patch", Commands: []string{"apps details set"}},
	{MethodID: "androidpublisher.edits.expansionfiles.get", Commands: []string{"releases expansion-files view"}},
	{MethodID: "androidpublisher.edits.expansionfiles.update", Commands: []string{"releases expansion-files set"}},
	{MethodID: "androidpublisher.edits.expansionfiles.upload", Commands: []string{"releases expansion-files upload"}},
	{MethodID: "androidpublisher.edits.images.delete", Commands: []string{"metadata images apply"}, Note: "--prune drops one live image by id"},
	{MethodID: "androidpublisher.edits.images.deleteall", Commands: []string{"metadata images apply"}, Note: "slot reconciliation clears a slot before re-upload (ADR-0013)"},
	{MethodID: "androidpublisher.edits.images.list", Commands: []string{"metadata images list", "metadata images pull", "metadata images apply"}},
	{MethodID: "androidpublisher.edits.images.upload", Commands: []string{"metadata images apply"}},
	{MethodID: "androidpublisher.edits.listings.delete", Commands: []string{"metadata apply"}, Note: "--prune drops a locale that left the local tree"},
	{MethodID: "androidpublisher.edits.listings.get", Commands: []string{"metadata pull"}},
	{MethodID: "androidpublisher.edits.listings.list", Commands: []string{"metadata list", "metadata pull", "metadata images list"}},
	{MethodID: "androidpublisher.edits.listings.patch", Commands: []string{"metadata apply"}},
	{MethodID: "androidpublisher.edits.testers.get", Commands: []string{"testers list"}},
	{MethodID: "androidpublisher.edits.testers.update", Commands: []string{"testers set"}},
	{MethodID: "androidpublisher.edits.tracks.create", Commands: []string{"tracks create"}},
	{
		MethodID: "androidpublisher.edits.tracks.get",
		Commands: []string{"tracks view", "releases list", "releases promote", "releases rollout"},
		Note:     "read-before-write: tracks.update replaces the whole releases array",
	},
	{MethodID: "androidpublisher.edits.tracks.list", Commands: []string{"tracks list", "apps audit"}},
	{
		MethodID: "androidpublisher.edits.tracks.update",
		Commands: []string{"releases upload", "releases promote", "releases rollout", "releases halt", "releases resume", "releases complete"},
		Note:     "the single write behind every release-state change (ADR-0002)",
	},

	// --- androidpublisher: application-scoped (Edit-free) ---------------
	{MethodID: "androidpublisher.applications.dataSafety", Commands: []string{"compliance datasafety set"}},
	{MethodID: "androidpublisher.applications.deviceTierConfigs.create", Commands: []string{"device-tiers create"}},
	{MethodID: "androidpublisher.applications.deviceTierConfigs.get", Commands: []string{"device-tiers view"}},
	{MethodID: "androidpublisher.applications.deviceTierConfigs.list", Commands: []string{"device-tiers list"}},
	{MethodID: "androidpublisher.apprecovery.addTargeting", Commands: []string{"recovery add-targeting"}},
	{MethodID: "androidpublisher.apprecovery.cancel", Commands: []string{"recovery cancel"}},
	{MethodID: "androidpublisher.apprecovery.create", Commands: []string{"recovery create"}},
	{MethodID: "androidpublisher.apprecovery.deploy", Commands: []string{"recovery deploy"}},
	{MethodID: "androidpublisher.apprecovery.list", Commands: []string{"recovery list"}},
	{MethodID: "androidpublisher.appsigning.enrollApp", Commands: []string{"signing enroll"}, Note: "--confirm-gated (ADR-0043)"},
	{MethodID: "androidpublisher.appsigning.rotateAppSigningKey", Commands: []string{"signing rotate"}, Note: "--confirm-gated (ADR-0043)"},
	{MethodID: "androidpublisher.generatedapks.download", Commands: []string{"releases generated download"}, Note: "binary body written to a file (ADR-0034)"},
	{MethodID: "androidpublisher.generatedapks.list", Commands: []string{"releases generated list"}},
	{MethodID: "androidpublisher.internalappsharingartifacts.uploadapk", Commands: []string{"releases sharing upload"}},
	{MethodID: "androidpublisher.internalappsharingartifacts.uploadbundle", Commands: []string{"releases sharing upload"}},
	{MethodID: "androidpublisher.reviews.get", Commands: []string{"reviews view"}},
	{MethodID: "androidpublisher.reviews.list", Commands: []string{"reviews list"}},
	{MethodID: "androidpublisher.reviews.reply", Commands: []string{"reviews reply"}},

	// --- androidpublisher: alternative app stores (DMA) -----------------
	{MethodID: "androidpublisher.appstoreappsreview.createappstorehostedapp", Commands: []string{"appstore create"}},
	{MethodID: "androidpublisher.appstoreappsreview.updateappstorehostedapp", Commands: []string{"appstore update"}, Note: "--confirm-gated: sends the app to review (ADR-0043)"},
	{MethodID: "androidpublisher.appstoreappsreview.updateappstorehostedapppublishstatus", Commands: []string{"appstore publish-status"}},
	{MethodID: "androidpublisher.appstoreappsreview.uploadapk", Commands: []string{"appstore upload apk"}},
	{MethodID: "androidpublisher.appstoreappsreview.uploadappstoreapppolicydeclarationfile", Commands: []string{"appstore upload policy"}},
	{MethodID: "androidpublisher.appstoreappsreview.uploadimage", Commands: []string{"appstore upload image"}},
	{MethodID: "androidpublisher.appstorecatalog.recentappviews.get", Commands: []string{"appstore catalog view"}},
	{MethodID: "androidpublisher.appstorecatalog.recentupdateevents.list", Commands: []string{"appstore catalog events list"}},

	// --- androidpublisher: commerce -------------------------------------
	{MethodID: "androidpublisher.orders.batchget", Commands: []string{"orders view"}, Note: "the multi-id form of orders view"},
	{MethodID: "androidpublisher.orders.get", Commands: []string{"orders view"}},
	{MethodID: "androidpublisher.orders.refund", Commands: []string{"orders refund"}, Note: "--confirm-gated, money-moving (ADR-0031)"},
	{
		MethodID: "androidpublisher.inappproducts.list",
		Commands: []string{"iap pull"},
		Note:     "read-only legacy union; gplay never writes the legacy surface (ADR-0041 §8)",
	},
	{MethodID: "androidpublisher.monetization.convertRegionPrices", Commands: []string{"subscriptions prices convert"}},
	{MethodID: "androidpublisher.monetization.onetimeproducts.delete", Commands: []string{"iap apply"}},
	{MethodID: "androidpublisher.monetization.onetimeproducts.list", Commands: []string{"iap pull"}},
	{MethodID: "androidpublisher.monetization.onetimeproducts.patch", Commands: []string{"iap apply"}, Note: "allowMissing makes patch the create: the API has no insert"},
	{MethodID: "androidpublisher.monetization.onetimeproducts.purchaseOptions.offers.batchDelete", Commands: []string{"iap apply"}},
	{MethodID: "androidpublisher.monetization.onetimeproducts.purchaseOptions.offers.batchUpdate", Commands: []string{"iap apply"}, Note: "the only offer write path on one-time products"},
	{MethodID: "androidpublisher.monetization.onetimeproducts.purchaseOptions.offers.list", Commands: []string{"iap pull"}},
	{MethodID: "androidpublisher.monetization.subscriptions.basePlans.activate", Commands: []string{"subscriptions apply"}, Note: "declarative `state:` on a base plan"},
	{MethodID: "androidpublisher.monetization.subscriptions.basePlans.deactivate", Commands: []string{"subscriptions apply"}},
	{MethodID: "androidpublisher.monetization.subscriptions.basePlans.migratePrices", Commands: []string{"subscriptions prices migrate"}, Note: "--confirm-gated, reprices live subscribers"},
	{MethodID: "androidpublisher.monetization.subscriptions.basePlans.offers.activate", Commands: []string{"subscriptions apply"}},
	{MethodID: "androidpublisher.monetization.subscriptions.basePlans.offers.create", Commands: []string{"subscriptions apply"}},
	{MethodID: "androidpublisher.monetization.subscriptions.basePlans.offers.deactivate", Commands: []string{"subscriptions apply"}},
	{MethodID: "androidpublisher.monetization.subscriptions.basePlans.offers.delete", Commands: []string{"subscriptions apply"}},
	{MethodID: "androidpublisher.monetization.subscriptions.basePlans.offers.list", Commands: []string{"subscriptions pull"}},
	{MethodID: "androidpublisher.monetization.subscriptions.basePlans.offers.patch", Commands: []string{"subscriptions apply"}},
	{MethodID: "androidpublisher.monetization.subscriptions.create", Commands: []string{"subscriptions apply"}},
	{MethodID: "androidpublisher.monetization.subscriptions.delete", Commands: []string{"subscriptions apply"}},
	{MethodID: "androidpublisher.monetization.subscriptions.list", Commands: []string{"subscriptions pull"}},
	{
		MethodID: "androidpublisher.monetization.subscriptions.patch",
		Commands: []string{"subscriptions apply"},
		Note:     "also carries base-plan config and per-territory prices: the sub-resource has no create/patch",
	},

	// --- androidpublisher: team -----------------------------------------
	{MethodID: "androidpublisher.grants.create", Commands: []string{"team grants set"}},
	{MethodID: "androidpublisher.grants.delete", Commands: []string{"team grants remove"}},
	{MethodID: "androidpublisher.grants.patch", Commands: []string{"team grants set"}},
	{MethodID: "androidpublisher.users.create", Commands: []string{"team users add"}},
	{MethodID: "androidpublisher.users.delete", Commands: []string{"team users remove"}},
	{MethodID: "androidpublisher.users.list", Commands: []string{"team users list", "team users view", "team grants list"}},
	{MethodID: "androidpublisher.users.patch", Commands: []string{"team users set"}},

	// --- playdeveloperreporting -----------------------------------------
	{MethodID: "playdeveloperreporting.anomalies.list", Commands: []string{"vitals anomalies"}},
	{MethodID: "playdeveloperreporting.apps.search", Commands: []string{"apps accessible list"}, Note: "server-authoritative discovery, distinct from the local registry (ADR-0039)"},
	{MethodID: "playdeveloperreporting.vitals.anonrssandswapmemoryusage.query", Commands: []string{"vitals query anonrssandswapmemoryusage"}},
	{MethodID: "playdeveloperreporting.vitals.anrrate.query", Commands: []string{"vitals anr", "vitals query anrrate"}},
	{MethodID: "playdeveloperreporting.vitals.bitmapmemoryusage.query", Commands: []string{"vitals query bitmapmemoryusage"}},
	{MethodID: "playdeveloperreporting.vitals.crashrate.query", Commands: []string{"vitals crashes", "vitals query crashrate"}},
	{MethodID: "playdeveloperreporting.vitals.errors.counts.query", Commands: []string{"vitals errors counts"}},
	{MethodID: "playdeveloperreporting.vitals.errors.issues.search", Commands: []string{"vitals errors issues"}},
	{MethodID: "playdeveloperreporting.vitals.errors.reports.search", Commands: []string{"vitals errors reports"}},
	{MethodID: "playdeveloperreporting.vitals.excessivewakeuprate.query", Commands: []string{"vitals excessivewakeup", "vitals query excessivewakeuprate"}},
	{MethodID: "playdeveloperreporting.vitals.lmkrate.query", Commands: []string{"vitals lmk", "vitals query lmkrate"}},
	{MethodID: "playdeveloperreporting.vitals.slowrenderingrate.query", Commands: []string{"vitals slowrendering", "vitals query slowrenderingrate"}},
	{MethodID: "playdeveloperreporting.vitals.slowstartrate.query", Commands: []string{"vitals slowstart", "vitals query slowstartrate"}},
	{MethodID: "playdeveloperreporting.vitals.stuckbackgroundwakelockrate.query", Commands: []string{"vitals stuckbgwakelock", "vitals query stuckbackgroundwakelockrate"}},

	// --- playcustomapp ---------------------------------------------------
	{MethodID: "playcustomapp.accounts.customApps.create", Commands: []string{"customapps create"}, Note: "account-scoped, not package-scoped (ADR-0032)"},

	// --- gamesConfiguration ----------------------------------------------
	{MethodID: "gamesConfiguration.achievementConfigurations.delete", Commands: []string{"games achievements delete"}},
	{MethodID: "gamesConfiguration.achievementConfigurations.get", Commands: []string{"games achievements view"}},
	{MethodID: "gamesConfiguration.achievementConfigurations.insert", Commands: []string{"games achievements create"}},
	{MethodID: "gamesConfiguration.achievementConfigurations.list", Commands: []string{"games achievements list"}},
	{MethodID: "gamesConfiguration.achievementConfigurations.update", Commands: []string{"games achievements update"}},
	{MethodID: "gamesConfiguration.leaderboardConfigurations.delete", Commands: []string{"games leaderboards delete"}},
	{MethodID: "gamesConfiguration.leaderboardConfigurations.get", Commands: []string{"games leaderboards view"}},
	{MethodID: "gamesConfiguration.leaderboardConfigurations.insert", Commands: []string{"games leaderboards create"}},
	{MethodID: "gamesConfiguration.leaderboardConfigurations.list", Commands: []string{"games leaderboards list"}},
	{MethodID: "gamesConfiguration.leaderboardConfigurations.update", Commands: []string{"games leaderboards update"}},
}

// CoverageExceptions lists the ✅ rows of docs/COVERAGE.md whose surface the CLI
// covers WITHOUT calling that surface's own methods. Without this the COVERAGE
// cross-check would demand a registry entry for a method gplay does not call,
// and the registry would start lying about the CLI's real dependencies.
//
// Keyed by the surface as spelled in COVERAGE.md's first column.
var CoverageExceptions = map[string]string{
	"applications.tracks.releases.list": "`releases list` reads the same releases through " +
		"androidpublisher.edits.tracks.get inside a read-only Edit; the Edit-free list method is not called",
}
