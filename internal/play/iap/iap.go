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
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/latency"
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

// The State* values are the OneTimeProductOffer.state enum; purchase options
// share DRAFT, ACTIVE and INACTIVE. ACTIVE, INACTIVE and CANCELLED are the
// lifecycle targets a catalog file may declare (each maps to one unary verb in
// SetOfferState); no verb reaches DRAFT, the state of a resource that has
// never been activated. Play alone sets StateInactivePublished, on purchase
// options it migrated automatically: off sale, but still exposed through the
// Play Billing Library.
const (
	StateDraft             = "DRAFT"
	StateActive            = "ACTIVE"
	StateInactive          = "INACTIVE"
	StateCancelled         = "CANCELLED"
	StateInactivePublished = "INACTIVE_PUBLISHED"
)

// listPageSize is the page size of the v2 product list and offers walk. It
// is sized against api.MaxAPISuccessBodyRead: Play pretty-prints, and a
// product with a buy and a rent option priced in every region weighs about
// 90 KB, so 100 of them came to 9 MB, past the 4 MiB cap; 20 stay near 1.8 MB
// (bodycap_test.go, #790).
const listPageSize = 20

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
	items, _, err := api.Paginate(api.Pager{Op: opList, Target: pkg, What: "onetimeproducts.list"},
		func(token string, _ int) ([]Item, string, error) {
			var pg struct {
				OneTimeProducts []json.RawMessage `json:"oneTimeProducts"`
				NextPageToken   string            `json:"nextPageToken"`
			}
			if _, err := api.DoJSON(ctx, hc, api.Call{
				Method: mList, Op: opList, Target: pkg,
				Params: map[string]string{"packageName": pkg},
				Query:  pageQuery(token),
			}, &pg); err != nil {
				return nil, "", err
			}
			page := make([]Item, 0, len(pg.OneTimeProducts))
			for _, rawP := range pg.OneTimeProducts {
				var p struct {
					ProductID string `json:"productId"`
				}
				if err := json.Unmarshal(rawP, &p); err != nil {
					return nil, "", &api.Error{Operation: opList, Package: pkg, Message: "decode one-time product: " + err.Error(), Cause: err}
				}
				if p.ProductID == "" {
					return nil, "", &api.Error{Operation: opList, Package: pkg, Message: "response contains a one-time product without a productId: refusing a catalog entry that cannot be addressed"}
				}
				page = append(page, Item{ProductID: p.ProductID, Raw: rawP})
			}
			return page, pg.NextPageToken, nil
		})
	return items, err
}

// pageQuery is the query of one v2 list page: the fixed page size, plus the
// continuation token past the first page.
func pageQuery(token string) url.Values {
	q := url.Values{}
	q.Set("pageSize", strconv.Itoa(listPageSize))
	if token != "" {
		q.Set("pageToken", token)
	}
	return q
}

// ListAllOffers reads every v2 offer of the app in one wildcard walk
// (productId='-', purchaseOptionId='-'), following nextPageToken to
// completion.
func ListAllOffers(ctx context.Context, hc *http.Client, pkg string) ([]OfferItem, error) {
	items, _, err := api.Paginate(api.Pager{Op: opOffersList, Target: pkg, What: "offers.list"},
		func(token string, _ int) ([]OfferItem, string, error) {
			// The wildcard walk rides the same template as a scoped list: `-`
			// is a legal path segment, so escaping leaves it untouched.
			var pg struct {
				OneTimeProductOffers []json.RawMessage `json:"oneTimeProductOffers"`
				NextPageToken        string            `json:"nextPageToken"`
			}
			if _, err := api.DoJSON(ctx, hc, api.Call{
				Method: mOffersList, Op: opOffersList, Target: pkg,
				Params: map[string]string{"packageName": pkg, "productId": "-", "purchaseOptionId": "-"},
				Query:  pageQuery(token),
			}, &pg); err != nil {
				return nil, "", err
			}
			page := make([]OfferItem, 0, len(pg.OneTimeProductOffers))
			for _, rawO := range pg.OneTimeProductOffers {
				var o struct {
					ProductID        string `json:"productId"`
					PurchaseOptionID string `json:"purchaseOptionId"`
					OfferID          string `json:"offerId"`
				}
				if err := json.Unmarshal(rawO, &o); err != nil {
					return nil, "", &api.Error{Operation: opOffersList, Package: pkg, Message: "decode offer: " + err.Error(), Cause: err}
				}
				if o.ProductID == "" || o.PurchaseOptionID == "" || o.OfferID == "" {
					return nil, "", &api.Error{Operation: opOffersList, Package: pkg, Message: "response contains an offer without its full identity (productId/purchaseOptionId/offerId): refusing a catalog entry that cannot be addressed"}
				}
				page = append(page, OfferItem{ProductID: o.ProductID, PurchaseOptionID: o.PurchaseOptionID, OfferID: o.OfferID, Raw: rawO})
			}
			return page, pg.NextPageToken, nil
		})
	return items, err
}

