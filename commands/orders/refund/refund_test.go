package refund_test

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

	refundcmd "github.com/PollyGlot/google-play-cli/commands/orders/refund"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// refundRT answers the /token exchange and the orders.refund POST, recording
// the API URL/method. status/body are configurable for the refusal paths;
// otherwise it returns 204 with an empty body (the API's success shape).
type refundRT struct {
	mu     sync.Mutex
	calls  []string
	apiURL string
	method string
	status int
	body   string
}

func (r *refundRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.calls = append(r.calls, "POST /token")
		return jsonResp(200, `{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	}
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	r.apiURL = req.URL.String()
	r.method = req.Method
	if r.status != 0 {
		return jsonResp(r.status, r.body), nil
	}
	return jsonResp(204, ""), nil
}

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

// TestRun_refund_success asserts --confirm POSTs to :refund without a revoke
// query (default: money back, entitlement kept) and emits a parseable success
// object.
func TestRun_refund_success(t *testing.T) {
	rt := &refundRT{}
	rc := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr
	r, err := refundcmd.Run(rc, refundcmd.Input{Package: "com.example.app", OrderID: "GPA.1234", Confirm: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// DESIGN §8: a ✓ confirmation lands on stderr (not stdout, so it never
	// pollutes --output json); the default keeps the entitlement.
	if !strings.HasPrefix(stderr.String(), "✓ ") || !strings.Contains(stderr.String(), "entitlement kept") {
		t.Errorf("stderr missing the ✓ entitlement-kept confirmation:\n%s", stderr.String())
	}
	if rt.method != http.MethodPost {
		t.Errorf("method = %q, want POST", rt.method)
	}
	if !strings.HasSuffix(rt.apiURL, "/applications/com.example.app/orders/GPA.1234:refund") {
		t.Errorf("url %q is not the orders.refund endpoint", rt.apiURL)
	}
	if strings.Contains(rt.apiURL, "revoke") {
		t.Errorf("url %q must not carry revoke by default", rt.apiURL)
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var got struct {
		OK       bool   `json:"ok"`
		Refunded string `json:"refunded"`
		Revoked  bool   `json:"revoked"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("success json %q not parseable: %v", out.String(), err)
	}
	if !got.OK || got.Refunded != "GPA.1234" || got.Revoked {
		t.Errorf("success json = %+v, want ok refunded=GPA.1234 revoked=false", got)
	}
}

