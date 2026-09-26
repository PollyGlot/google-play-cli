// Package subscriptions calls the Android Publisher monetization.subscriptions
// endpoints: the subscription level of the Monetization catalog (CONTEXT.md).
// These are Edit-free, application-scoped calls on the package axis
// (applications/{packageName}/subscriptions...), like deviceTierConfigs and
// orders. Raw HTTP (ADR-0007), never the google-go-sdk.
//
// This package ships the declarative-catalog surface of PRD #51: the
// subscription level (list followed to completion, create, patch with a
// caller-scoped updateMask, delete: slice #367), the pricing helper
// convertRegionPrices (#368), and the offers sub-resource plus the base-plan/
// offer state ops (offers.go, #369). Subscriber price migration (#370) is the
// remaining monetization write. See ADR-0041.
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
	opList    = "monetization.subscriptions.list"
	opCreate  = "monetization.subscriptions.create"
	opPatch   = "monetization.subscriptions.patch"
	opDelete  = "monetization.subscriptions.delete"
	opConvert = "monetization.convertRegionPrices"
)

// m* are the registry entries this package calls. Resolving them at init turns
// an unregistered or vanished method into a startup panic CI catches (the
// registry tests resolve every entry), never a runtime surprise for a user;
// verb and URL template then come from the Discovery snapshot instead of
// literals maintained here (#513).
var (
	mList    = apiregistry.MustResolve("androidpublisher.monetization.subscriptions.list")
	mCreate  = apiregistry.MustResolve("androidpublisher.monetization.subscriptions.create")
	mPatch   = apiregistry.MustResolve("androidpublisher.monetization.subscriptions.patch")
	mDelete  = apiregistry.MustResolve("androidpublisher.monetization.subscriptions.delete")
	mConvert = apiregistry.MustResolve("androidpublisher.monetization.convertRegionPrices")
)

// Money mirrors the Money schema: whole units (a decimal int64 string) plus
// nano (10^-9) units, tagged with an ISO-4217 currency.
type Money struct {
	CurrencyCode string `json:"currencyCode,omitempty"`
	Units        string `json:"units,omitempty"`
	Nanos        int32  `json:"nanos,omitempty"`
}

// listPageSize is the page size subscriptions.list requests; List follows
// nextPageToken to completion regardless (reconciliation needs the whole
// catalog, silent truncation would surface as phantom deletes in a plan).
const listPageSize = 100

// Item is one live subscription: the parsed product ID plus the verbatim
// resource bytes (the unit `pull` writes to a catalog file, ADR-0003 spirit).
type Item struct {
	ProductID string
	Raw       json.RawMessage
}

// listPage mirrors the ListSubscriptionsResponse envelope, keeping each
// subscription as raw bytes so the pass-through survives the merge.
type listPage struct {
	Subscriptions []json.RawMessage `json:"subscriptions"`
	NextPageToken string            `json:"nextPageToken"`
}

// List reads the complete live subscription catalog of a package, following
// nextPageToken to completion (no silent truncation: a missing page would read
// as deletes in a Reconciliation plan). No Edit: the GET is application-scoped.
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
					return nil, "", &api.Error{Operation: opList, Package: pkg, Message: "decode subscription: " + err.Error(), Cause: err}
				}
				if s.ProductID == "" {
					return nil, "", &api.Error{Operation: opList, Package: pkg, Message: "response contains a subscription without a productId: refusing a catalog entry that cannot be addressed"}
				}
				page = append(page, Item{ProductID: s.ProductID, Raw: rawSub})
			}
			return page, pg.NextPageToken, nil
		})
	return items, err
}

// Create creates a subscription from the catalog-file resource, sent verbatim
// as the request body. productId and regionsVersion.version ride as query
// parameters: the API requires the regions version pin for any write that
// carries regional prices (ADR-0041 §7).
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

// Patch updates a subscription from the catalog-file resource, sent verbatim,
// with the updateMask scoped to exactly the fields the caller reconciles: the
// mechanism that keeps anything outside the caller's managed set out of reach
// of an apply (ADR-0041 §5).
func Patch(ctx context.Context, hc *http.Client, pkg, productID, regionsVersion string, updateMask []string, body json.RawMessage) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("updateMask", strings.Join(updateMask, ","))
	q.Set("regionsVersion.version", regionsVersion)
	return api.Do(ctx, hc, api.Call{
		Method: mPatch, Op: opPatch, Target: pkg,
		Params: map[string]string{"packageName": pkg, "productId": productID},
		Query:  q,
		Body:   body,
	})
}

// Delete deletes a subscription. Reaching here means the plan carried a delete
// and the operator passed --confirm; the API additionally refuses to delete a
// subscription that ever had a published base plan, so the gate covers intent
// while the server covers damage (ADR-0041).
func Delete(ctx context.Context, hc *http.Client, pkg, productID string) error {
	_, err := api.Do(ctx, hc, api.Call{
		Method: mDelete, Op: opDelete, Target: pkg,
		Params: map[string]string{"packageName": pkg, "productId": productID},
	})
	return err
}

// ConvertRegionPrices derives per-region prices from one base price using
// today's exchange rates and Google's country-specific pricing patterns
// (monetization.convertRegionPrices): the pricing helper of the Monetization
// catalog (ADR-0041 §9). Read-only in effect: it computes, it never writes
// catalog state. The verbatim response is the ADR-0003 pass-through.
func ConvertRegionPrices(ctx context.Context, hc *http.Client, pkg string, price Money) (json.RawMessage, error) {
	return api.Do(ctx, hc, api.Call{
		Method: mConvert, Op: opConvert, Target: pkg,
		Params: map[string]string{"packageName": pkg},
		Body: struct {
			Price Money `json:"price"`
		}{Price: price},
	})
}
