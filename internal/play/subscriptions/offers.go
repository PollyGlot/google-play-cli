// offers.go: the level-3 surface of the Monetization catalog (slice #369):
// subscription offers (monetization.subscriptions.basePlans.offers) and the
// lifecycle state ops of base plans and offers. Offers are a real sub-resource
// with their own CRUD: unlike base plans, whose config rides the parent
// subscription patch, so the catalog embeds them in the files while apply
// reconciles them through these endpoints (ADR-0041 §5). The one base-plan
// endpoint of its own besides the state ops is basePlans.delete (slice #542).
package subscriptions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// op* tag *api.Error with the REST reference method id.
const (
	opOffersList         = "monetization.subscriptions.basePlans.offers.list"
	opOffersCreate       = "monetization.subscriptions.basePlans.offers.create"
	opOffersPatch        = "monetization.subscriptions.basePlans.offers.patch"
	opOffersDelete       = "monetization.subscriptions.basePlans.offers.delete"
	opOffersActivate     = "monetization.subscriptions.basePlans.offers.activate"
	opOffersDeactivate   = "monetization.subscriptions.basePlans.offers.deactivate"
	opBasePlanActivate   = "monetization.subscriptions.basePlans.activate"
	opBasePlanDeactivate = "monetization.subscriptions.basePlans.deactivate"
	opBasePlanDelete     = "monetization.subscriptions.basePlans.delete"
)

// m* are the registry entries this package calls. Resolving them at init turns
// an unregistered or vanished method into a startup panic CI catches (the
// registry tests resolve every entry), never a runtime surprise for a user;
// verb and URL template then come from the Discovery snapshot instead of
// literals maintained here (#513). The custom verbs (`:activate`,
// `:deactivate`, `:migratePrices`) are part of the snapshot's flatPath, so they
// ride the template too rather than being concatenated here.
var (
	mOffersList         = apiregistry.MustResolve("androidpublisher.monetization.subscriptions.basePlans.offers.list")
	mOffersCreate       = apiregistry.MustResolve("androidpublisher.monetization.subscriptions.basePlans.offers.create")
	mOffersPatch        = apiregistry.MustResolve("androidpublisher.monetization.subscriptions.basePlans.offers.patch")
	mOffersDelete       = apiregistry.MustResolve("androidpublisher.monetization.subscriptions.basePlans.offers.delete")
	mOffersActivate     = apiregistry.MustResolve("androidpublisher.monetization.subscriptions.basePlans.offers.activate")
	mOffersDeactivate   = apiregistry.MustResolve("androidpublisher.monetization.subscriptions.basePlans.offers.deactivate")
	mBasePlanActivate   = apiregistry.MustResolve("androidpublisher.monetization.subscriptions.basePlans.activate")
	mBasePlanDeactivate = apiregistry.MustResolve("androidpublisher.monetization.subscriptions.basePlans.deactivate")
	mBasePlanDelete     = apiregistry.MustResolve("androidpublisher.monetization.subscriptions.basePlans.delete")
	mMigratePrices      = apiregistry.MustResolve("androidpublisher.monetization.subscriptions.basePlans.migratePrices")
)

// OfferItem is one live offer: its full composite identity plus the verbatim
// resource bytes.
type OfferItem struct {
	ProductID  string
	BasePlanID string
	OfferID    string
	Raw        json.RawMessage
}

// offersPage mirrors the ListSubscriptionOffersResponse envelope.
type offersPage struct {
	SubscriptionOffers []json.RawMessage `json:"subscriptionOffers"`
	NextPageToken      string            `json:"nextPageToken"`
}