// TestRun_refund_revoke asserts --revoke maps to the revoke=true query parameter
// and reports revoked:true.
func TestRun_refund_revoke(t *testing.T) {
	rt := &refundRT{}
	rc := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr
	r, err := refundcmd.Run(rc, refundcmd.Input{Package: "com.example.app", OrderID: "GPA.1234", Confirm: true, Revoke: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(rt.apiURL, "revoke=true") {
		t.Errorf("url %q must carry revoke=true", rt.apiURL)
	}
	// The ✓ confirmation must name the revoked entitlement (distinct from the
	// entitlement-kept default).
	if !strings.HasPrefix(stderr.String(), "✓ ") || !strings.Contains(stderr.String(), "revoked the entitlement") {
		t.Errorf("stderr missing the ✓ revoked-entitlement confirmation:\n%s", stderr.String())
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var got struct {
		Revoked bool `json:"revoked"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("revoke success json %q not parseable: %v", out.String(), err)
	}
	if !got.Revoked {
		t.Errorf("revoke success json %q must report revoked:true", out.String())
	}
}

// TestRun_missingConfirm_exit3_noNetwork asserts a refund without --confirm
// refuses with exit 3 naming the flag, before any HTTP call.
func TestRun_missingConfirm_exit3_noNetwork(t *testing.T) {
	rt := &refundRT{}
	rc := newRC(t, rt)
	_, err := refundcmd.Run(rc, refundcmd.Input{Package: "com.example.app", OrderID: "GPA.1234"})
	assertExit(t, err, 3)
	// The structured Flag is the signal an automated caller branches on, so pin
	// the concrete type and that it names the confirm gate (ADR-0017).
	var sfe *exit.SafetyFlagError
	if !errors.As(err, &sfe) {
		t.Fatalf("err %v is not *exit.SafetyFlagError", err)
	}
	if sfe.Flag != "confirm" {
		t.Errorf("SafetyFlagError.Flag = %q, want confirm", sfe.Flag)
	}
	if !strings.Contains(err.Error(), "--confirm") {
		t.Errorf("exit-3 message %q must name --confirm", err.Error())
	}
	if len(rt.calls) != 0 {
		t.Errorf("must not reach the network; calls=%v", rt.calls)
	}
}

// TestRun_dryRun_requiresConfirm_noNetwork asserts --dry-run previews offline
// and surfaces requires:["confirm"] under --output json.
func TestRun_dryRun_requiresConfirm_noNetwork(t *testing.T) {
	rt := &refundRT{}
	rc := newRC(t, rt)
	r, err := refundcmd.Run(rc, refundcmd.Input{Package: "com.example.app", OrderID: "GPA.1234", DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("dry-run must not reach the network; calls=%v", rt.calls)
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var got struct {
		DryRun   bool     `json:"dryRun"`
		OrderID  string   `json:"orderId"`
		Requires []string `json:"requires"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("dry-run json %q not parseable: %v", out.String(), err)
	}
	if !got.DryRun || got.OrderID != "GPA.1234" || len(got.Requires) != 1 || got.Requires[0] != "confirm" {
		t.Errorf("dry-run json = %+v, want dryRun orderId=GPA.1234 requires=[confirm]", got)
	}
}

// TestRun_403_namesManageOrders asserts a forbidden refund maps to exit 11 and
// the refusal names CAN_MANAGE_ORDERS (agent-resolvable).
func TestRun_403_namesManageOrders(t *testing.T) {
	rt := &refundRT{status: 403, body: `{"error":{"message":"The caller does not have permission"}}`}
	rc := newRC(t, rt)
	_, err := refundcmd.Run(rc, refundcmd.Input{Package: "com.example.app", OrderID: "GPA.1234", Confirm: true})
	assertExit(t, err, 11)
	if !strings.Contains(err.Error(), "CAN_MANAGE_ORDERS") {
		t.Errorf("403 refusal %q must name CAN_MANAGE_ORDERS", err.Error())
	}
}

// TestRun_tooOld_specificRefusal asserts the API's older-than-3-years rejection
// becomes a specific refusal (not a generic error), preserving the API exit
// code (400 → 30). The assertion keys on a phrase that exists ONLY in
// refundTooOldError.Error() (never in the raw API message) so it fails if the
// classifier stops wrapping the too-old case (the raw *api.Error would still
// echo "3 years" and exit 30, which would silently pass a weaker assertion).
func TestRun_tooOld_specificRefusal(t *testing.T) {
	rt := &refundRT{status: 400, body: `{"error":{"message":"Orders older than 3 years cannot be refunded."}}`}
	rc := newRC(t, rt)
	_, err := refundcmd.Run(rc, refundcmd.Input{Package: "com.example.app", OrderID: "GPA.old", Confirm: true})
	assertExit(t, err, 30)
	if !strings.Contains(err.Error(), "hard API limit, not a permission issue") {
		t.Errorf("too-old refusal %q must be the gplay-specific refundTooOldError, not a generic passthrough", err.Error())
	}
}

// TestRun_missingOrderID_exit2_noNetwork asserts an empty order ID is CLI misuse
// caught before any HTTP call (and before the confirm gate).
func TestRun_missingOrderID_exit2_noNetwork(t *testing.T) {
	rt := &refundRT{}
	rc := newRC(t, rt)
	_, err := refundcmd.Run(rc, refundcmd.Input{Package: "com.example.app", OrderID: "  ", Confirm: true})
	assertExit(t, err, 2)
	if len(rt.calls) != 0 {
		t.Errorf("must not reach the network; calls=%v", rt.calls)
	}
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
