// Package iap calls the one-time-product surfaces of the Android Publisher
// API: the IAP branch of the Monetization catalog (PRD #51, ADR-0041 §8):
// the v2 model `monetization.onetimeproducts` (nested purchaseOptions →
// offers) for all reads AND writes, plus a read-only walk of the legacy
// `inappproducts` so unmigrated products are never invisible (`pull` unions
// the two; legacy is never written in place). Edit-free, package axis. Raw
// HTTP (ADR-0007), never the google-go-sdk.
//
// The v2 write model has no create: onetimeproducts.patch carries
// allowMissing, so the upsert IS the create. Offer writes ride the per-
// purchase-option batch endpoints (no single create/patch exists). Lifecycle
// states (slice #541) never ride a patch (the field is output-only): purchase
// options move through their per-product batch verb (their only state write
// path), offers through the unary :activate / :deactivate / :cancel verbs.
package iap

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
	opList              = "monetization.onetimeproducts.list"
	opPatch             = "monetization.onetimeproducts.patch"
	opDelete            = "monetization.onetimeproducts.delete"
	opOffersList        = "monetization.onetimeproducts.purchaseOptions.offers.list"
	opOffersBatchUpdate = "monetization.onetimeproducts.purchaseOptions.offers.batchUpdate"
	opOffersBatchDelete = "monetization.onetimeproducts.purchaseOptions.offers.batchDelete"
	opOptionStates      = "monetization.onetimeproducts.purchaseOptions.batchUpdateStates"
	opOffersActivate    = "monetization.onetimeproducts.purchaseOptions.offers.activate"
	opOffersDeactivate  = "monetization.onetimeproducts.purchaseOptions.offers.deactivate"
	opOffersCancel      = "monetization.onetimeproducts.purchaseOptions.offers.cancel"
	opLegacyList        = "inappproducts.list"
)

// m* are the registry entries this package calls. Resolving them at init turns
// an unregistered or vanished method into a startup panic CI catches (the
// registry tests resolve every entry), never a runtime surprise for a user;
// verb and URL template then come from the Discovery snapshot instead of
// literals maintained here (#513). Case in point: the snapshot paths the v2
// patch under lowercase `onetimeproducts` while the reads use
// `oneTimeProducts`, and taking both from the snapshot is what keeps that
// asymmetry right.
var (
	mList              = apiregistry.MustResolve("androidpublisher.monetization.onetimeproducts.list")
	mPatch             = apiregistry.MustResolve("androidpublisher.monetization.onetimeproducts.patch")
	mDelete            = apiregistry.MustResolve("androidpublisher.monetization.onetimeproducts.delete")
	mOffersList        = apiregistry.MustResolve("androidpublisher.monetization.onetimeproducts.purchaseOptions.offers.list")
	mOffersBatchUpdate = apiregistry.MustResolve("androidpublisher.monetization.onetimeproducts.purchaseOptions.offers.batchUpdate")
	mOffersBatchDelete = apiregistry.MustResolve("androidpublisher.monetization.onetimeproducts.purchaseOptions.offers.batchDelete")
	mOptionStates      = apiregistry.MustResolve("androidpublisher.monetization.onetimeproducts.purchaseOptions.batchUpdateStates")
	mOffersActivate    = apiregistry.MustResolve("androidpublisher.monetization.onetimeproducts.purchaseOptions.offers.activate")
	mOffersDeactivate  = apiregistry.MustResolve("androidpublisher.monetization.onetimeproducts.purchaseOptions.offers.deactivate")
	mOffersCancel      = apiregistry.MustResolve("androidpublisher.monetization.onetimeproducts.purchaseOptions.offers.cancel")
	mLegacyList        = apiregistry.MustResolve("androidpublisher.inappproducts.list")
)

// Offer lifecycle targets a catalog file may declare in an offer's state:
// field (OneTimeProductOffer.state minus DRAFT/UNSPECIFIED, which no verb can
// reach). Each maps to one unary verb in SetOfferState.
const (
	OfferStateActive    = "ACTIVE"
	OfferStateInactive  = "INACTIVE"
	OfferStateCancelled = "CANCELLED"
)

const listPageSize = 100

// Item is one live v2 one-time product: parsed ID + verbatim resource.
type Item struct {
	ProductID string
	Raw       json.RawMessage
}

// OfferItem is one live v2 offer: full composite identity + verbatim resource.
type OfferItem struct {
	ProductID        string
	PurchaseOptionID string
	OfferID          string
	Raw              json.RawMessage
}

// LegacyItem is one live legacy in-app product: its SKU + verbatim resource
// (recognizable by its `sku` field: the v2 resource carries `productId`).
type LegacyItem struct {
	SKU string
	Raw json.RawMessage
}

