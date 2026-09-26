package view_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	viewcmd "github.com/PollyGlot/google-play-cli/commands/orders/view"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// newFake answers the orders.get / orders.batchget calls. A non-zero status
// serves body for the refusal path (403/404); otherwise it serves the batch
// body for a :batchGet path and the single order body for a get.
func newFake(status int, body string) *testkit.Fake {
	if status != 0 {
		return testkit.NewFake(testkit.Any(status, body))
	}
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		if strings.Contains(c.Path, ":batchGet") {
			return http.StatusOK, batchBody, true
		}
		return http.StatusOK, orderBody, true
	})
}

// lastURL is the URL of the last API call, failing the test when none was made.
func lastURL(t *testing.T, fake *testkit.Fake) string {
	t.Helper()
	calls := fake.Calls()
	if len(calls) == 0 {
		t.Fatal("no API call recorded")
	}
	return calls[len(calls)-1].URL
}

const orderBody = `{
  "orderId": "GPA.1234-5678-9012-34567",
  "state": "PROCESSED",
  "createTime": "2024-01-15T10:30:00Z",
  "total": {"currencyCode": "USD", "units": "4", "nanos": 990000000},
  "lineItems": [{"productId": "com.example.coins", "productTitle": "Coins", "total": {"currencyCode": "USD", "units": "4", "nanos": 990000000}}],
  "buyerAddress": {"country": "US"}
}`

const batchBody = `{
  "orders": [
    {"orderId": "GPA.0001", "state": "PROCESSED", "total": {"currencyCode": "USD", "units": "1", "nanos": 0}, "createTime": "2024-01-01T00:00:00Z"},
    {"orderId": "GPA.0002", "state": "REFUNDED", "total": {"currencyCode": "USD", "units": "2", "nanos": 500000000}, "createTime": "2024-02-02T00:00:00Z"}
  ],
  "nextPageToken": "ignored-but-verbatim"
}`

func signedSAJSON(t *testing.T) []byte {
	t.Helper()
	key := testkit.RSAKey(t)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, _ := json.Marshal(map[string]any{"type": "service_account", "project_id": "p", "private_key": string(pemBytes), "client_email": "ci@p.iam.gserviceaccount.com", "token_uri": "https://oauth2.googleapis.com/token"})
	return raw
}

func newRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc
}

