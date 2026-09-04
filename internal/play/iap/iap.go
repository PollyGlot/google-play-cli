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
// purchase-option batch endpoints (no single create/patch exists).
package iap

import (
	"context"
	"encoding/json"
	"io"
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
	mLegacyList        = apiregistry.MustResolve("androidpublisher.inappproducts.list")
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
		u, err := mList.URL(map[string]string{"packageName": pkg})
		if err != nil {
			return nil, &api.Error{Operation: opList, Package: pkg, Message: err.Error(), Cause: err}
		}
		req, err := http.NewRequestWithContext(ctx, mList.Verb, u+"?"+q.Encode(), nil)
		if err != nil {
			return nil, &api.Error{Operation: opList, Package: pkg, Message: err.Error(), Cause: err}
		}
		raw, err := do(hc, opList, pkg, req)
		if err != nil {
			return nil, err
		}
		var pg struct {
			OneTimeProducts []json.RawMessage `json:"oneTimeProducts"`
			NextPageToken   string            `json:"nextPageToken"`
		}
		if err := json.Unmarshal(raw, &pg); err != nil {
			return nil, &api.Error{Operation: opList, Package: pkg, Message: "decode response: " + err.Error(), Cause: err}
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
		u, err := mOffersList.URL(map[string]string{"packageName": pkg, "productId": "-", "purchaseOptionId": "-"})
		if err != nil {
			return nil, &api.Error{Operation: opOffersList, Package: pkg, Message: err.Error(), Cause: err}
		}
		req, err := http.NewRequestWithContext(ctx, mOffersList.Verb, u+"?"+q.Encode(), nil)
		if err != nil {
			return nil, &api.Error{Operation: opOffersList, Package: pkg, Message: err.Error(), Cause: err}
		}
		raw, err := do(hc, opOffersList, pkg, req)
		if err != nil {
			return nil, err
		}
		var pg struct {
			OneTimeProductOffers []json.RawMessage `json:"oneTimeProductOffers"`
			NextPageToken        string            `json:"nextPageToken"`
		}
		if err := json.Unmarshal(raw, &pg); err != nil {
			return nil, &api.Error{Operation: opOffersList, Package: pkg, Message: "decode response: " + err.Error(), Cause: err}
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
	u, err := mPatch.URL(map[string]string{"packageName": pkg, "productId": productID})
	if err != nil {
		return nil, &api.Error{Operation: opPatch, Package: pkg, Message: err.Error(), Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, mPatch.Verb, u+"?"+q.Encode(), strings.NewReader(string(body)))
	if err != nil {
		return nil, &api.Error{Operation: opPatch, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	return do(hc, opPatch, pkg, req)
}

// DeleteOneTimeProduct deletes one v2 product. Reaching here means the plan
// carried a delete and the operator passed --confirm (ADR-0041 §3).
func DeleteOneTimeProduct(ctx context.Context, hc *http.Client, pkg, productID string) error {
	u, err := mDelete.URL(map[string]string{"packageName": pkg, "productId": productID})
	if err != nil {
		return &api.Error{Operation: opDelete, Package: pkg, Message: err.Error(), Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, mDelete.Verb, u, nil)
	if err != nil {
		return &api.Error{Operation: opDelete, Package: pkg, Message: err.Error(), Cause: err}
	}
	_, err = do(hc, opDelete, pkg, req)
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
	body, err := json.Marshal(struct {
		Requests []updateReq `json:"requests"`
	}{Requests: reqs})
	if err != nil {
		return &api.Error{Operation: opOffersBatchUpdate, Package: pkg, Message: "encode request: " + err.Error(), Cause: err}
	}
	u, err := mOffersBatchUpdate.URL(map[string]string{"packageName": pkg, "productId": productID, "purchaseOptionId": purchaseOptionID})
	if err != nil {
		return &api.Error{Operation: opOffersBatchUpdate, Package: pkg, Message: err.Error(), Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, mOffersBatchUpdate.Verb, u, strings.NewReader(string(body)))
	if err != nil {
		return &api.Error{Operation: opOffersBatchUpdate, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	_, err = do(hc, opOffersBatchUpdate, pkg, req)
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
	body, err := json.Marshal(struct {
		Requests []deleteReq `json:"requests"`
	}{Requests: reqs})
	if err != nil {
		return &api.Error{Operation: opOffersBatchDelete, Package: pkg, Message: "encode request: " + err.Error(), Cause: err}
	}
	u, err := mOffersBatchDelete.URL(map[string]string{"packageName": pkg, "productId": productID, "purchaseOptionId": purchaseOptionID})
	if err != nil {
		return &api.Error{Operation: opOffersBatchDelete, Package: pkg, Message: err.Error(), Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, mOffersBatchDelete.Verb, u, strings.NewReader(string(body)))
	if err != nil {
		return &api.Error{Operation: opOffersBatchDelete, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	_, err = do(hc, opOffersBatchDelete, pkg, req)
	return err
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
		u, err := mLegacyList.URL(map[string]string{"packageName": pkg})
		if err != nil {
			return nil, &api.Error{Operation: opLegacyList, Package: pkg, Message: err.Error(), Cause: err}
		}
		if token != "" {
			q := url.Values{}
			q.Set("token", token)
			u += "?" + q.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, mLegacyList.Verb, u, nil)
		if err != nil {
			return nil, &api.Error{Operation: opLegacyList, Package: pkg, Message: err.Error(), Cause: err}
		}
		raw, err := do(hc, opLegacyList, pkg, req)
		if err != nil {
			return nil, err
		}
		var pg struct {
			InAppProduct    []json.RawMessage `json:"inappproduct"`
			TokenPagination struct {
				NextPageToken string `json:"nextPageToken"`
			} `json:"tokenPagination"`
		}
		if err := json.Unmarshal(raw, &pg); err != nil {
			return nil, &api.Error{Operation: opLegacyList, Package: pkg, Message: "decode response: " + err.Error(), Cause: err}
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

// do runs req and maps the response to (raw body, *api.Error): a non-2xx body is
// parsed for the error envelope, a 2xx body is returned verbatim for the
// ADR-0003 pass-through.
func do(hc *http.Client, op, pkg string, req *http.Request) (json.RawMessage, error) {
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(b, resp.StatusCode)
		return nil, &api.Error{Operation: op, Package: pkg, StatusCode: resp.StatusCode, Message: msg, Reasons: reasons}
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if readErr != nil {
		return nil, &api.Error{Operation: op, Package: pkg, StatusCode: resp.StatusCode, Message: "read response body: " + readErr.Error(), Cause: readErr}
	}
	return json.RawMessage(raw), nil
}
