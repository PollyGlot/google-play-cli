package apiregistry

import (
	"fmt"
	"strings"
)

// Redundant is one API method gplay deliberately does not call because a
// *called* method already serves the same resource in another shape: `update`
// next to a called `patch`, `get`/`batchGet` next to a called `list`, a batch
// state verb next to the unary verbs the declarative apply drives. ADR-0047.
//
// The declaration is honest only while CanonicalID is registered: lintDispositions
// (and the offline test around it) fails the moment the canonical loses its
// command, and the method becomes a gap again instead of hiding behind a stale
// disposition.
type Redundant struct {
	MethodID    string
	CanonicalID string
	Reason      string
}

// Parked is one API method whose decision is deferred to a `type:parking`
// issue: it stays uncovered in docs/COVERAGE.md, but the row links the issue
// so a 🔴 mark is never an unexplained hole. ADR-0047.
type Parked struct {
	MethodID string
	Issue    int
	Reason   string
}

// Redundancies returns the redundant list. A function, like Entries, so no
// caller can mutate the shared backing array.
func Redundancies() []Redundant {
	out := make([]Redundant, len(redundancies))
	copy(out, redundancies)
	return out
}

// Parkings returns the parked list, same contract as Redundancies.
func Parkings() []Parked {
	out := make([]Parked, len(parkings))
	copy(out, parkings)
	return out
}

// LintDispositions checks the four hand-written lists against each other and
// against the method universe (`known`, the ids of docs/discovery/paths.txt):
//
//   - a method carries at most one disposition (registered, excluded,
//     redundant or parked);
//   - every disposition names a method paths.txt knows;
//   - a redundant entry's canonical is a *registered* method: the invariant
//     that keeps "redundant" from becoming "unshipped";
//   - a parked entry names an issue (> 0);
//   - every entry says why.
//
// It returns every violation rather than the first so one run names them all.
// Both the registry tests and the coverage renderer call it, so a violation
// fails offline in two places before it can be committed.
func LintDispositions(known map[string]bool) []error {
	return lintDispositions(known, entries, exclusions, redundancies, parkings)
}

func lintDispositions(known map[string]bool, entries []Entry, exclusions []Exclusion, redundancies []Redundant, parkings []Parked) []error {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	registered := map[string]bool{}
	for _, e := range entries {
		registered[e.MethodID] = true
	}
	excluded := map[string]bool{}
	for _, x := range exclusions {
		excluded[x.MethodID] = true
	}

	// disposition records the first list that claimed an id so a second claim
	// can name both.
	disposition := map[string]string{}
	for id := range registered {
		disposition[id] = "registered"
	}
	for id := range excluded {
		if prev, dup := disposition[id]; dup {
			fail("method %q is both %s and excluded", id, prev)
			continue
		}
		disposition[id] = "excluded"
	}

	seenRedundant := map[string]bool{}
	for _, r := range redundancies {
		if r.MethodID == "" {
			fail("redundant entry with an empty method id")
			continue
		}
		if seenRedundant[r.MethodID] {
			fail("method %q is declared redundant twice", r.MethodID)
		}
		seenRedundant[r.MethodID] = true
		if !known[r.MethodID] {
			fail("redundant method %q is absent from paths.txt: run `make discovery-update`, or fix the id", r.MethodID)
		}
		if prev, dup := disposition[r.MethodID]; dup {
			fail("method %q is both %s and redundant", r.MethodID, prev)
		}
		disposition[r.MethodID] = "redundant"
		if !registered[r.CanonicalID] {
			fail("redundant method %q names canonical %q, which no shipped command calls: register the canonical or drop the disposition (the method is a gap again)", r.MethodID, r.CanonicalID)
		}
		if strings.TrimSpace(r.Reason) == "" {
			fail("redundant method %q has no reason", r.MethodID)
		}
	}

	seenParked := map[string]bool{}
	for _, p := range parkings {
		if p.MethodID == "" {
			fail("parked entry with an empty method id")
			continue
		}
		if seenParked[p.MethodID] {
			fail("method %q is parked twice", p.MethodID)
		}
		seenParked[p.MethodID] = true
		if !known[p.MethodID] {
			fail("parked method %q is absent from paths.txt: run `make discovery-update`, or fix the id", p.MethodID)
		}
		if prev, dup := disposition[p.MethodID]; dup {
			fail("method %q is both %s and parked", p.MethodID, prev)
		}
		disposition[p.MethodID] = "parked"
		if p.Issue <= 0 {
			fail("parked method %q names no issue: a parked decision is traced to a `type:parking` issue or it is a plain gap", p.MethodID)
		}
		if strings.TrimSpace(p.Reason) == "" {
			fail("parked method %q has no reason", p.MethodID)
		}
	}
	return errs
}