// ListOneTimeProducts reads the complete live v2 catalog, following
// nextPageToken to completion (no silent truncation: a missing page would
// read as deletes in a Reconciliation plan).
func ListOneTimeProducts(ctx context.Context, hc *http.Client, pkg string) ([]Item, error) {
	var (
		items []Item
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
		var pg struct {
			OneTimeProducts []json.RawMessage `json:"oneTimeProducts"`
			NextPageToken   string            `json:"nextPageToken"`
		}
		if _, err := api.DoJSON(ctx, hc, api.Call{
			Method: mList, Op: opList, Target: pkg,
			Params: map[string]string{"packageName": pkg},
			Query:  q,
		}, &pg); err != nil {
			return nil, err
		}
		for _, rawP := range pg.OneTimeProducts {
			var p struct {
				ProductID string `json:"productId"`
			}
			if err := json.Unmarshal(rawP, &p); err != nil {
				return nil, &api.Error{Operation: opList, Package: pkg, Message: "decode one-time product: " + err.Error(), Cause: err}
			}
			if p.ProductID == "" {
				return nil, &api.Error{Operation: opList, Package: pkg, Message: "response contains a one-time product without a productId: refusing a catalog entry that cannot be addressed"}
			}
			items = append(items, Item{ProductID: p.ProductID, Raw: rawP})
		}
		if pg.NextPageToken == "" {
			return items, nil
		}
		if _, dup := seen[pg.NextPageToken]; dup {
			return nil, &api.Error{Operation: opList, Package: pkg, Message: "pagination token loop detected in onetimeproducts.list (server repeated a nextPageToken)"}
		}
		seen[pg.NextPageToken] = struct{}{}
		token = pg.NextPageToken
	}
}

// ListAllOffers reads every v2 offer of the app in one wildcard walk
// (productId='-', purchaseOptionId='-'), following nextPageToken to
// completion.
func ListAllOffers(ctx context.Context, hc *http.Client, pkg string) ([]OfferItem, error) {
	var (
		items []OfferItem
		token string
	)
	seen := map[string]struct{}{}
	for {
		q := url.Values{}
		q.Set("pageSize", strconv.Itoa(listPageSize))
		if token != "" {
			q.Set("pageToken", token)
		}
		// The wildcard walk rides the same template as a scoped list: `-` is a
		// legal path segment, so escaping leaves it untouched.
		var pg struct {
			OneTimeProductOffers []json.RawMessage `json:"oneTimeProductOffers"`
			NextPageToken        string            `json:"nextPageToken"`
		}
		if _, err := api.DoJSON(ctx, hc, api.Call{
			Method: mOffersList, Op: opOffersList, Target: pkg,
			Params: map[string]string{"packageName": pkg, "productId": "-", "purchaseOptionId": "-"},
			Query:  q,
		}, &pg); err != nil {
			return nil, err
		}
		for _, rawO := range pg.OneTimeProductOffers {
			var o struct {
				ProductID        string `json:"productId"`
				PurchaseOptionID string `json:"purchaseOptionId"`
				OfferID          string `json:"offerId"`
			}
			if err := json.Unmarshal(rawO, &o); err != nil {
				return nil, &api.Error{Operation: opOffersList, Package: pkg, Message: "decode offer: " + err.Error(), Cause: err}
			}
			if o.ProductID == "" || o.PurchaseOptionID == "" || o.OfferID == "" {
				return nil, &api.Error{Operation: opOffersList, Package: pkg, Message: "response contains an offer without its full identity (productId/purchaseOptionId/offerId): refusing a catalog entry that cannot be addressed"}
			}
			items = append(items, OfferItem{ProductID: o.ProductID, PurchaseOptionID: o.PurchaseOptionID, OfferID: o.OfferID, Raw: rawO})
		}
		if pg.NextPageToken == "" {
			return items, nil
		}
		if _, dup := seen[pg.NextPageToken]; dup {
			return nil, &api.Error{Operation: opOffersList, Package: pkg, Message: "pagination token loop detected in offers.list (server repeated a nextPageToken)"}
		}
		seen[pg.NextPageToken] = struct{}{}
		token = pg.NextPageToken
	}
}

