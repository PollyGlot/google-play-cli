// Package accessibleapps talks to the Play Developer Reporting API's
// `apps.search` method (`playdeveloperreporting.apps.search`) — the
// server-authoritative enumeration of the Apps the calling credential can
// access, backing `gplay apps accessible list` (#347, ADR-0039).
//
// Unlike internal/apps/registry — gplay's LOCAL, chosen working set of
// packages — this is the server's answer to "which Apps can this
// credential see?". The two intentionally do not coincide (ADR-0039): a
// service account may hold androidpublisher rights on an App without the
// Reporting access that surfaces it here, and may see hundreds of org Apps
// it does not drive. This surface is for bootstrap discovery, not registry
// reconciliation.
//
// Like internal/play/vitals this is the Play Developer Reporting service: a
// distinct host (api.ReportingBase) and OAuth scope (token.ReportingScope,
// wired via kernel.WithScope on the command). Read-only.
package accessibleapps

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// opSearch is the native RPC id, used as the Operation on any *api.Error so
// the shared classifier maps 403 → exit 11, 5xx → exit 40, etc.
const opSearch = "playdeveloperreporting.apps.search"

// App is one entry of a SearchAccessibleApps response
// (GooglePlayDeveloperReportingV1beta1App). Fields are decoded verbatim
// from the API shape; --output json still emits the raw body (ADR-0003),
// so this typed view feeds only the table/markdown renderers.
type App struct {
	// Name is the resource name, format `apps/{app}`.
	Name string `json:"name,omitempty"`
	// PackageName is the Android package, e.g. `com.example.app123` — the
	// value an operator feeds straight into `gplay apps add`.
	PackageName string `json:"packageName,omitempty"`
	// DisplayName is the latest Play Console title; may not yet match the
	// public Play Store listing (per the Discovery description).
	DisplayName string `json:"displayName,omitempty"`
}

// SearchResponse is the parsed SearchAccessibleAppsResponse: the Apps of
// one page plus the continuation token to pass back as --page-token.
type SearchResponse struct {
	Apps          []App  `json:"apps,omitempty"`
	NextPageToken string `json:"nextPageToken,omitempty"`
}

// Search issues a single `apps:search` GET and returns the parsed page, the
// verbatim response body (for the --output json pass-through, ADR-0003),
// and any error. Pagination is caller-driven — one page per call, the
// device-tiers/games convention (#347): pass pageToken from a previous
// response's NextPageToken to fetch the next page. pageSize <= 0 lets the
// server apply its default (50; max 1000).
func Search(ctx context.Context, hc *http.Client, pageSize int, pageToken string) (SearchResponse, json.RawMessage, error) {
	u := api.ReportingBase + "/apps:search"
	q := url.Values{}
	if pageSize > 0 {
		q.Set("pageSize", strconv.Itoa(pageSize))
	}
	if pageToken != "" {
		q.Set("pageToken", pageToken)
	}
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return SearchResponse{}, nil, &api.Error{Operation: opSearch, Message: err.Error(), Cause: err}
	}
	raw, err := do(hc, req)
	if err != nil {
		return SearchResponse{}, nil, err
	}
	var sr SearchResponse
	if err := json.Unmarshal(raw, &sr); err != nil {
		return SearchResponse{}, nil, &api.Error{Operation: opSearch, Message: "decode response: " + err.Error(), Cause: err}
	}
	return sr, raw, nil
}

// do performs the request, mapping any non-2xx to an *api.Error carrying
// the status so exit.For dispatches it through the shared taxonomy. It
// mirrors the helper in internal/play/devicetiers; the Package field is
// left empty because apps.search is account-scoped, not app-scoped.
func do(hc *http.Client, req *http.Request) (json.RawMessage, error) {
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &api.Error{Operation: opSearch, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return nil, &api.Error{Operation: opSearch, StatusCode: resp.StatusCode, Message: msg, Reasons: reasons}
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if readErr != nil {
		return nil, &api.Error{Operation: opSearch, StatusCode: resp.StatusCode, Message: "read response body: " + readErr.Error(), Cause: readErr}
	}
	return json.RawMessage(raw), nil
}
