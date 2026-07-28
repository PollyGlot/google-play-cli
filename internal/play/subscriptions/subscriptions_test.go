package subscriptions_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/subscriptions"
)

// subsRT is a RoundTripper that serves scripted responses in order and records
// every request (method, URL, body) for assertions. No network.
type subsRT struct {
	mu        sync.Mutex
	requests  []recordedReq
	responses []scripted
}

type recordedReq struct {
	method string
	url    string
	body   string
}

type scripted struct {
	status int
	body   string
}

func (r *subsRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var body string
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		body = string(b)
	}
	r.requests = append(r.requests, recordedReq{method: req.Method, url: req.URL.String(), body: body})
	if len(r.responses) == 0 {
		return jsonResp(200, `{}`), nil
	}
	next := r.responses[0]
	r.responses = r.responses[1:]
	return jsonResp(next.status, next.body), nil
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func client(rt http.RoundTripper) *http.Client { return &http.Client{Transport: rt} }

// TestList_paginatesToCompletion asserts List follows nextPageToken across
// pages, parses each subscription's productId, and keeps the verbatim resource
// bytes for the pass-through.
func TestList_paginatesToCompletion(t *testing.T) {
	rt := &subsRT{responses: []scripted{
		{200, `{"subscriptions":[{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}]}],"nextPageToken":"p2"}`},
		{200, `{"subscriptions":[{"productId":"pro","listings":[{"languageCode":"en-US","title":"Pro"}]}]}`},
	}}
	items, err := subscriptions.List(context.Background(), client(rt), "com.example.app")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 || items[0].ProductID != "premium" || items[1].ProductID != "pro" {
		t.Fatalf("items = %+v, want premium then pro", items)
	}
	if !strings.Contains(string(items[0].Raw), `"title":"Premium"`) {
		t.Errorf("Raw %s should carry the verbatim resource", items[0].Raw)
	}
	if len(rt.requests) != 2 {
		t.Fatalf("want 2 page requests, got %d", len(rt.requests))
	}
	first, second := rt.requests[0], rt.requests[1]
	if first.method != http.MethodGet || !strings.Contains(first.url, "/applications/com.example.app/subscriptions?") {
		t.Errorf("first request %s %s is not the subscriptions.list endpoint", first.method, first.url)
	}
	if strings.Contains(first.url, "/edits/") {
		t.Errorf("url %q must not open an Edit", first.url)
	}
	if !strings.Contains(second.url, "pageToken=p2") {
		t.Errorf("second request %q must carry the nextPageToken", second.url)
	}
}

// TestList_tokenLoopGuard asserts a server that repeats a nextPageToken forever
// surfaces an error instead of an infinite loop.
func TestList_tokenLoopGuard(t *testing.T) {
	rt := &subsRT{responses: []scripted{
		{200, `{"subscriptions":[{"productId":"a"}],"nextPageToken":"same"}`},
		{200, `{"subscriptions":[{"productId":"b"}],"nextPageToken":"same"}`},
		{200, `{"subscriptions":[{"productId":"c"}],"nextPageToken":"same"}`},
	}}
	_, err := subscriptions.List(context.Background(), client(rt), "com.example.app")
	if err == nil {
		t.Fatal("want token-loop error, got nil")
	}
	if !strings.Contains(err.Error(), "loop") {
		t.Errorf("error %q should name the pagination loop", err)
	}
}

// TestList_emptyProductID_refuses asserts a malformed resource without a
// productId is rejected at the API boundary instead of becoming an
// unaddressable catalog entry.
func TestList_emptyProductID_refuses(t *testing.T) {
	rt := &subsRT{responses: []scripted{
		{200, `{"subscriptions":[{"listings":[{"languageCode":"en-US","title":"No ID"}]}]}`},
	}}
	_, err := subscriptions.List(context.Background(), client(rt), "com.example.app")
	if err == nil || !strings.Contains(err.Error(), "without a productId") {
		t.Fatalf("err = %v, want the missing-productId refusal", err)
	}
}

