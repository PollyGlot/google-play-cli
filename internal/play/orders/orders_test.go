package orders_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/orders"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func resp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

const orderBody = `{
  "orderId": "GPA.1234-5678-9012-34567",
  "state": "PROCESSED",
  "createTime": "2024-01-15T10:30:00Z",
  "total": {"currencyCode": "USD", "units": "4", "nanos": 990000000},
  "lineItems": [
    {"productId": "com.example.coins", "productTitle": "Coins", "total": {"currencyCode": "USD", "units": "4", "nanos": 990000000}}
  ],
  "buyerAddress": {"country": "US"}
}`

// TestGet_noEdit_applicationScoped asserts the GET addresses the order on the
// package axis (no /edits/) and parses the summary fields.
func TestGet_noEdit_applicationScoped(t *testing.T) {
	var gotURL, gotMethod string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		gotMethod = r.Method
		return resp(200, orderBody), nil
	})
	hc := &http.Client{Transport: rt}

	o, raw, err := orders.Get(context.Background(), hc, "com.example.app", "GPA.1234-5678-9012-34567")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if !strings.HasSuffix(gotURL, "/applications/com.example.app/orders/GPA.1234-5678-9012-34567") {
		t.Errorf("url %q is not the application-scoped orders.get endpoint", gotURL)
	}
	if strings.Contains(gotURL, "/edits/") {
		t.Errorf("url %q must not open an Edit", gotURL)
	}
	if o.OrderID != "GPA.1234-5678-9012-34567" {
		t.Errorf("orderId = %q", o.OrderID)
	}
	if o.State != "PROCESSED" {
		t.Errorf("state = %q", o.State)
	}
	if o.Total == nil || o.Total.CurrencyCode != "USD" || o.Total.Units != "4" || o.Total.Nanos != 990000000 {
		t.Errorf("total = %+v", o.Total)
	}
	if len(o.LineItems) != 1 || o.LineItems[0].ProductID != "com.example.coins" {
		t.Errorf("line items = %+v", o.LineItems)
	}
	// ADR-0003: raw body is the verbatim response, including fields the typed
	// Order struct does not model (buyerAddress).
	if !strings.Contains(string(raw), `"buyerAddress"`) {
		t.Errorf("raw passthrough dropped fields: %s", raw)
	}
}

// TestGet_orderIdPathEscaped asserts an order ID is path-escaped (order IDs are
// dot/dash, but escaping guards any stray reserved char without corrupting the
// path segment).
func TestGet_orderIdPathEscaped(t *testing.T) {
	var gotURL string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		return resp(200, orderBody), nil
	})
	if _, _, err := orders.Get(context.Background(), &http.Client{Transport: rt}, "com.example.app", "GPA.1234 5678"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if strings.Contains(gotURL, "GPA.1234 5678") {
		t.Errorf("url %q did not escape the order id", gotURL)
	}
	if !strings.Contains(gotURL, "GPA.1234%205678") {
		t.Errorf("url %q missing the escaped order id", gotURL)
	}
}

// TestGet_403_exit11 asserts a forbidden response (missing
// CAN_VIEW_FINANCIAL_DATA) maps to the authz exit code.
func TestGet_403_exit11(t *testing.T) {
	rt := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return resp(403, `{"error":{"message":"The caller does not have permission"}}`), nil
	})
	_, _, err := orders.Get(context.Background(), &http.Client{Transport: rt}, "com.example.app", "GPA.1")
	assertExit(t, err, 11)
}

// TestGet_404_exit30 asserts an unknown order id maps to the not-found exit code.
func TestGet_404_exit30(t *testing.T) {
	rt := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return resp(404, `{"error":{"message":"order not found"}}`), nil
	})
	_, _, err := orders.Get(context.Background(), &http.Client{Transport: rt}, "com.example.app", "GPA.missing")
	assertExit(t, err, 30)
}

func assertExit(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error with exit %d, got nil", want)
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err %v is not *api.Error", err)
	}
	if got := apiErr.ExitCode(); got != want {
		t.Errorf("exit = %d, want %d", got, want)
	}
}