// redundancies is ordered by method id, like entries and exclusions, so a
// diff reads like a diff on paths.txt. Each pair must be agreeable in one
// glance: the canonical is the method a shipped command calls on the *same*
// resource; the reason names the shape difference.
var redundancies = []Redundant{
	{
		MethodID:    "androidpublisher.applications.tracks.releases.list",
		CanonicalID: "androidpublisher.edits.tracks.list",
		Reason:      "releases are read per track inside an Edit by `tracks list`; this is the Edit-less projection of the same data",
	},
	{
		MethodID:    "androidpublisher.edits.details.update",
		CanonicalID: "androidpublisher.edits.details.patch",
		Reason:      "full-body `update` of App details; `apps details set` writes field by field through `patch`",
	},
	{
		MethodID:    "androidpublisher.edits.expansionfiles.patch",
		CanonicalID: "androidpublisher.edits.expansionfiles.update",
		Reason:      "partial write of a single-field resource; `releases expansion-files set` uses `update`",
	},
	{
		MethodID:    "androidpublisher.edits.listings.deleteall",
		CanonicalID: "androidpublisher.edits.listings.delete",
		Reason:      "drops every locale at once; `metadata apply --prune` deletes locale by locale and refuses the defaultLanguage Listing (ADR-0011)",
	},
	{
		MethodID:    "androidpublisher.edits.testers.patch",
		CanonicalID: "androidpublisher.edits.testers.update",
		Reason:      "partial write of a single-field resource; `testers set` replaces the whole group list through `update`",
	},
	{
		MethodID:    "androidpublisher.edits.tracks.patch",
		CanonicalID: "androidpublisher.edits.tracks.update",
		Reason:      "partial write of a track; every release command sends the full track body through `update`",
	},
	{
		MethodID:    "androidpublisher.inappproducts.batchDelete",
		CanonicalID: "androidpublisher.monetization.onetimeproducts.delete",
		Reason:      "legacy write; gplay never writes the legacy surface, `iap apply` deletes through v2 (ADR-0041 §8)",
	},
	{
		MethodID:    "androidpublisher.inappproducts.batchGet",
		CanonicalID: "androidpublisher.inappproducts.list",
		Reason:      "multi-id read; `iap pull` reads the whole legacy catalog through `list` (ADR-0041 §8)",
	},
	{
		MethodID:    "androidpublisher.inappproducts.batchUpdate",
		CanonicalID: "androidpublisher.monetization.onetimeproducts.patch",
		Reason:      "legacy write; gplay never writes the legacy surface, `iap apply` writes through v2 (ADR-0041 §8)",
	},
	{
		MethodID:    "androidpublisher.inappproducts.delete",
		CanonicalID: "androidpublisher.monetization.onetimeproducts.delete",
		Reason:      "legacy write; gplay never writes the legacy surface, `iap apply` deletes through v2 (ADR-0041 §8)",
	},
	{
		MethodID:    "androidpublisher.inappproducts.get",
		CanonicalID: "androidpublisher.inappproducts.list",
		Reason:      "single-id read; `iap pull` reads the whole legacy catalog through `list` (ADR-0041 §8)",
	},
	{
		MethodID:    "androidpublisher.inappproducts.insert",
		CanonicalID: "androidpublisher.monetization.onetimeproducts.patch",
		Reason:      "legacy write; gplay never writes the legacy surface, `iap apply` creates through v2 `patch` with allowMissing (ADR-0041 §8)",
	},
	{
		MethodID:    "androidpublisher.inappproducts.patch",
		CanonicalID: "androidpublisher.monetization.onetimeproducts.patch",
		Reason:      "legacy write; gplay never writes the legacy surface, `iap apply` writes through v2 (ADR-0041 §8)",
	},
	{
		MethodID:    "androidpublisher.inappproducts.update",
		CanonicalID: "androidpublisher.monetization.onetimeproducts.patch",
		Reason:      "legacy write; gplay never writes the legacy surface, `iap apply` writes through v2 (ADR-0041 §8)",
	},
	{
		MethodID:    "androidpublisher.monetization.onetimeproducts.batchDelete",
		CanonicalID: "androidpublisher.monetization.onetimeproducts.delete",
		Reason:      "multi-id form; `iap apply` deletes one product per call so each refusal maps to one product",
	},
	{
		MethodID:    "androidpublisher.monetization.onetimeproducts.batchGet",
		CanonicalID: "androidpublisher.monetization.onetimeproducts.list",
		Reason:      "multi-id read; `iap pull` reads the whole catalog through `list`",
	},
	{
		MethodID:    "androidpublisher.monetization.onetimeproducts.batchUpdate",
		CanonicalID: "androidpublisher.monetization.onetimeproducts.patch",
		Reason:      "multi-id form; `iap apply` upserts one product per call so each refusal maps to one product",
	},
	{
		MethodID:    "androidpublisher.monetization.onetimeproducts.get",
		CanonicalID: "androidpublisher.monetization.onetimeproducts.list",
		Reason:      "single-id read; `iap pull` reads the whole catalog through `list`",
	},
	{
		MethodID:    "androidpublisher.monetization.onetimeproducts.purchaseOptions.batchDelete",
		CanonicalID: "androidpublisher.monetization.onetimeproducts.patch",
		Reason:      "purchase options are nested in the product body; a dropped option leaves through the product `patch` (ADR-0041)",
	},
	{
		MethodID:    "androidpublisher.monetization.onetimeproducts.purchaseOptions.offers.batchGet",
		CanonicalID: "androidpublisher.monetization.onetimeproducts.purchaseOptions.offers.list",
		Reason:      "multi-id read; `iap pull` reads every offer of a purchase option through `list`",
	},
	{
		MethodID:    "androidpublisher.monetization.onetimeproducts.purchaseOptions.offers.batchUpdateStates",
		CanonicalID: "androidpublisher.monetization.onetimeproducts.purchaseOptions.offers.activate",
		Reason:      "batch state verb; `iap apply` drives the unary `activate`/`deactivate`/`cancel` verbs, one offer per call (ADR-0041)",
	},
	{
		MethodID:    "androidpublisher.monetization.subscriptions.archive",
		CanonicalID: "androidpublisher.monetization.subscriptions.patch",
		Reason:      "deprecated in Discovery (\"subscription archiving is not supported\"); the archived state is output-only, lifecycle rides `patch`/`delete` (ADR-0041)",
	},
	{
		MethodID:    "androidpublisher.monetization.subscriptions.basePlans.batchMigratePrices",
		CanonicalID: "androidpublisher.monetization.subscriptions.basePlans.migratePrices",
		Reason:      "multi-plan form; `subscriptions prices migrate` gates one base plan per call (ADR-0041)",
	},
	{
		MethodID:    "androidpublisher.monetization.subscriptions.basePlans.batchUpdateStates",
		CanonicalID: "androidpublisher.monetization.subscriptions.basePlans.activate",
		Reason:      "batch state verb; `subscriptions apply` drives the unary `activate`/`deactivate` verbs, one base plan per call (ADR-0041)",
	},
	{
		MethodID:    "androidpublisher.monetization.subscriptions.basePlans.offers.batchGet",
		CanonicalID: "androidpublisher.monetization.subscriptions.basePlans.offers.list",
		Reason:      "multi-id read; `subscriptions pull` reads every offer of a base plan through `list`",
	},
	{
		MethodID:    "androidpublisher.monetization.subscriptions.basePlans.offers.batchUpdate",
		CanonicalID: "androidpublisher.monetization.subscriptions.basePlans.offers.patch",
		Reason:      "multi-id form; `subscriptions apply` upserts one offer per call through `create`/`patch`",
	},
	{
		MethodID:    "androidpublisher.monetization.subscriptions.basePlans.offers.batchUpdateStates",
		CanonicalID: "androidpublisher.monetization.subscriptions.basePlans.offers.activate",
		Reason:      "batch state verb; `subscriptions apply` drives the unary `activate`/`deactivate` verbs, one offer per call (ADR-0041)",
	},
	{
		MethodID:    "androidpublisher.monetization.subscriptions.basePlans.offers.get",
		CanonicalID: "androidpublisher.monetization.subscriptions.basePlans.offers.list",
		Reason:      "single-id read; `subscriptions pull` reads every offer of a base plan through `list`",
	},
	{
		MethodID:    "androidpublisher.monetization.subscriptions.batchGet",
		CanonicalID: "androidpublisher.monetization.subscriptions.list",
		Reason:      "multi-id read; `subscriptions pull` reads the whole catalog through `list`",
	},
	{
		MethodID:    "androidpublisher.monetization.subscriptions.batchUpdate",
		CanonicalID: "androidpublisher.monetization.subscriptions.patch",
		Reason:      "multi-id form; `subscriptions apply` upserts one subscription per call so each refusal maps to one product",
	},
	{
		MethodID:    "androidpublisher.monetization.subscriptions.get",
		CanonicalID: "androidpublisher.monetization.subscriptions.list",
		Reason:      "single-id read; `subscriptions pull` reads the whole catalog through `list`",
	},
}