// TestCreate_postsResourceWithRegionsVersion asserts Create POSTs the file
// resource verbatim with productId and regionsVersion.version as query params.
func TestCreate_postsResourceWithRegionsVersion(t *testing.T) {
	rt := &subsRT{responses: []scripted{{200, `{"productId":"premium"}`}}}
	body := json.RawMessage(`{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}]}`)
	raw, err := subscriptions.Create(context.Background(), client(rt), "com.example.app", "premium", "2022/02", body)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(rt.requests) != 1 {
		t.Fatalf("want 1 request, got %d", len(rt.requests))
	}
	req := rt.requests[0]
	if req.method != http.MethodPost || !strings.Contains(req.url, "/applications/com.example.app/subscriptions?") {
		t.Errorf("request %s %s is not subscriptions.create", req.method, req.url)
	}
	if !strings.Contains(req.url, "productId=premium") {
		t.Errorf("url %q must carry productId", req.url)
	}
	if !strings.Contains(req.url, "regionsVersion.version=2022%2F02") {
		t.Errorf("url %q must carry regionsVersion.version", req.url)
	}
	if req.body != string(body) {
		t.Errorf("body %q must be the resource verbatim", req.body)
	}
	if !strings.Contains(string(raw), "premium") {
		t.Errorf("response %s should be returned verbatim", raw)
	}
}

// TestPatch_scopedUpdateMask asserts Patch PATCHes the addressed subscription
// with an updateMask restricted to the fields the walking skeleton manages, so
// unmanaged nesting (basePlans) can never be clobbered.
func TestPatch_scopedUpdateMask(t *testing.T) {
	rt := &subsRT{responses: []scripted{{200, `{"productId":"premium"}`}}}
	body := json.RawMessage(`{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium+"}]}`)
	_, err := subscriptions.Patch(context.Background(), client(rt), "com.example.app", "premium", "2022/02", []string{"listings", "taxAndComplianceSettings"}, body)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	req := rt.requests[0]
	if req.method != http.MethodPatch || !strings.Contains(req.url, "/applications/com.example.app/subscriptions/premium?") {
		t.Errorf("request %s %s is not subscriptions.patch", req.method, req.url)
	}
	if !strings.Contains(req.url, "updateMask=listings%2CtaxAndComplianceSettings") {
		t.Errorf("url %q must scope the updateMask", req.url)
	}
	if strings.Contains(req.url, "basePlans") {
		t.Errorf("url %q must not include unmanaged fields in the mask", req.url)
	}
	if !strings.Contains(req.url, "regionsVersion.version=2022%2F02") {
		t.Errorf("url %q must carry regionsVersion.version", req.url)
	}
}

// TestDelete_addressesProduct asserts Delete issues the DELETE and treats an
// empty 2xx as success.
func TestDelete_addressesProduct(t *testing.T) {
	rt := &subsRT{responses: []scripted{{204, ``}}}
	if err := subscriptions.Delete(context.Background(), client(rt), "com.example.app", "legacy"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	req := rt.requests[0]
	if req.method != http.MethodDelete || !strings.HasSuffix(req.url, "/applications/com.example.app/subscriptions/legacy") {
		t.Errorf("request %s %s is not subscriptions.delete", req.method, req.url)
	}
}

// TestErrorEnvelope_apiError asserts a non-2xx maps to *api.Error carrying the
// status and the operation, so commands can classify it.
func TestErrorEnvelope_apiError(t *testing.T) {
	rt := &subsRT{responses: []scripted{{403, `{"error":{"message":"The caller does not have permission"}}`}}}
	_, err := subscriptions.List(context.Background(), client(rt), "com.example.app")
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err %v is not *api.Error", err)
	}
	if apiErr.StatusCode != 403 || apiErr.Operation != "monetization.subscriptions.list" {
		t.Errorf("apiErr = {status:%d op:%s}, want 403 monetization.subscriptions.list", apiErr.StatusCode, apiErr.Operation)
	}
}