// TestRun_happyPath_packageScoped_noEdit asserts a single ID hits orders.get on
// the package axis (no Edit) and passes the response through verbatim.
func TestRun_happyPath_packageScoped_noEdit(t *testing.T) {
	fake := newFake(0, "")
	rc := newRC(t, fake)
	r, err := viewcmd.Run(rc, viewcmd.Input{Package: "com.example.app", OrderIDs: []string{"GPA.1234-5678-9012-34567"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	apiURL := lastURL(t, fake)
	if !strings.HasSuffix(apiURL, "/applications/com.example.app/orders/GPA.1234-5678-9012-34567") {
		t.Errorf("url %q is not the package-scoped orders.get endpoint", apiURL)
	}
	if strings.Contains(apiURL, "/edits/") {
		t.Errorf("url %q must not open an Edit", apiURL)
	}
	// ADR-0003: --output json is the verbatim Order, including fields the typed
	// summary drops (buyerAddress).
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(out.String(), `"buyerAddress"`) {
		t.Errorf("json %s should pass the order through verbatim", out.String())
	}
}

// TestRun_humanSummary renders the single table view and asserts the compact
// summary (order id, state, total + currency, create time, line items) appears.
func TestRun_humanSummary(t *testing.T) {
	fake := newFake(0, "")
	rc := newRC(t, fake)
	r, err := viewcmd.Run(rc, viewcmd.Input{Package: "com.example.app", OrderIDs: []string{"GPA.1234-5678-9012-34567"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var out bytes.Buffer
	if err := r.Renderers().Table(&out); err != nil {
		t.Fatalf("Table: %v", err)
	}
	got := out.String()
	for _, want := range []string{"GPA.1234-5678-9012-34567", "PROCESSED", "4.99 USD", "2024-01-15T10:30:00Z", "com.example.coins (Coins)"} {
		if !strings.Contains(got, want) {
			t.Errorf("table %q missing %q", got, want)
		}
	}
}

// TestRun_batch_routesToBatchGet asserts two IDs hit orders.batchget (the
// :batchGet endpoint with one orderIds param each), render one summary line per
// order, and pass the BatchGetOrdersResponse through verbatim.
func TestRun_batch_routesToBatchGet(t *testing.T) {
	fake := newFake(0, "")
	rc := newRC(t, fake)
	r, err := viewcmd.Run(rc, viewcmd.Input{Package: "com.example.app", OrderIDs: []string{"GPA.0001", "GPA.0002"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	apiURL := lastURL(t, fake)
	if !strings.Contains(apiURL, "/applications/com.example.app/orders:batchGet?") {
		t.Errorf("url %q is not the orders.batchget endpoint", apiURL)
	}
	if c := strings.Count(apiURL, "orderIds="); c != 2 {
		t.Errorf("url %q must carry one orderIds param per ID, got %d", apiURL, c)
	}
	// Human view: one summary line per order.
	var tbl bytes.Buffer
	if err := r.Renderers().Table(&tbl); err != nil {
		t.Fatalf("Table: %v", err)
	}
	got := tbl.String()
	for _, want := range []string{"GPA.0001", "PROCESSED", "GPA.0002", "REFUNDED"} {
		if !strings.Contains(got, want) {
			t.Errorf("batch table %q missing %q", got, want)
		}
	}
	if lines := strings.Count(strings.TrimSpace(got), "\n"); lines != 1 { // 2 orders → 2 lines → 1 separator
		t.Errorf("batch table should print one line per order, got %d separators in %q", lines, got)
	}
	// ADR-0003: --output json is the verbatim BatchGetOrdersResponse.
	var js bytes.Buffer
	if err := r.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(js.String(), "nextPageToken") {
		t.Errorf("json %s should pass the batch envelope through verbatim", js.String())
	}
}

// TestRun_missingOrderID_exit2_noNetwork asserts a whitespace-only order ID is
// CLI misuse caught before any HTTP call.
func TestRun_missingOrderID_exit2_noNetwork(t *testing.T) {
	fake := newFake(0, "")
	rc := newRC(t, fake)
	_, err := viewcmd.Run(rc, viewcmd.Input{Package: "com.example.app", OrderIDs: []string{"   "}})
	assertExit(t, err, 2)
	if len(fake.Calls()) != 0 || fake.TokenExchanges() != 0 {
		t.Errorf("must not reach the network; calls=%v tokens=%d", fake.Calls(), fake.TokenExchanges())
	}
}

// TestRun_capExceeded_exit2_noNetwork asserts more than 1000 IDs is a usage
// error naming the cap, caught before any HTTP call.
func TestRun_capExceeded_exit2_noNetwork(t *testing.T) {
	fake := newFake(0, "")
	rc := newRC(t, fake)
	ids := make([]string, 1001)
	for i := range ids {
		ids[i] = "GPA." + strings.Repeat("x", 4)
	}
	_, err := viewcmd.Run(rc, viewcmd.Input{Package: "com.example.app", OrderIDs: ids})
	assertExit(t, err, 2)
	if !strings.Contains(err.Error(), "1000") {
		t.Errorf("cap error %q must name the 1–1000 limit", err.Error())
	}
	if len(fake.Calls()) != 0 || fake.TokenExchanges() != 0 {
		t.Errorf("must not reach the network; calls=%v tokens=%d", fake.Calls(), fake.TokenExchanges())
	}
}

// TestRun_403_namesPermission asserts a forbidden read maps to exit 11 and the
// refusal message names CAN_VIEW_FINANCIAL_DATA (agent-resolvable).
func TestRun_403_namesPermission(t *testing.T) {
	fake := newFake(403, `{"error":{"message":"The caller does not have permission"}}`)
	rc := newRC(t, fake)
	_, err := viewcmd.Run(rc, viewcmd.Input{Package: "com.example.app", OrderIDs: []string{"GPA.1"}})
	assertExit(t, err, 11)
	if !strings.Contains(err.Error(), "CAN_VIEW_FINANCIAL_DATA") {
		t.Errorf("403 refusal %q must name CAN_VIEW_FINANCIAL_DATA", err.Error())
	}
}

// TestRun_batch_404_exit30 asserts a batch 404 (any unknown ID fails the whole
// request) maps to the not-found exit code with the all-or-nothing hint.
func TestRun_batch_404_exit30(t *testing.T) {
	fake := newFake(404, `{"error":{"message":"order not found"}}`)
	rc := newRC(t, fake)
	_, err := viewcmd.Run(rc, viewcmd.Input{Package: "com.example.app", OrderIDs: []string{"GPA.1", "GPA.missing"}})
	assertExit(t, err, 30)
	if !strings.Contains(err.Error(), "batchget") {
		t.Errorf("batch 404 hint %q should explain the all-or-nothing semantics", err.Error())
	}
}

// TestRun_404_exit30 asserts an unknown single order id maps to the not-found
// exit code with a hint.
func TestRun_404_exit30(t *testing.T) {
	fake := newFake(404, `{"error":{"message":"order not found"}}`)
	rc := newRC(t, fake)
	_, err := viewcmd.Run(rc, viewcmd.Input{Package: "com.example.app", OrderIDs: []string{"GPA.missing"}})
	assertExit(t, err, 30)
}

func assertExit(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error with exit %d, got nil", want)
	}
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) {
		t.Fatalf("err %v has no ExitCode", err)
	}
	if got := c.ExitCode(); got != want {
		t.Errorf("exit = %d, want %d", got, want)
	}
}
