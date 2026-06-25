package view_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	viewcmd "github.com/PollyGlot/google-play-cli/commands/orders/view"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// ordersRT is a RoundTripper that answers the /token exchange and the
// orders.get call, recording the API URL. status/body are configurable so the
// refusal path can return a 403.
type ordersRT struct {
	mu     sync.Mutex
	calls  []string
	getURL string
	status int
	body   string
}

func (r *ordersRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.calls = append(r.calls, "POST /token")
		return jsonResp(200, `{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	}
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	r.getURL = req.URL.String()
	status, body := r.status, r.body
	if status == 0 {
		status, body = 200, orderBody
	}
	return jsonResp(status, body), nil
}

const orderBody = `{
  "orderId": "GPA.1234-5678-9012-34567",
  "state": "PROCESSED",
  "createTime": "2024-01-15T10:30:00Z",
  "total": {"currencyCode": "USD", "units": "4", "nanos": 990000000},
  "lineItems": [{"productId": "com.example.coins", "productTitle": "Coins", "total": {"currencyCode": "USD", "units": "4", "nanos": 990000000}}],
  "buyerAddress": {"country": "US"}
}`

func jsonResp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func signedSAJSON(t *testing.T) []byte {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
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

// TestRun_happyPath_packageScoped_noEdit asserts the GET addresses the order on
// the package axis (no Edit) and passes the response through verbatim.
func TestRun_happyPath_packageScoped_noEdit(t *testing.T) {
	rt := &ordersRT{}
	rc := newRC(t, rt)
	r, err := viewcmd.Run(rc, viewcmd.Input{Package: "com.example.app", OrderID: "GPA.1234-5678-9012-34567"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasSuffix(rt.getURL, "/applications/com.example.app/orders/GPA.1234-5678-9012-34567") {
		t.Errorf("url %q is not the package-scoped orders.get endpoint", rt.getURL)
	}
	if strings.Contains(rt.getURL, "/edits/") {
		t.Errorf("url %q must not open an Edit", rt.getURL)
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

// TestRun_humanSummary renders the table view and asserts the compact summary
// (order id, state, total + currency, create time, line items) appears.
func TestRun_humanSummary(t *testing.T) {
	rt := &ordersRT{}
	rc := newRC(t, rt)
	r, err := viewcmd.Run(rc, viewcmd.Input{Package: "com.example.app", OrderID: "GPA.1234-5678-9012-34567"})
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

// TestRun_missingOrderID_exit2_noNetwork asserts an empty order ID is CLI misuse
// caught before any HTTP call.
func TestRun_missingOrderID_exit2_noNetwork(t *testing.T) {
	rt := &ordersRT{}
	rc := newRC(t, rt)
	_, err := viewcmd.Run(rc, viewcmd.Input{Package: "com.example.app", OrderID: "   "})
	assertExit(t, err, 2)
	if len(rt.calls) != 0 {
		t.Errorf("must not reach the network; calls=%v", rt.calls)
	}
}

// TestRun_403_namesPermission asserts a forbidden read maps to exit 11 and the
// refusal message names CAN_VIEW_FINANCIAL_DATA (agent-resolvable).
func TestRun_403_namesPermission(t *testing.T) {
	rt := &ordersRT{status: 403, body: `{"error":{"message":"The caller does not have permission"}}`}
	rc := newRC(t, rt)
	_, err := viewcmd.Run(rc, viewcmd.Input{Package: "com.example.app", OrderID: "GPA.1"})
	assertExit(t, err, 11)
	if !strings.Contains(err.Error(), "CAN_VIEW_FINANCIAL_DATA") {
		t.Errorf("403 refusal %q must name CAN_VIEW_FINANCIAL_DATA", err.Error())
	}
}

// TestRun_404_exit30 asserts an unknown order id maps to the not-found exit code
// with a hint.
func TestRun_404_exit30(t *testing.T) {
	rt := &ordersRT{status: 404, body: `{"error":{"message":"order not found"}}`}
	rc := newRC(t, rt)
	_, err := viewcmd.Run(rc, viewcmd.Input{Package: "com.example.app", OrderID: "GPA.missing"})
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
