// offers.go — the level-3 surface of the Monetization catalog (slice #369):
// subscription offers (monetization.subscriptions.basePlans.offers) and the
// lifecycle state ops of base plans and offers. Offers are a real sub-resource
// with their own CRUD — unlike base plans, whose config rides the parent
// subscription patch — so the catalog embeds them in the files while apply
// reconciles them through these endpoints (ADR-0041 §5).
package subscriptions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

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
// (productId='-', basePlanId='-'), following nextPageToken to completion —
// one paginated call instead of one per base plan, and no silent truncation
// (a missing page would read as offer deletes in a Reconciliation plan).
func ListAllOffers(ctx context.Context, hc *http.Client, pkg string) ([]OfferItem, error) {
	var (
		items []OfferItem
		token string
	)
	// seen guards against a server that repeats a pageToken forever.
	seen := map[string]struct{}{}
	for {
		q := url.Values{}
		q.Set("pageSize", strconv.Itoa(listPageSize))
		if token != "" {
			q.Set("pageToken", token)
		}
		u := base(pkg) + "/-/basePlans/-/offers?" + q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, &api.Error{Operation: opOffersList, Package: pkg, Message: err.Error(), Cause: err}
		}
		raw, err := do(hc, opOffersList, pkg, req)
		if err != nil {
			return nil, err
		}
		var pg offersPage
		if err := json.Unmarshal(raw, &pg); err != nil {
			return nil, &api.Error{Operation: opOffersList, Package: pkg, Message: "decode response: " + err.Error(), Cause: err}
		}
		for _, rawOffer := range pg.SubscriptionOffers {
			var o struct {
				ProductID  string `json:"productId"`
				BasePlanID string `json:"basePlanId"`
				OfferID    string `json:"offerId"`
			}
			if err := json.Unmarshal(rawOffer, &o); err != nil {
				return nil, &api.Error{Operation: opOffersList, Package: pkg, Message: "decode offer: " + err.Error(), Cause: err}
			}
			if o.ProductID == "" || o.BasePlanID == "" || o.OfferID == "" {
				return nil, &api.Error{Operation: opOffersList, Package: pkg, Message: "response contains an offer without its full identity (productId/basePlanId/offerId) — refusing a catalog entry that cannot be addressed"}
			}
			items = append(items, OfferItem{ProductID: o.ProductID, BasePlanID: o.BasePlanID, OfferID: o.OfferID, Raw: rawOffer})
		}
		if pg.NextPageToken == "" {
			return items, nil
		}
		if _, dup := seen[pg.NextPageToken]; dup {
			return nil, &api.Error{
				Operation: opOffersList,
				Package:   pkg,
				Message:   "pagination token loop detected in offers.list (server repeated a nextPageToken)",
			}
		}
		seen[pg.NextPageToken] = struct{}{}
		token = pg.NextPageToken
	}
}

// CreateOffer creates an offer under its base plan from the catalog fragment,
// sent verbatim. offerId and regionsVersion.version ride as query parameters.
func CreateOffer(ctx context.Context, hc *http.Client, pkg, productID, basePlanID, offerID, regionsVersion string, body json.RawMessage) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("offerId", offerID)
	q.Set("regionsVersion.version", regionsVersion)
	u := offersBase(pkg, productID, basePlanID) + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(string(body)))
	if err != nil {
		return nil, &api.Error{Operation: opOffersCreate, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	return do(hc, opOffersCreate, pkg, req)
}

// PatchOffer updates an offer from the catalog fragment, sent verbatim, with
// the updateMask scoped to exactly the changed managed fields (ADR-0041 §5).
func PatchOffer(ctx context.Context, hc *http.Client, pkg, productID, basePlanID, offerID, regionsVersion string, updateMask []string, body json.RawMessage) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("updateMask", strings.Join(updateMask, ","))
	q.Set("regionsVersion.version", regionsVersion)
	u := offersBase(pkg, productID, basePlanID) + "/" + url.PathEscape(offerID) + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, u, strings.NewReader(string(body)))
	if err != nil {
		return nil, &api.Error{Operation: opOffersPatch, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	return do(hc, opOffersPatch, pkg, req)
}

// DeleteOffer deletes an offer. Reaching here means the plan carried a delete
// and the operator passed --confirm; the API refuses to delete an active offer,
// so the gate covers intent while the server covers damage (ADR-0041 §3).
func DeleteOffer(ctx context.Context, hc *http.Client, pkg, productID, basePlanID, offerID string) error {
	u := offersBase(pkg, productID, basePlanID) + "/" + url.PathEscape(offerID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return &api.Error{Operation: opOffersDelete, Package: pkg, Message: err.Error(), Cause: err}
	}
	_, err = do(hc, opOffersDelete, pkg, req)
	return err
}

// SetBasePlanState activates (true) or deactivates (false) a base plan — the
// reconciliation arm of the declarative state: field in the catalog files. The
// body is an empty object; the identity rides the path.
func SetBasePlanState(ctx context.Context, hc *http.Client, pkg, productID, basePlanID string, activate bool) (json.RawMessage, error) {
	verb, op := ":deactivate", opBasePlanDeactivate
	if activate {
		verb, op = ":activate", opBasePlanActivate
	}
	u := base(pkg) + "/" + url.PathEscape(productID) + "/basePlans/" + url.PathEscape(basePlanID) + verb
	return postEmpty(ctx, hc, op, pkg, u)
}

// SetOfferState activates (true) or deactivates (false) an offer.
func SetOfferState(ctx context.Context, hc *http.Client, pkg, productID, basePlanID, offerID string, activate bool) (json.RawMessage, error) {
	verb, op := ":deactivate", opOffersDeactivate
	if activate {
		verb, op = ":activate", opOffersActivate
	}
	u := offersBase(pkg, productID, basePlanID) + "/" + url.PathEscape(offerID) + verb
	return postEmpty(ctx, hc, op, pkg, u)
}

// offersBase is the offers collection URL under one base plan.
func offersBase(pkg, productID, basePlanID string) string {
	return base(pkg) + "/" + url.PathEscape(productID) + "/basePlans/" + url.PathEscape(basePlanID) + "/offers"
}

// postEmpty POSTs an empty JSON object to a custom-verb URL.
func postEmpty(ctx context.Context, hc *http.Client, op, pkg, u string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader("{}"))
	if err != nil {
		return nil, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	return do(hc, op, pkg, req)
}
