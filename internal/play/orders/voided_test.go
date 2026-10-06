package orders_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/play/orders"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const voidedPage1 = `{
  "pageInfo": {"totalResults": 3, "resultPerPage": 2},
  "tokenPagination": {"nextPageToken": "p2"},
  "voidedPurchases": [
    {"kind": "androidpublisher#voidedPurchase", "orderId": "GPA.0001", "purchaseToken": "tok1", "purchaseTimeMillis": "1700000000000", "voidedTimeMillis": "1700086400000", "voidedSource": 2, "voidedReason": 7},
    {"kind": "androidpublisher#voidedPurchase", "orderId": "GPA.0002", "purchaseTimeMillis": "1700000000000", "voidedTimeMillis": "1700000100000", "voidedSource": 0, "voidedReason": 1, "futureField": {"x": 1}}
  ]
}`

const voidedPage2 = `{
  "voidedPurchases": [
    {"orderId": "GPA.0003", "voidedQuantity": 2, "voidedSource": 1, "voidedReason": 0}
  ]
}`

func listVoided(t *testing.T, fake *testkit.Fake, opts orders.VoidedListOptions) (orders.VoidedList, bool) {
	t.Helper()
	got, truncated, err := orders.ListVoided(context.Background(), &http.Client{Transport: fake}, "com.example.app", opts)
	if err != nil {
		t.Fatalf("ListVoided: %v", err)
	}
	return got, truncated
}

func query(t *testing.T, c testkit.Call) url.Values {
	t.Helper()
	q, err := url.ParseQuery(c.Query)
	if err != nil {
		t.Fatalf("parse query %q: %v", c.Query, err)
	}
	return q
}

// TestListVoided_queryShape asserts the GET hits the application-scoped
// voidedpurchases endpoint (no Edit) and carries the window, type and
// partial-refund parameters as the API names them.
func TestListVoided_queryShape(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, `{}`))
	start := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	listVoided(t, fake, orders.VoidedListOptions{Start: start, IncludeSubscriptions: true, IncludePartialRefunds: true})

	calls := fake.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	c := calls[0]
	if c.Method != http.MethodGet {
		t.Errorf("method = %q, want GET", c.Method)
	}
	if !strings.HasSuffix(c.Path, "/applications/com.example.app/purchases/voidedpurchases") {
		t.Errorf("path %q is not the voidedpurchases.list endpoint", c.Path)
	}
	q := query(t, c)
	// 2026-09-06T12:00:00Z in epoch millis, computed independently.
	if got := q.Get("startTime"); got != "1788696000000" {
		t.Errorf("startTime = %q, want 1788696000000", got)
	}
	if got := q.Get("type"); got != "1" {
		t.Errorf("type = %q, want 1 (in-app and subscriptions)", got)
	}
	if got := q.Get("includeQuantityBasedPartialRefund"); got != "true" {
		t.Errorf("includeQuantityBasedPartialRefund = %q, want true", got)
	}
	if q.Has("endTime") {
		t.Errorf("endTime must be left to the API default (now): %q", c.Query)
	}
}

// TestListVoided_inappOnly asserts in-app-only sends type=0 and omits the
// partial-refund flag when it is off.
func TestListVoided_inappOnly(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, `{}`))
	listVoided(t, fake, orders.VoidedListOptions{Start: time.Now().Add(-time.Hour)})
	q := query(t, fake.Calls()[0])
	if got := q.Get("type"); got != "0" {
		t.Errorf("type = %q, want 0 (in-app only)", got)
	}
	if q.Has("includeQuantityBasedPartialRefund") {
		t.Errorf("partial-refund flag sent while off: %v", q)
	}
}

// TestListVoided_followsTokenPagination asserts the walk carries
// tokenPagination.nextPageToken back as the `token` parameter, and that the
// merged envelope keeps every item byte-for-byte (unmodeled fields included)
// under the API's voidedPurchases key.
func TestListVoided_followsTokenPagination(t *testing.T) {
	fake := testkit.NewFake(testkit.Sequence(voidedPage1, voidedPage2))
	got, truncated := listVoided(t, fake, orders.VoidedListOptions{Start: time.Now().Add(-time.Hour)})
	if truncated {
		t.Error("an exhausted walk must not report truncation")
	}
	calls := fake.Calls()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	if q := query(t, calls[0]); q.Has("token") {
		t.Errorf("first page must not carry a token: %v", q)
	}
	if got := query(t, calls[1]).Get("token"); got != "p2" {
		t.Errorf("second page token = %q, want p2", got)
	}
	if len(got.Purchases) != 3 || got.Purchases[0].OrderID != "GPA.0001" || got.Purchases[2].OrderID != "GPA.0003" {
		t.Fatalf("purchases = %+v", got.Purchases)
	}
	var env struct {
		VoidedPurchases []json.RawMessage `json:"voidedPurchases"`
	}
	if err := json.Unmarshal(got.Raw, &env); err != nil {
		t.Fatalf("raw envelope: %v (%s)", err, got.Raw)
	}
	if len(env.VoidedPurchases) != 3 {
		t.Fatalf("envelope items = %d, want 3", len(env.VoidedPurchases))
	}
	if !strings.Contains(string(env.VoidedPurchases[1]), `"futureField":{"x":1}`) {
		t.Errorf("item not passed through verbatim: %s", env.VoidedPurchases[1])
	}
}

// TestListVoided_limitCapsAndReportsTruncation asserts a limit stops the walk
// and reports that items were hidden.
func TestListVoided_limitCapsAndReportsTruncation(t *testing.T) {
	fake := testkit.NewFake(testkit.Sequence(voidedPage1, voidedPage2))
	got, truncated := listVoided(t, fake, orders.VoidedListOptions{Start: time.Now().Add(-time.Hour), Limit: 2})
	if !truncated {
		t.Error("a limit that hid a page must report truncation")
	}
	if len(fake.Calls()) != 1 {
		t.Errorf("calls = %d, want 1 (the limit is reached on page 1)", len(fake.Calls()))
	}
	if len(got.Purchases) != 2 {
		t.Errorf("purchases = %d, want 2", len(got.Purchases))
	}
}

// TestListVoided_emptyIsEmptyArray asserts an empty window renders as an empty
// array, never null, so a jq consumer reads the same shape every time.
func TestListVoided_emptyIsEmptyArray(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, `{}`))
	got, _ := listVoided(t, fake, orders.VoidedListOptions{Start: time.Now().Add(-time.Hour)})
	if !strings.Contains(string(got.Raw), `"voidedPurchases":[]`) {
		t.Errorf("raw = %s, want an empty voidedPurchases array", got.Raw)
	}
}

// TestListVoided_403_exit11 asserts a missing financial-data permission maps
// to the authz exit code.
func TestListVoided_403_exit11(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusForbidden, `{"error":{"message":"The caller does not have permission"}}`))
	_, _, err := orders.ListVoided(context.Background(), &http.Client{Transport: fake}, "com.example.app", orders.VoidedListOptions{Start: time.Now()})
	assertExit(t, err, 11)
}
