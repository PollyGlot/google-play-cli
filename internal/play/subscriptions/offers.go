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
	"github.com/PollyGlot/google-play-cli/internal/play/latency"
)

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

type OfferItem struct {
	ProductID  string
	BasePlanID string
	OfferID    string
	Raw        json.RawMessage
}

type offersPage struct {
	SubscriptionOffers []json.RawMessage `json:"subscriptionOffers"`
	NextPageToken      string            `json:"nextPageToken"`
}

func ListAllOffers(ctx context.Context, hc *http.Client, pkg string) ([]OfferItem, error) {
	items, _, err := api.Paginate(api.Pager{Op: opOffersList, Target: pkg, What: "offers.list"},
		func(token string, _ int) ([]OfferItem, string, error) {
			q := url.Values{}
			q.Set("pageSize", strconv.Itoa(listPageSize))
			if token != "" {
				q.Set("pageToken", token)
			}
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

func PatchOffer(ctx context.Context, hc *http.Client, pkg, productID, basePlanID, offerID, regionsVersion string, updateMask []string, body json.RawMessage, lt latency.Tolerance) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("updateMask", strings.Join(updateMask, ","))
	q.Set("regionsVersion.version", regionsVersion)
	lt.SetQuery(q)
	return api.Do(ctx, hc, api.Call{
		Method: mOffersPatch, Op: opOffersPatch, Target: pkg,
		Params: offerScope(pkg, productID, basePlanID, offerID),
		Query:  q,
		Body:   body,
	})
}

func DeleteOffer(ctx context.Context, hc *http.Client, pkg, productID, basePlanID, offerID string) error {
	_, err := api.Do(ctx, hc, api.Call{
		Method: mOffersDelete, Op: opOffersDelete, Target: pkg,
		Params: offerScope(pkg, productID, basePlanID, offerID),
	})
	return err
}

func SetBasePlanState(ctx context.Context, hc *http.Client, pkg, productID, basePlanID string, activate bool, lt latency.Tolerance) (json.RawMessage, error) {
	m, op := mBasePlanDeactivate, opBasePlanDeactivate
	if activate {
		m, op = mBasePlanActivate, opBasePlanActivate
	}
	return postState(ctx, hc, m, op, pkg, offerScope(pkg, productID, basePlanID), lt)
}

func DeleteBasePlan(ctx context.Context, hc *http.Client, pkg, productID, basePlanID string) error {
	_, err := api.Do(ctx, hc, api.Call{
		Method: mBasePlanDelete, Op: opBasePlanDelete, Target: pkg,
		Params: offerScope(pkg, productID, basePlanID),
	})
	return err
}

func SetOfferState(ctx context.Context, hc *http.Client, pkg, productID, basePlanID, offerID string, activate bool, lt latency.Tolerance) (json.RawMessage, error) {
	m, op := mOffersDeactivate, opOffersDeactivate
	if activate {
		m, op = mOffersActivate, opOffersActivate
	}
	return postState(ctx, hc, m, op, pkg, offerScope(pkg, productID, basePlanID, offerID), lt)
}

const opMigratePrices = "monetization.subscriptions.basePlans.migratePrices"

type RegionsVersion struct {
	Version string `json:"version"`
}

type RegionalPriceMigration struct {
	RegionCode                    string `json:"regionCode"`
	OldestAllowedPriceVersionTime string `json:"oldestAllowedPriceVersionTime"`
	PriceIncreaseType             string `json:"priceIncreaseType,omitempty"`
}

type MigrateBasePlanPricesRequest struct {
	PackageName             string                   `json:"packageName"`
	ProductID               string                   `json:"productId"`
	BasePlanID              string                   `json:"basePlanId"`
	RegionalPriceMigrations []RegionalPriceMigration `json:"regionalPriceMigrations"`
	RegionsVersion          RegionsVersion           `json:"regionsVersion"`
}

func MigrateBasePlanPrices(ctx context.Context, hc *http.Client, pkg, productID, basePlanID string, request MigrateBasePlanPricesRequest) (json.RawMessage, error) {
	return api.Do(ctx, hc, api.Call{
		Method: mMigratePrices, Op: opMigratePrices, Target: pkg,
		Params: offerScope(pkg, productID, basePlanID),
		Body:   request,
	})
}

func offerScope(pkg, productID, basePlanID string, offerID ...string) map[string]string {
	p := map[string]string{"packageName": pkg, "productId": productID, "basePlanId": basePlanID}
	if len(offerID) == 1 {
		p["offerId"] = offerID[0]
	}
	return p
}

func postState(ctx context.Context, hc *http.Client, m apiregistry.Method, op, pkg string, params map[string]string, lt latency.Tolerance) (json.RawMessage, error) {
	body := struct {
		LatencyTolerance latency.Tolerance `json:"latencyTolerance,omitempty"`
	}{lt}
	return api.Do(ctx, hc, api.Call{Method: m, Op: op, Target: pkg, Params: params, Body: body})
}
