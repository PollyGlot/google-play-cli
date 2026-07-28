// Package subscriptions calls the Android Publisher monetization.subscriptions
// endpoints — the subscription level of the Monetization catalog (CONTEXT.md).
// These are Edit-free, application-scoped calls on the package axis
// (applications/{packageName}/subscriptions...), like deviceTierConfigs and
// orders. Raw HTTP (ADR-0007), never the google-go-sdk.
//
// This package ships the walking-skeleton surface of PRD #51 (slice #367): list
// (followed to completion for reconciliation), create, patch (with a caller-
// scoped updateMask so unmanaged nesting is never clobbered) and delete. The
// nested basePlans/offers levels arrive with later slices (#368–#370). See
// ADR-0041.
package subscriptions

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

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
// nextPageToken to completion (no silent truncation — a missing page would read
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
		u := base(pkg) + "?" + q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
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
				return nil, &api.Error{Operation: opList, Package: pkg, Message: "response contains a subscription without a productId — refusing a catalog entry that cannot be addressed"}
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
// parameters — the API requires the regions version pin for any write that
// carries regional prices (ADR-0041 §6).
func Create(ctx context.Context, hc *http.Client, pkg, productID, regionsVersion string, body json.RawMessage) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("productId", productID)
	q.Set("regionsVersion.version", regionsVersion)
	u := base(pkg) + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(string(body)))
	if err != nil {
		return nil, &api.Error{Operation: opCreate, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	return do(hc, opCreate, pkg, req)
}

// Patch updates a subscription from the catalog-file resource, sent verbatim,
// with the updateMask scoped to exactly the fields the caller reconciles — the
// mechanism that keeps unmanaged nesting (basePlans, until slice #368) out of
// reach of an apply (ADR-0041 §5).
func Patch(ctx context.Context, hc *http.Client, pkg, productID, regionsVersion string, updateMask []string, body json.RawMessage) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("updateMask", strings.Join(updateMask, ","))
	q.Set("regionsVersion.version", regionsVersion)
	u := base(pkg) + "/" + url.PathEscape(productID) + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, u, strings.NewReader(string(body)))
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
	u := base(pkg) + "/" + url.PathEscape(productID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return &api.Error{Operation: opDelete, Package: pkg, Message: err.Error(), Cause: err}
	}
	_, err = do(hc, opDelete, pkg, req)
	return err
}

// ConvertRegionPrices derives per-region prices from one base price using
// today's exchange rates and Google's country-specific pricing patterns
// (monetization.convertRegionPrices) — the pricing helper of the Monetization
// catalog (ADR-0041 §8). Read-only in effect: it computes, it never writes
// catalog state. The verbatim response is the ADR-0003 pass-through.
func ConvertRegionPrices(ctx context.Context, hc *http.Client, pkg string, price Money) (json.RawMessage, error) {
	body, err := json.Marshal(struct {
		Price Money `json:"price"`
	}{Price: price})
	if err != nil {
		return nil, &api.Error{Operation: opConvert, Package: pkg, Message: "encode request: " + err.Error(), Cause: err}
	}
	u := api.AndroidPubBase + "/applications/" + url.PathEscape(pkg) + "/pricing:convertRegionPrices"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(string(body)))
	if err != nil {
		return nil, &api.Error{Operation: opConvert, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	return do(hc, opConvert, pkg, req)
}

// base is the application-scoped subscriptions collection URL.
func base(pkg string) string {
	return api.AndroidPubBase + "/applications/" + url.PathEscape(pkg) + "/subscriptions"
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
