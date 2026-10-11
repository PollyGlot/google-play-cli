package apiregistry

// Exclusion is one API method gplay will never wrap, with the one-line reason
// why. "By nature" is a narrow claim under ADR-0026: every Play *admin* method
// is in scope, so the only legitimate exclusions are *runtime* APIs, called
// server-to-server by an app backend reacting to a user event: a purchase
// token minted on a device, a token delivered by a Real-time Developer
// Notification, or a transaction the backend must report as it happens. A
// method that is merely unshipped, redundant or parked is NOT excluded: it is
// uncovered, and docs/COVERAGE.md says so.
type Exclusion struct {
	MethodID string
	Reason   string
}

// Exclusions returns the list. Like Entries it is a function so no caller can
// mutate the shared backing array.
func Exclusions() []Exclusion {
	out := make([]Exclusion, len(exclusions))
	copy(out, exclusions)
	return out
}

// exclusions is ordered by method id, like entries, so a diff reads like a diff
// on paths.txt.
//
// The Play Integrity API is excluded by nature too, but it is its own API with
// no snapshot under docs/discovery/, so it has no method id to list here.
var exclusions = []Exclusion{
	{
		MethodID: "androidpublisher.externaltransactions.createexternaltransaction",
		Reason:   "runtime: the backend reports each alternative-billing transaction as it happens, not an admin operation",
	},
	{
		MethodID: "androidpublisher.externaltransactions.getexternaltransaction",
		Reason:   "runtime: reads back a transaction the backend reported, keyed by its own transaction id",
	},
	{
		MethodID: "androidpublisher.externaltransactions.refundexternaltransaction",
		Reason:   "runtime: the backend reports a refund of an alternative-billing transaction as it happens",
	},
	{
		MethodID: "androidpublisher.orders.reviewrefund",
		Reason:   "runtime: answers a refund review with a token delivered by a Real-time Developer Notification",
	},
	{
		MethodID: "androidpublisher.purchases.products.acknowledge",
		Reason:   "runtime: acknowledges a purchase token minted on-device, not an admin operation",
	},
	{
		MethodID: "androidpublisher.purchases.products.consume",
		Reason:   "runtime: consumes a purchase token minted on-device, not an admin operation",
	},
	{
		MethodID: "androidpublisher.purchases.products.get",
		Reason:   "runtime: server-side purchase-token verification",
	},
	{
		MethodID: "androidpublisher.purchases.productsv2.getproductpurchasev2",
		Reason:   "runtime: server-side purchase-token verification (v2 shape)",
	},
	{
		MethodID: "androidpublisher.purchases.subscriptions.acknowledge",
		Reason:   "runtime: acknowledges a subscription purchase token minted on-device",
	},
	{
		MethodID: "androidpublisher.purchases.subscriptions.cancel",
		Reason:   "runtime: acts on a subscription purchase token, not on a catalog resource",
	},
	{
		MethodID: "androidpublisher.purchases.subscriptions.defer",
		Reason:   "runtime: acts on a subscription purchase token, not on a catalog resource",
	},
	{
		MethodID: "androidpublisher.purchases.subscriptionsv2.cancel",
		Reason:   "runtime: acts on a subscription purchase token (v2 shape)",
	},
	{
		MethodID: "androidpublisher.purchases.subscriptionsv2.defer",
		Reason:   "runtime: acts on a subscription purchase token (v2 shape)",
	},
	{
		MethodID: "androidpublisher.purchases.subscriptionsv2.get",
		Reason:   "runtime: server-side subscription-token verification (v2 shape)",
	},
	{
		MethodID: "androidpublisher.purchases.subscriptionsv2.revoke",
		Reason:   "runtime: acts on a subscription purchase token (v2 shape)",
	},
}