// ListAllOffers reads every offer of the app in one wildcard walk
// (productId='-', basePlanId='-'), following nextPageToken to completion:
// one paginated call instead of one per base plan, and no silent truncation
// (a missing page would read as offer deletes in a Reconciliation plan).
func ListAllOffers(ctx context.Context, hc *http.Client, pkg string) ([]OfferItem, error) {
	items, _, err := api.Paginate(api.Pager{Op: opOffersList, Target: pkg, What: "offers.list"},
		func(token string, _ int) ([]OfferItem, string, error) {
			q := url.Values{}
			q.Set("pageSize", strconv.Itoa(listPageSize))
			if token != "" {
				q.Set("pageToken", token)
			}
			// The wildcard walk rides the same template as a scoped list: `-` is
			// a legal path segment, so escaping leaves it untouched.
			var pg offersPage
			if _, err := api.DoJSON(ctx, hc, api.Call{
				Method: mOffersList, Op: opOffersList, Target: pkg,
				Params: map[string]string{"packageName": pkg, "productId": "-", "basePlanId": "-"},
				Query:  q,
			}, &pg); err != nil {
				return nil, "", err
			}
			page := make([]OfferItem, 0, len(pg.SubscriptionOffers))
			for _, rawOffer := range pg.SubscriptionOffers {
				var o struct {
					ProductID  string `json:"productId"`
					BasePlanID string `json:"basePlanId"`
					OfferID    string `json:"offerId"`
				}
				if err := json.Unmarshal(rawOffer, &o); err != nil {
					return nil, "", &api.Error{Operation: opOffersList, Package: pkg, Message: "decode offer: " + err.Error(), Cause: err}
				}
				if o.ProductID == "" || o.BasePlanID == "" || o.OfferID == "" {
					return nil, "", &api.Error{Operation: opOffersList, Package: pkg, Message: "response contains an offer without its full identity (productId/basePlanId/offerId): refusing a catalog entry that cannot be addressed"}
				}
				page = append(page, OfferItem{ProductID: o.ProductID, BasePlanID: o.BasePlanID, OfferID: o.OfferID, Raw: rawOffer})
			}
			return page, pg.NextPageToken, nil
		})
	return items, err
}

// CreateOffer creates an offer under its base plan from the catalog fragment,
// sent verbatim. offerId and regionsVersion.version ride as query parameters.
func CreateOffer(ctx context.Context, hc *http.Client, pkg, productID, basePlanID, offerID, regionsVersion string, body json.RawMessage) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("offerId", offerID)
	q.Set("regionsVersion.version", regionsVersion)
	return api.Do(ctx, hc, api.Call{
		Method: mOffersCreate, Op: opOffersCreate, Target: pkg,
		Params: offerScope(pkg, productID, basePlanID),
		Query:  q,
		Body:   body,
	})
}

// PatchOffer updates an offer from the catalog fragment, sent verbatim, with
// the updateMask scoped to exactly the changed managed fields (ADR-0041 §5).
func PatchOffer(ctx context.Context, hc *http.Client, pkg, productID, basePlanID, offerID, regionsVersion string, updateMask []string, body json.RawMessage) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("updateMask", strings.Join(updateMask, ","))
	q.Set("regionsVersion.version", regionsVersion)
	return api.Do(ctx, hc, api.Call{
		Method: mOffersPatch, Op: opOffersPatch, Target: pkg,
		Params: offerScope(pkg, productID, basePlanID, offerID),
		Query:  q,
		Body:   body,
	})
}

// DeleteOffer deletes an offer. Reaching here means the plan carried a delete
// and the operator passed --confirm; the API refuses to delete an active offer,
// so the gate covers intent while the server covers damage (ADR-0041 §3).
func DeleteOffer(ctx context.Context, hc *http.Client, pkg, productID, basePlanID, offerID string) error {
	_, err := api.Do(ctx, hc, api.Call{
		Method: mOffersDelete, Op: opOffersDelete, Target: pkg,
		Params: offerScope(pkg, productID, basePlanID, offerID),
	})
	return err
}

// SetBasePlanState activates (true) or deactivates (false) a base plan: the
// reconciliation arm of the declarative state: field in the catalog files. The
// body is an empty object; the identity rides the path.
func SetBasePlanState(ctx context.Context, hc *http.Client, pkg, productID, basePlanID string, activate bool) (json.RawMessage, error) {
	m, op := mBasePlanDeactivate, opBasePlanDeactivate
	if activate {
		m, op = mBasePlanActivate, opBasePlanActivate
	}
	return postEmpty(ctx, hc, m, op, pkg, offerScope(pkg, productID, basePlanID))
}

