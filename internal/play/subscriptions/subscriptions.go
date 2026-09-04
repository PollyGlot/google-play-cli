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
		var pg listPage
		if err := json.Unmarshal(raw, &pg); err != nil {
			return nil, &api.Error{Operation: opList, Package: pkg, Message: "decode response: " + err.Error(), Cause: err}
		}
		for _, rawSub := range pg.Subscriptions {
			var s struct {
				ProductID string `json:"productId"`
			}
			if err := json.Unmarshal(rawSub, &s); err != nil {
				return nil, &api.Error{Operation: opList, Package: pkg, Message: "decode subscription: " + err.Error(), Cause: err}
			}
			if s.ProductID == "" {
				return nil, &api.Error{Operation: opList, Package: pkg, Message: "response contains a subscription without a productId: refusing a catalog entry that cannot be addressed"}
			}
			items = append(items, Item{ProductID: s.ProductID, Raw: rawSub})
		}
		if pg.NextPageToken == "" {
			return items, nil
		}
		if _, dup := seen[pg.NextPageToken]; dup {
			return nil, &api.Error{
				Operation: opList,
				Package:   pkg,
				Message:   "pagination token loop detected in monetization.subscriptions.list (server repeated a nextPageToken)",
			}
		}
		seen[pg.NextPageToken] = struct{}{}
		token = pg.NextPageToken
	}
}

// Create creates a subscription from the catalog-file resource, sent verbatim
// as the request body. productId and regionsVersion.version ride as query
// parameters: the API requires the regions version pin for any write that
// carries regional prices (ADR-0041 §7).
func Create(ctx context.Context, hc *http.Client, pkg, productID, regionsVersion string, body json.RawMessage) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("productId", productID)
	q.Set("regionsVersion.version", regionsVersion)
	u, err := mCreate.URL(map[string]string{"packageName": pkg})
	if err != nil {
		return nil, &api.Error{Operation: opCreate, Package: pkg, Message: err.Error(), Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, mCreate.Verb, u+"?"+q.Encode(), strings.NewReader(string(body)))
	if err != nil {
		return nil, &api.Error{Operation: opCreate, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	return do(hc, opCreate, pkg, req)
}

// Patch updates a subscription from the catalog-file resource, sent verbatim,
// with the updateMask scoped to exactly the fields the caller reconciles: the
// mechanism that keeps anything outside the caller's managed set out of reach
// of an apply (ADR-0041 §5).
func Patch(ctx context.Context, hc *http.Client, pkg, productID, regionsVersion string, updateMask []string, body json.RawMessage) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("updateMask", strings.Join(updateMask, ","))
	q.Set("regionsVersion.version", regionsVersion)
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

// Delete deletes a subscription. Reaching here means the plan carried a delete
// and the operator passed --confirm; the API additionally refuses to delete a
// subscription that ever had a published base plan, so the gate covers intent
// while the server covers damage (ADR-0041).
func Delete(ctx context.Context, hc *http.Client, pkg, productID string) error {
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

// ConvertRegionPrices derives per-region prices from one base price using
// today's exchange rates and Google's country-specific pricing patterns
// (monetization.convertRegionPrices): the pricing helper of the Monetization
// catalog (ADR-0041 §9). Read-only in effect: it computes, it never writes
// catalog state. The verbatim response is the ADR-0003 pass-through.
func ConvertRegionPrices(ctx context.Context, hc *http.Client, pkg string, price Money) (json.RawMessage, error) {
	body, err := json.Marshal(struct {
		Price Money `json:"price"`
	}{Price: price})
	if err != nil {
		return nil, &api.Error{Operation: opConvert, Package: pkg, Message: "encode request: " + err.Error(), Cause: err}
	}
	u, err := mConvert.URL(map[string]string{"packageName": pkg})
	if err != nil {
		return nil, &api.Error{Operation: opConvert, Package: pkg, Message: err.Error(), Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, mConvert.Verb, u, strings.NewReader(string(body)))
	if err != nil {
		return nil, &api.Error{Operation: opConvert, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	return do(hc, opConvert, pkg, req)
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