// parkings is ordered by method id too. Every entry points at an open
// `type:parking` issue: the decision lives there, this list only makes the
// coverage row point at it.
var parkings = []Parked{
	{
		MethodID: "androidpublisher.edits.apks.addexternallyhosted",
		Issue:    546,
		Reason:   "externally hosted APKs are a Managed Play only flow",
	},
	{
		MethodID: "androidpublisher.externaltransactions.createexternaltransaction",
		Issue:    295,
		Reason:   "alternative billing / DMA reporting: an external-transactions pipeline, not a catalog write",
	},
	{
		MethodID: "androidpublisher.externaltransactions.getexternaltransaction",
		Issue:    295,
		Reason:   "alternative billing / DMA reporting: an external-transactions pipeline, not a catalog read",
	},
	{
		MethodID: "androidpublisher.externaltransactions.refundexternaltransaction",
		Issue:    295,
		Reason:   "alternative billing / DMA reporting: money-moving on an external transaction",
	},
	{
		MethodID: "androidpublisher.orders.reviewrefund",
		Issue:    352,
		Reason:   "chargeback refund review; slice-vs-park still to be grilled",
	},
	{
		MethodID: "androidpublisher.purchases.voidedpurchases.list",
		Issue:    346,
		Reason:   "voided-purchases feed for refund/chargeback reconciliation",
	},
	{
		MethodID: "androidpublisher.systemapks.variants.create",
		Issue:    296,
		Reason:   "system APK variants are preload/OEM system-image material",
	},
	{
		MethodID: "androidpublisher.systemapks.variants.download",
		Issue:    296,
		Reason:   "system APK variants are preload/OEM system-image material",
	},
	{
		MethodID: "androidpublisher.systemapks.variants.get",
		Issue:    296,
		Reason:   "system APK variants are preload/OEM system-image material",
	},
	{
		MethodID: "androidpublisher.systemapks.variants.list",
		Issue:    296,
		Reason:   "system APK variants are preload/OEM system-image material",
	},
	{
		MethodID: "playdeveloperreporting.apps.fetchReleaseFilterOptions",
		Issue:    348,
		Reason:   "release-keyed vitals filters, to fold into `vitals` when release filtering lands",
	},
}