// PatchOneTimeProduct upserts (allowMissing=true, the v2 create: the API has
// no insert) or edits (updateMask scoped to the changed managed fields) one
// product from the catalog-file resource, sent verbatim. The Discovery
// snapshot paths this method under lowercase `onetimeproducts/`, unlike the
// reads: followed verbatim.
func PatchOneTimeProduct(ctx context.Context, hc *http.Client, pkg, productID, regionsVersion string, updateMask []string, allowMissing bool, body json.RawMessage) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("regionsVersion.version", regionsVersion)
	if allowMissing {
		q.Set("allowMissing", "true")
	}
	if len(updateMask) > 0 {
		q.Set("updateMask", strings.Join(updateMask, ","))
	}
	return api.Do(ctx, hc, api.Call{
		Method: mPatch, Op: opPatch, Target: pkg,
		Params: map[string]string{"packageName": pkg, "productId": productID},
		Query:  q,
		Body:   body,
	})
}

// DeleteOneTimeProduct deletes one v2 product. Reaching here means the plan
// carried a delete and the operator passed --confirm (ADR-0041 §3).
func DeleteOneTimeProduct(ctx context.Context, hc *http.Client, pkg, productID string) error {
	_, err := api.Do(ctx, hc, api.Call{
		Method: mDelete, Op: opDelete, Target: pkg,
		Params: map[string]string{"packageName": pkg, "productId": productID},
	})
	return err
}

// OfferUpdate is one entry of a BatchUpdateOffers call: the offer resource
// verbatim, upserted when AllowMissing (the offer create: no single insert
// exists) or masked to the changed fields.
type OfferUpdate struct {
	Offer        json.RawMessage
	AllowMissing bool
	UpdateMask   []string
}

// BatchUpdateOffers creates/edits the given offers of one purchase option in
// one offers:batchUpdate call: the only write path the API exposes for
// offers.
func BatchUpdateOffers(ctx context.Context, hc *http.Client, pkg, productID, purchaseOptionID string, updates []OfferUpdate, regionsVersion string) error {
	type updateReq struct {
		OneTimeProductOffer json.RawMessage `json:"oneTimeProductOffer"`
		AllowMissing        bool            `json:"allowMissing,omitempty"`
		UpdateMask          string          `json:"updateMask,omitempty"`
		RegionsVersion      struct {
			Version string `json:"version"`
		} `json:"regionsVersion"`
	}
	reqs := make([]updateReq, 0, len(updates))
	for _, upd := range updates {
		r := updateReq{OneTimeProductOffer: upd.Offer, AllowMissing: upd.AllowMissing, UpdateMask: strings.Join(upd.UpdateMask, ",")}
		r.RegionsVersion.Version = regionsVersion
		reqs = append(reqs, r)
	}
	_, err := api.Do(ctx, hc, api.Call{
		Method: mOffersBatchUpdate, Op: opOffersBatchUpdate, Target: pkg,
		Params: map[string]string{"packageName": pkg, "productId": productID, "purchaseOptionId": purchaseOptionID},
		Body: struct {
			Requests []updateReq `json:"requests"`
		}{Requests: reqs},
	})
	return err
}

// BatchDeleteOffers deletes the given offers of one purchase option in one
// offers:batchDelete call, naming each offer's full identity.
func BatchDeleteOffers(ctx context.Context, hc *http.Client, pkg, productID, purchaseOptionID string, offerIDs []string) error {
	type deleteReq struct {
		PackageName      string `json:"packageName"`
		ProductID        string `json:"productId"`
		PurchaseOptionID string `json:"purchaseOptionId"`
		OfferID          string `json:"offerId"`
	}
	reqs := make([]deleteReq, 0, len(offerIDs))
	for _, id := range offerIDs {
		reqs = append(reqs, deleteReq{PackageName: pkg, ProductID: productID, PurchaseOptionID: purchaseOptionID, OfferID: id})
	}
	_, err := api.Do(ctx, hc, api.Call{
		Method: mOffersBatchDelete, Op: opOffersBatchDelete, Target: pkg,
		Params: map[string]string{"packageName": pkg, "productId": productID, "purchaseOptionId": purchaseOptionID},
		Body: struct {
			Requests []deleteReq `json:"requests"`
		}{Requests: reqs},
	})
	return err
}

// PurchaseOptionStateUpdate is one entry of a BatchUpdatePurchaseOptionStates
// call: the purchase option to move, and the direction (activate or
// deactivate: the two verbs that sub-resource exposes).
type PurchaseOptionStateUpdate struct {
	PurchaseOptionID string
	Activate         bool
}

