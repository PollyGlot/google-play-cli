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

const (
	StateDraft             = "DRAFT"
	StateActive            = "ACTIVE"
	StateInactive          = "INACTIVE"
	StateCancelled         = "CANCELLED"
	StateInactivePublished = "INACTIVE_PUBLISHED"
)

const listPageSize = 20

type Item struct {
	ProductID string
	Raw       json.RawMessage
}

type OfferItem struct {
	ProductID        string
	PurchaseOptionID string
	OfferID          string
	Raw              json.RawMessage
}

type LegacyItem struct {
	SKU string
	Raw json.RawMessage
}

func ListSource(pkg string) api.Source { return api.Source{Operation: opList, Package: pkg} }

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
					return nil, "", api.DecodeFailure(api.Error{Operation: opList, Package: pkg, Message: "decode one-time product: " + err.Error(), Cause: err})
				}
				if p.ProductID == "" {
					return nil, "", api.DecodeFailure(api.Error{Operation: opList, Package: pkg, Message: "response contains a one-time product without a productId: refusing a catalog entry that cannot be addressed"})
				}
				page = append(page, Item{ProductID: p.ProductID, Raw: rawP})
			}
			return page, pg.NextPageToken, nil
		})
	return items, err
}

func pageQuery(token string) url.Values {
	q := url.Values{}
	q.Set("pageSize", strconv.Itoa(listPageSize))
	if token != "" {
		q.Set("pageToken", token)
	}
	return q
}

func OffersListSource(pkg string) api.Source {
	return api.Source{Operation: opOffersList, Package: pkg}
}

func ListAllOffers(ctx context.Context, hc *http.Client, pkg string) ([]OfferItem, error) {
	items, _, err := api.Paginate(api.Pager{Op: opOffersList, Target: pkg, What: "offers.list"},
		func(token string, _ int) ([]OfferItem, string, error) {
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
					return nil, "", api.DecodeFailure(api.Error{Operation: opOffersList, Package: pkg, Message: "decode offer: " + err.Error(), Cause: err})
				}
				if o.ProductID == "" || o.PurchaseOptionID == "" || o.OfferID == "" {
					return nil, "", api.DecodeFailure(api.Error{Operation: opOffersList, Package: pkg, Message: "response contains an offer without its full identity (productId/purchaseOptionId/offerId): refusing a catalog entry that cannot be addressed"})
				}
				page = append(page, OfferItem{ProductID: o.ProductID, PurchaseOptionID: o.PurchaseOptionID, OfferID: o.OfferID, Raw: rawO})
			}
			return page, pg.NextPageToken, nil
		})
	return items, err
}

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

type OfferUpdate struct {
	Offer        json.RawMessage
	AllowMissing bool
	UpdateMask   []string
}

const maxBatchRequests = 100

type BatchError struct {
	Op             string
	Target         string
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
		_ = json.Unmarshal(upd.Offer, &offer)
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

type PurchaseOptionStateUpdate struct {
	PurchaseOptionID string
	Activate         bool
}

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
		return nil, api.BuildFailure(api.Error{Operation: opOffersActivate, Package: pkg, Message: "unsupported offer state target " + strconv.Quote(target)})
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

var ErrLegacyRetired = errors.New("legacy inappproducts API retired by Google for this app")

const legacyRetiredMessage = "migrate to the new publishing api"

func InAppProductsListSource(pkg string) api.Source {
	return api.Source{Operation: opLegacyList, Package: pkg}
}

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
					return nil, "", api.DecodeFailure(api.Error{Operation: opLegacyList, Package: pkg, Message: "decode in-app product: " + err.Error(), Cause: err})
				}
				if p.SKU == "" {
					return nil, "", api.DecodeFailure(api.Error{Operation: opLegacyList, Package: pkg, Message: "response contains an in-app product without a sku: refusing a catalog entry that cannot be addressed"})
				}
				page = append(page, LegacyItem{SKU: p.SKU, Raw: rawP})
			}
			return page, pg.TokenPagination.NextPageToken, nil
		})
	return items, err
}