// DeleteBasePlan deletes a base plan (slice #542): the shrink arm of the
// base-plan diff, since the parent subscription patch never removes a plan
// its body omits. The API only deletes a DRAFT base plan: a plan that was ever
// published (ACTIVE, or INACTIVE after activation) is refused server-side,
// and the caller surfaces that refusal with the deactivate-first hint rather
// than retrying.
func DeleteBasePlan(ctx context.Context, hc *http.Client, pkg, productID, basePlanID string) error {
	_, err := api.Do(ctx, hc, api.Call{
		Method: mBasePlanDelete, Op: opBasePlanDelete, Target: pkg,
		Params: offerScope(pkg, productID, basePlanID),
	})
	return err
}

// SetOfferState activates (true) or deactivates (false) an offer.
func SetOfferState(ctx context.Context, hc *http.Client, pkg, productID, basePlanID, offerID string, activate bool) (json.RawMessage, error) {
	m, op := mOffersDeactivate, opOffersDeactivate
	if activate {
		m, op = mOffersActivate, opOffersActivate
	}
	return postEmpty(ctx, hc, m, op, pkg, offerScope(pkg, productID, basePlanID, offerID))
}

// opMigratePrices tags *api.Error for the subscriber price migration.
const opMigratePrices = "monetization.subscriptions.basePlans.migratePrices"

// RegionsVersion mirrors the RegionsVersion schema.
type RegionsVersion struct {
	Version string `json:"version"`
}

// RegionalPriceMigration mirrors RegionalPriceMigrationConfig: one region
// whose legacy price cohorts older than the cutoff migrate to the current
// price.
type RegionalPriceMigration struct {
	RegionCode                    string `json:"regionCode"`
	OldestAllowedPriceVersionTime string `json:"oldestAllowedPriceVersionTime"`
	PriceIncreaseType             string `json:"priceIncreaseType,omitempty"`
}

// MigrateBasePlanPricesRequest mirrors the request schema. The path identity
// (packageName/productId/basePlanId) is echoed in the body as the schema
// requires ("must be equal to" the resource fields).
type MigrateBasePlanPricesRequest struct {
	PackageName             string                   `json:"packageName"`
	ProductID               string                   `json:"productId"`
	BasePlanID              string                   `json:"basePlanId"`
	RegionalPriceMigrations []RegionalPriceMigration `json:"regionalPriceMigrations"`
	RegionsVersion          RegionsVersion           `json:"regionsVersion"`
}

// MigrateBasePlanPrices migrates EXISTING subscribers of one base plan to the
// current price (basePlans.migratePrices): the sole imperative escape hatch
// of the Monetization catalog (ADR-0041 §4): apply never reprices a live
// purchaser, this call is how an operator deliberately does. One base plan per
// call: the batch sibling is deliberately not wrapped (no bulk money-moving,
// the ADR-0031 stance). The command gates it behind --confirm before reaching
// here.
func MigrateBasePlanPrices(ctx context.Context, hc *http.Client, pkg, productID, basePlanID string, request MigrateBasePlanPricesRequest) (json.RawMessage, error) {
	return api.Do(ctx, hc, api.Call{
		Method: mMigratePrices, Op: opMigratePrices, Target: pkg,
		Params: offerScope(pkg, productID, basePlanID),
		Body:   request,
	})
}

// offerScope builds the path parameters shared by every base-plan and offer
// template: the offer id is optional because the base-plan verbs stop one level
// higher, and the resolver rejects a key its template does not declare.
func offerScope(pkg, productID, basePlanID string, offerID ...string) map[string]string {
	p := map[string]string{"packageName": pkg, "productId": productID, "basePlanId": basePlanID}
	if len(offerID) == 1 {
		p["offerId"] = offerID[0]
	}
	return p
}

// postEmpty POSTs an empty JSON object to a custom verb.
func postEmpty(ctx context.Context, hc *http.Client, m apiregistry.Method, op, pkg string, params map[string]string) (json.RawMessage, error) {
	return api.Do(ctx, hc, api.Call{Method: m, Op: op, Target: pkg, Params: params, Body: []byte("{}")})
}