// PatchOneTimeProduct upserts (allowMissing=true, the v2 create: the API has
// no insert) or edits (updateMask scoped to the changed managed fields) one
// product from the catalog-file resource, sent verbatim. The Discovery
// snapshot paths this method under lowercase `onetimeproducts/`, unlike the
// reads: followed verbatim.
func PatchOneTimeProduct(ctx context.Context, hc *http.Client, pkg, productID, regionsVersion string, updateMask []string, allowMissing bool, body json.RawMessage, lt latency.Tolerance) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("regionsVersion.version", regionsVersion)
	lt.SetQuery(q)
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
func DeleteOneTimeProduct(ctx context.Context, hc *http.Client, pkg, productID string, lt latency.Tolerance) error {
	q := url.Values{}
	lt.SetQuery(q)
	_, err := api.Do(ctx, hc, api.Call{
		Method: mDelete, Op: opDelete, Target: pkg,
		Params: map[string]string{"packageName": pkg, "productId": productID},
		Query:  q,
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

// maxBatchRequests is the request count each one-time-product batch body
// takes: Discovery documents its `requests` list "of up to 100 elements".
const maxBatchRequests = 100

// BatchError reports a batch write gplay split into calls of at most
// maxBatchRequests that failed part-way. Applied names the items of the calls
// Play accepted, Failed those of the failing call, NotSent those of the calls
// never sent. OutcomeUnknown says whether the failing call may have been
// applied all the same (a 5xx, a transport failure once the request left, a
// 2xx whose body broke): only a 4xx or a request that never left proves it
// was not. It wraps the failing call's error, so the exit code and the
// envelope stay the API's.
type BatchError struct {
	Op             string // REST method id
	Target         string // productId, or productId/purchaseOptionId
	Applied        []string
	Failed         []string
	NotSent        []string
	OutcomeUnknown bool
	Cause          error
}

func (e *BatchError) Error() string {
	list := func(ids []string) string {
		if len(ids) == 0 {
			return "none"
		}
		return strings.Join(ids, ", ")
	}
	total := len(e.Applied) + len(e.Failed) + len(e.NotSent)
	detail := "not applied: " + list(append(append([]string{}, e.Failed...), e.NotSent...))
	if e.OutcomeUnknown {
		detail = "outcome unknown (Play may have applied them): " + list(e.Failed) +
			"; not sent: " + list(e.NotSent) + "; `gplay iap apply --dry-run` shows what is still pending"
	}
	return fmt.Sprintf("%s on %s failed after %d of %d request(s) were applied in calls of at most %d (applied: %s; %s): %v",
		e.Op, e.Target, len(e.Applied), total, maxBatchRequests, list(e.Applied), detail, e.Cause)
}

func (e *BatchError) Unwrap() error { return e.Cause }

// refused reports whether a failed batch call provably wrote nothing: Play
// answered 4xx (429 included: refused before the write path), or the request
// never left the machine (api.NeverSent, the line edits.commit draws for
// COMMIT_OUTCOME_UNKNOWN). Anything else may have landed.
func refused(err error) bool {
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.StatusCode == 0 {
		return api.NeverSent(apiErr.Cause)
	}
	return apiErr.StatusCode >= 400 && apiErr.StatusCode < 500
}

// sendInBatches sends ids in order, at most maxBatchRequests per call:
// send(lo, hi) writes ids[lo:hi]. A batch that fits one call fails with the
// call's own error; a split one fails with a *BatchError naming what Play
// already applied.
func sendInBatches(op, target string, ids []string, send func(lo, hi int) error) error {
	for lo := 0; lo < len(ids); lo += maxBatchRequests {
		hi := min(lo+maxBatchRequests, len(ids))
		if err := send(lo, hi); err != nil {
			if len(ids) <= maxBatchRequests {
				return err
			}
			return &BatchError{Op: op, Target: target, Applied: ids[:lo], Failed: ids[lo:hi], NotSent: ids[hi:], OutcomeUnknown: !refused(err), Cause: err}
		}
	}
	return nil
}

// BatchUpdateOffers creates/edits the given offers of one purchase option
// through offers:batchUpdate, the only write path the API exposes for offers,
// in calls of at most maxBatchRequests.
func BatchUpdateOffers(ctx context.Context, hc *http.Client, pkg, productID, purchaseOptionID string, updates []OfferUpdate, regionsVersion string, lt latency.Tolerance) error {
	type updateReq struct {
		OneTimeProductOffer json.RawMessage   `json:"oneTimeProductOffer"`
		AllowMissing        bool              `json:"allowMissing,omitempty"`
		UpdateMask          string            `json:"updateMask,omitempty"`
		LatencyTolerance    latency.Tolerance `json:"latencyTolerance,omitempty"`
		RegionsVersion      struct {
			Version string `json:"version"`
		} `json:"regionsVersion"`
	}
	reqs := make([]updateReq, 0, len(updates))
	ids := make([]string, 0, len(updates))
	for _, upd := range updates {
		r := updateReq{OneTimeProductOffer: upd.Offer, AllowMissing: upd.AllowMissing, UpdateMask: strings.Join(upd.UpdateMask, ","), LatencyTolerance: lt}
		r.RegionsVersion.Version = regionsVersion
		reqs = append(reqs, r)
		var offer struct {
			OfferID string `json:"offerId"`
		}
		_ = json.Unmarshal(upd.Offer, &offer) // names the offer in a BatchError only
		ids = append(ids, offer.OfferID)
	}
	return sendInBatches(opOffersBatchUpdate, productID+"/"+purchaseOptionID, ids, func(lo, hi int) error {
		_, err := api.Do(ctx, hc, api.Call{
			Method: mOffersBatchUpdate, Op: opOffersBatchUpdate, Target: pkg,
			Params: map[string]string{"packageName": pkg, "productId": productID, "purchaseOptionId": purchaseOptionID},
			Body: struct {
				Requests []updateReq `json:"requests"`
			}{Requests: reqs[lo:hi]},
		})
		return err
	})
}

// BatchDeleteOffers deletes the given offers of one purchase option through
// offers:batchDelete, naming each offer's full identity, in calls of at most
// maxBatchRequests.
func BatchDeleteOffers(ctx context.Context, hc *http.Client, pkg, productID, purchaseOptionID string, offerIDs []string, lt latency.Tolerance) error {
	type deleteReq struct {
		PackageName      string            `json:"packageName"`
		ProductID        string            `json:"productId"`
		PurchaseOptionID string            `json:"purchaseOptionId"`
		OfferID          string            `json:"offerId"`
		LatencyTolerance latency.Tolerance `json:"latencyTolerance,omitempty"`
	}
	reqs := make([]deleteReq, 0, len(offerIDs))
	for _, id := range offerIDs {
		reqs = append(reqs, deleteReq{PackageName: pkg, ProductID: productID, PurchaseOptionID: purchaseOptionID, OfferID: id, LatencyTolerance: lt})
	}
	return sendInBatches(opOffersBatchDelete, productID+"/"+purchaseOptionID, offerIDs, func(lo, hi int) error {
		_, err := api.Do(ctx, hc, api.Call{
			Method: mOffersBatchDelete, Op: opOffersBatchDelete, Target: pkg,
			Params: map[string]string{"packageName": pkg, "productId": productID, "purchaseOptionId": purchaseOptionID},
			Body: struct {
				Requests []deleteReq `json:"requests"`
			}{Requests: reqs[lo:hi]},
		})
		return err
	})
}

// PurchaseOptionStateUpdate is one entry of a BatchUpdatePurchaseOptionStates
// call: the purchase option to move, and the direction (activate or
// deactivate: the two verbs that sub-resource exposes).
type PurchaseOptionStateUpdate struct {
	PurchaseOptionID string
	Activate         bool
}

// BatchUpdatePurchaseOptionStates moves the given purchase options of one
// product through purchaseOptions:batchUpdateStates, in calls of at most
// maxBatchRequests: the only state write path the API exposes for purchase
// options (no unary verb exists, unlike offers). The body is the oneof shape
// of UpdatePurchaseOptionStateRequest: one of activatePurchaseOptionRequest /
// deactivatePurchaseOptionRequest per entry, each echoing the identity the
// schema requires.
func BatchUpdatePurchaseOptionStates(ctx context.Context, hc *http.Client, pkg, productID string, updates []PurchaseOptionStateUpdate, lt latency.Tolerance) error {
	type optionReq struct {
		PackageName      string            `json:"packageName"`
		ProductID        string            `json:"productId"`
		PurchaseOptionID string            `json:"purchaseOptionId"`
		LatencyTolerance latency.Tolerance `json:"latencyTolerance,omitempty"`
	}
	type stateReq struct {
		Activate   *optionReq `json:"activatePurchaseOptionRequest,omitempty"`
		Deactivate *optionReq `json:"deactivatePurchaseOptionRequest,omitempty"`
	}
	reqs := make([]stateReq, 0, len(updates))
	ids := make([]string, 0, len(updates))
	for _, upd := range updates {
		id := &optionReq{PackageName: pkg, ProductID: productID, PurchaseOptionID: upd.PurchaseOptionID, LatencyTolerance: lt}
		if upd.Activate {
			reqs = append(reqs, stateReq{Activate: id})
		} else {
			reqs = append(reqs, stateReq{Deactivate: id})
		}
		ids = append(ids, upd.PurchaseOptionID)
	}
	return sendInBatches(opOptionStates, productID, ids, func(lo, hi int) error {
		_, err := api.Do(ctx, hc, api.Call{
			Method: mOptionStates, Op: opOptionStates, Target: pkg,
			Params: map[string]string{"packageName": pkg, "productId": productID},
			Body: struct {
				Requests []stateReq `json:"requests"`
			}{Requests: reqs[lo:hi]},
		})
		return err
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
func SetOfferState(ctx context.Context, hc *http.Client, pkg, productID, purchaseOptionID, offerID, target string, lt latency.Tolerance) (json.RawMessage, error) {
	var (
		m  apiregistry.Method
		op string
	)
	switch target {
	case StateActive:
		m, op = mOffersActivate, opOffersActivate
	case StateInactive:
		m, op = mOffersDeactivate, opOffersDeactivate
	case StateCancelled:
		m, op = mOffersCancel, opOffersCancel
	default:
		return nil, &api.Error{Operation: opOffersActivate, Package: pkg, Message: "unsupported offer state target " + strconv.Quote(target)}
	}
	identity := map[string]string{"packageName": pkg, "productId": productID, "purchaseOptionId": purchaseOptionID, "offerId": offerID}
	body := maps.Clone(identity)
	if lt != "" {
		body["latencyTolerance"] = string(lt)
	}
	return api.Do(ctx, hc, api.Call{
		Method: m, Op: op, Target: pkg,
		Params: identity,
		Body:   body,
	})
}

// ErrLegacyRetired reports that Google refuses the legacy inappproducts API
// for this app and points to the v2 model instead (#741). The refusal is a
// 403 that carries no discriminating reason, only its message, so the message
// is the signal.
var ErrLegacyRetired = errors.New("legacy inappproducts API retired by Google for this app")

const legacyRetiredMessage = "migrate to the new publishing api"

// ListInAppProducts reads the complete legacy inappproducts catalog, following
// the legacy tokenPagination envelope to completion. Read-only: gplay never
// writes the legacy surface in place (ADR-0041 §8). When Google refuses the
// legacy API for the app, the error wraps ErrLegacyRetired.
func ListInAppProducts(ctx context.Context, hc *http.Client, pkg string) ([]LegacyItem, error) {
	items, err := listInAppProducts(ctx, hc, pkg)
	var apiErr *api.Error
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden && strings.Contains(strings.ToLower(apiErr.Message), legacyRetiredMessage) {
		return nil, errors.Join(ErrLegacyRetired, err)
	}
	return items, err
}

func listInAppProducts(ctx context.Context, hc *http.Client, pkg string) ([]LegacyItem, error) {
	items, _, err := api.Paginate(api.Pager{Op: opLegacyList, Target: pkg, What: "inappproducts.list"},
		func(token string, _ int) ([]LegacyItem, string, error) {
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
				return nil, "", err
			}
			page := make([]LegacyItem, 0, len(pg.InAppProduct))
			for _, rawP := range pg.InAppProduct {
				var p struct {
					SKU string `json:"sku"`
				}
				if err := json.Unmarshal(rawP, &p); err != nil {
					return nil, "", &api.Error{Operation: opLegacyList, Package: pkg, Message: "decode in-app product: " + err.Error(), Cause: err}
				}
				if p.SKU == "" {
					return nil, "", &api.Error{Operation: opLegacyList, Package: pkg, Message: "response contains an in-app product without a sku: refusing a catalog entry that cannot be addressed"}
				}
				page = append(page, LegacyItem{SKU: p.SKU, Raw: rawP})
			}
			return page, pg.TokenPagination.NextPageToken, nil
		})
	return items, err
}
