package apiregistry

type Exclusion struct {
	MethodID string
	Reason   string
}

func Exclusions() []Exclusion {
	out := make([]Exclusion, len(exclusions))
	copy(out, exclusions)
	return out
}

const userAuthOnly = "user auth: callable only by a tester player or a Play Console user account (user-delegated OAuth scope https://www.googleapis.com/auth/games), never by a service account"

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
		MethodID: "androidpublisher.purchases.subscriptionsv2.cancelplantransition",
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
	{
		MethodID: "androidpublisher.purchases.subscriptionsv2.scheduleplantransition",
		Reason:   "runtime: acts on a subscription purchase token (v2 shape)",
	},
	{MethodID: "gamesManagement.achievements.reset", Reason: userAuthOnly},
	{MethodID: "gamesManagement.achievements.resetAll", Reason: userAuthOnly},
	{MethodID: "gamesManagement.achievements.resetAllForAllPlayers", Reason: userAuthOnly},
	{MethodID: "gamesManagement.achievements.resetForAllPlayers", Reason: userAuthOnly},
	{MethodID: "gamesManagement.achievements.resetMultipleForAllPlayers", Reason: userAuthOnly},
	{MethodID: "gamesManagement.applications.listHidden", Reason: userAuthOnly},
	{MethodID: "gamesManagement.events.reset", Reason: userAuthOnly},
	{MethodID: "gamesManagement.events.resetAll", Reason: userAuthOnly},
	{MethodID: "gamesManagement.events.resetAllForAllPlayers", Reason: userAuthOnly},
	{MethodID: "gamesManagement.events.resetForAllPlayers", Reason: userAuthOnly},
	{MethodID: "gamesManagement.events.resetMultipleForAllPlayers", Reason: userAuthOnly},
	{MethodID: "gamesManagement.players.hide", Reason: userAuthOnly},
	{MethodID: "gamesManagement.players.unhide", Reason: userAuthOnly},
	{MethodID: "gamesManagement.scores.reset", Reason: userAuthOnly},
	{MethodID: "gamesManagement.scores.resetAll", Reason: userAuthOnly},
	{MethodID: "gamesManagement.scores.resetAllForAllPlayers", Reason: userAuthOnly},
	{MethodID: "gamesManagement.scores.resetForAllPlayers", Reason: userAuthOnly},
	{MethodID: "gamesManagement.scores.resetMultipleForAllPlayers", Reason: userAuthOnly},
}