// BatchUpdatePurchaseOptionStates moves the given purchase options of one
// product in one purchaseOptions:batchUpdateStates call: the only state write
// path the API exposes for purchase options (no unary verb exists, unlike
// offers). The body is the oneof shape of UpdatePurchaseOptionStateRequest:
// one of activatePurchaseOptionRequest / deactivatePurchaseOptionRequest per
// entry, each echoing the identity the schema requires.
func BatchUpdatePurchaseOptionStates(ctx context.Context, hc *http.Client, pkg, productID string, updates []PurchaseOptionStateUpdate) (json.RawMessage, error) {
	type identity struct {
		PackageName      string `json:"packageName"`
		ProductID        string `json:"productId"`
		PurchaseOptionID string `json:"purchaseOptionId"`
	}
	type stateReq struct {
		Activate   *identity `json:"activatePurchaseOptionRequest,omitempty"`
		Deactivate *identity `json:"deactivatePurchaseOptionRequest,omitempty"`
	}
	reqs := make([]stateReq, 0, len(updates))
	for _, upd := range updates {
		id := &identity{PackageName: pkg, ProductID: productID, PurchaseOptionID: upd.PurchaseOptionID}
		if upd.Activate {
			reqs = append(reqs, stateReq{Activate: id})
		} else {
			reqs = append(reqs, stateReq{Deactivate: id})
		}
	}
	return api.Do(ctx, hc, api.Call{
		Method: mOptionStates, Op: opOptionStates, Target: pkg,
		Params: map[string]string{"packageName": pkg, "productId": productID},
		Body: struct {
			Requests []stateReq `json:"requests"`
		}{Requests: reqs},
	})
}

// SetOfferState moves one offer to the declared target through its unary
// verb: ACTIVE → :activate, INACTIVE → :deactivate (discounted offers),
// CANCELLED → :cancel (pre-orders; irreversible, so the command gates it
// behind --confirm before reaching here). Any other target is a programming
// error surfaced as an *api.Error rather than a silent no-op. The custom verb
// is part of the snapshot's flatPath, so it rides the template. The request
// message echoes the identity the schema declares (the subscription siblings
// accept an empty body, the one-time ones list the fields: sent to be safe).
func SetOfferState(ctx context.Context, hc *http.Client, pkg, productID, purchaseOptionID, offerID, target string) (json.RawMessage, error) {
	var (
		m  apiregistry.Method
		op string
	)
	switch target {
	case OfferStateActive:
		m, op = mOffersActivate, opOffersActivate
	case OfferStateInactive:
		m, op = mOffersDeactivate, opOffersDeactivate
	case OfferStateCancelled:
		m, op = mOffersCancel, opOffersCancel
	default:
		return nil, &api.Error{Operation: opOffersActivate, Package: pkg, Message: "unsupported offer state target " + strconv.Quote(target)}
	}
	identity := map[string]string{"packageName": pkg, "productId": productID, "purchaseOptionId": purchaseOptionID, "offerId": offerID}
	return api.Do(ctx, hc, api.Call{
		Method: m, Op: op, Target: pkg,
		Params: identity,
		Body:   identity,
	})
}

// ListInAppProducts reads the complete legacy inappproducts catalog, following
// the legacy tokenPagination envelope to completion. Read-only: gplay never
// writes the legacy surface in place (ADR-0041 §8).
func ListInAppProducts(ctx context.Context, hc *http.Client, pkg string) ([]LegacyItem, error) {
	var (
		items []LegacyItem
		token string
	)
	seen := map[string]struct{}{}
	for {
		var q url.Values
		if token != "" {
			q = url.Values{"token": {token}}
		}
		var pg struct {
			InAppProduct    []json.RawMessage `json:"inappproduct"`
			TokenPagination struct {
				NextPageToken string `json:"nextPageToken"`
			} `json:"tokenPagination"`
		}
		if _, err := api.DoJSON(ctx, hc, api.Call{
			Method: mLegacyList, Op: opLegacyList, Target: pkg,
			Params: map[string]string{"packageName": pkg},
			Query:  q,
		}, &pg); err != nil {
			return nil, err
		}
		for _, rawP := range pg.InAppProduct {
			var p struct {
				SKU string `json:"sku"`
			}
			if err := json.Unmarshal(rawP, &p); err != nil {
				return nil, &api.Error{Operation: opLegacyList, Package: pkg, Message: "decode in-app product: " + err.Error(), Cause: err}
			}
			if p.SKU == "" {
				return nil, &api.Error{Operation: opLegacyList, Package: pkg, Message: "response contains an in-app product without a sku: refusing a catalog entry that cannot be addressed"}
			}
			items = append(items, LegacyItem{SKU: p.SKU, Raw: rawP})
		}
		next := pg.TokenPagination.NextPageToken
		if next == "" {
			return items, nil
		}
		if _, dup := seen[next]; dup {
			return nil, &api.Error{Operation: opLegacyList, Package: pkg, Message: "pagination token loop detected in inappproducts.list (server repeated a nextPageToken)"}
		}
		seen[next] = struct{}{}
		token = next
	}
}
