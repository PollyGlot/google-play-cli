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
	opList    = "monetization.subscriptions.list"
	opCreate  = "monetization.subscriptions.create"
	opPatch   = "monetization.subscriptions.patch"
	opDelete  = "monetization.subscriptions.delete"
	opConvert = "monetization.convertRegionPrices"
)

var (
	mList    = apiregistry.MustResolve("androidpublisher.monetization.subscriptions.list")
	mCreate  = apiregistry.MustResolve("androidpublisher.monetization.subscriptions.create")
	mPatch   = apiregistry.MustResolve("androidpublisher.monetization.subscriptions.patch")
	mDelete  = apiregistry.MustResolve("androidpublisher.monetization.subscriptions.delete")
	mConvert = apiregistry.MustResolve("androidpublisher.monetization.convertRegionPrices")
)

const (
	StateDraft    = "DRAFT"
	StateActive   = "ACTIVE"
	StateInactive = "INACTIVE"
)

type Money struct {
	CurrencyCode string `json:"currencyCode,omitempty"`
	Units        string `json:"units,omitempty"`
	Nanos        int32  `json:"nanos,omitempty"`
}

const listPageSize = 20

type Item struct {
	ProductID string
	Raw       json.RawMessage
}

type listPage struct {
	Subscriptions []json.RawMessage `json:"subscriptions"`
	NextPageToken string            `json:"nextPageToken"`
}

func ListSource(pkg string) api.Source { return api.Source{Operation: opList, Package: pkg} }

func List(ctx context.Context, hc *http.Client, pkg string) ([]Item, error) {
	items, _, err := api.Paginate(api.Pager{Op: opList, Target: pkg, What: "monetization.subscriptions.list"},
		func(token string, _ int) ([]Item, string, error) {
			q := url.Values{}
			q.Set("pageSize", strconv.Itoa(listPageSize))
			if token != "" {
				q.Set("pageToken", token)
			}
			var pg listPage
			if _, err := api.DoJSON(ctx, hc, api.Call{
				Method: mList, Op: opList, Target: pkg,
				Params: map[string]string{"packageName": pkg},
				Query:  q,
			}, &pg); err != nil {
				return nil, "", err
			}
			page := make([]Item, 0, len(pg.Subscriptions))
			for _, rawSub := range pg.Subscriptions {
				var s struct {
					ProductID string `json:"productId"`
				}
				if err := json.Unmarshal(rawSub, &s); err != nil {
					return nil, "", api.DecodeFailure(api.Error{Operation: opList, Package: pkg, Message: "decode subscription: " + err.Error(), Cause: err})
				}
				if s.ProductID == "" {
					return nil, "", api.DecodeFailure(api.Error{Operation: opList, Package: pkg, Message: "response contains a subscription without a productId: refusing a catalog entry that cannot be addressed"})
				}
				page = append(page, Item{ProductID: s.ProductID, Raw: rawSub})
			}
			return page, pg.NextPageToken, nil
		})
	return items, err
}

func Create(ctx context.Context, hc *http.Client, pkg, productID, regionsVersion string, body json.RawMessage) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("productId", productID)
	q.Set("regionsVersion.version", regionsVersion)
	return api.Do(ctx, hc, api.Call{
		Method: mCreate, Op: opCreate, Target: pkg,
		Params: map[string]string{"packageName": pkg},
		Query:  q,
		Body:   body,
	})
}

func Patch(ctx context.Context, hc *http.Client, pkg, productID, regionsVersion string, updateMask []string, body json.RawMessage, lt latency.Tolerance) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("updateMask", strings.Join(updateMask, ","))
	q.Set("regionsVersion.version", regionsVersion)
	lt.SetQuery(q)
	return api.Do(ctx, hc, api.Call{
		Method: mPatch, Op: opPatch, Target: pkg,
		Params: map[string]string{"packageName": pkg, "productId": productID},
		Query:  q,
		Body:   body,
	})
}

func Delete(ctx context.Context, hc *http.Client, pkg, productID string) error {
	_, err := api.Do(ctx, hc, api.Call{
		Method: mDelete, Op: opDelete, Target: pkg,
		Params: map[string]string{"packageName": pkg, "productId": productID},
	})
	return err
}

func ConvertRegionPrices(ctx context.Context, hc *http.Client, pkg string, price Money) (json.RawMessage, error) {
	return api.Do(ctx, hc, api.Call{
		Method: mConvert, Op: opConvert, Target: pkg,
		Params: map[string]string{"packageName": pkg},
		Body: struct {
			Price Money `json:"price"`
		}{Price: price},
	})
}
