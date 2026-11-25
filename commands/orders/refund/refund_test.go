package refund_test

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

	refundcmd "github.com/PollyGlot/google-play-cli/commands/orders/refund"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func newFake(status int, body string) *testkit.Fake {
	return testkit.NewFake(testkit.Any(status, body))
}

func lastURL(t *testing.T, fake *testkit.Fake) string {
	t.Helper()
	calls := fake.Calls()
	if len(calls) == 0 {
		t.Fatal("no API call recorded")
	}
	return calls[len(calls)-1].URL
}

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

func TestRun_refund_success(t *testing.T) {
	fake := newFake(http.StatusNoContent, "")
	rc := newRC(t, fake)
	var stderr bytes.Buffer
	rc.Stderr = &stderr
	r, err := refundcmd.Run(rc, refundcmd.Input{Package: "com.example.app", OrderID: "GPA.1234", Confirm: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasPrefix(stderr.String(), "✓ ") || !strings.Contains(stderr.String(), "entitlement kept") {
		t.Errorf("stderr missing the ✓ entitlement-kept confirmation:\n%s", stderr.String())
	}
	calls := fake.Calls()
	if len(calls) == 0 {
		t.Fatal("no API call recorded")
	}
	apiURL := calls[len(calls)-1].URL
	if m := calls[len(calls)-1].Method; m != http.MethodPost {
		t.Errorf("method = %q, want POST", m)
	}
	if !strings.HasSuffix(apiURL, "/applications/com.example.app/orders/GPA.1234:refund") {
		t.Errorf("url %q is not the orders.refund endpoint", apiURL)
	}
	if strings.Contains(apiURL, "revoke") {
		t.Errorf("url %q must not carry revoke by default", apiURL)
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

func TestRun_refund_revoke(t *testing.T) {
	fake := newFake(http.StatusNoContent, "")
	rc := newRC(t, fake)
	var stderr bytes.Buffer
	rc.Stderr = &stderr
	r, err := refundcmd.Run(rc, refundcmd.Input{Package: "com.example.app", OrderID: "GPA.1234", Confirm: true, Revoke: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if apiURL := lastURL(t, fake); !strings.Contains(apiURL, "revoke=true") {
		t.Errorf("url %q must carry revoke=true", apiURL)
	}
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

func TestRun_missingConfirm_exit3_noNetwork(t *testing.T) {
	fake := newFake(http.StatusNoContent, "")
	rc := newRC(t, fake)
	_, err := refundcmd.Run(rc, refundcmd.Input{Package: "com.example.app", OrderID: "GPA.1234"})
	assertExit(t, err, 3)
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
	if len(fake.Calls()) != 0 || fake.TokenExchanges() != 0 {
		t.Errorf("must not reach the network; calls=%v tokens=%d", fake.Calls(), fake.TokenExchanges())
	}
}

func TestRun_dryRun_requiresConfirm_noNetwork(t *testing.T) {
	fake := newFake(http.StatusNoContent, "")
	rc := newRC(t, fake)
	r, err := refundcmd.Run(rc, refundcmd.Input{Package: "com.example.app", OrderID: "GPA.1234", DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fake.Calls()) != 0 || fake.TokenExchanges() != 0 {
		t.Errorf("dry-run must not reach the network; calls=%v tokens=%d", fake.Calls(), fake.TokenExchanges())
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

func TestRun_403_namesManageOrders(t *testing.T) {
	fake := newFake(403, `{"error":{"message":"The caller does not have permission"}}`)
	rc := newRC(t, fake)
	_, err := refundcmd.Run(rc, refundcmd.Input{Package: "com.example.app", OrderID: "GPA.1234", Confirm: true})
	assertExit(t, err, 11)
	if !strings.Contains(err.Error(), "CAN_MANAGE_ORDERS") {
		t.Errorf("403 refusal %q must name CAN_MANAGE_ORDERS", err.Error())
	}
}

func TestRun_tooOld_specificRefusal(t *testing.T) {
	fake := newFake(400, `{"error":{"message":"Orders older than 3 years cannot be refunded."}}`)
	rc := newRC(t, fake)
	_, err := refundcmd.Run(rc, refundcmd.Input{Package: "com.example.app", OrderID: "GPA.old", Confirm: true})
	assertExit(t, err, 30)
	if !strings.Contains(err.Error(), "hard API limit, not a permission issue") {
		t.Errorf("too-old refusal %q must be the gplay-specific refundTooOldError, not a generic passthrough", err.Error())
	}
}

func TestRun_missingOrderID_exit2_noNetwork(t *testing.T) {
	fake := newFake(http.StatusNoContent, "")
	rc := newRC(t, fake)
	_, err := refundcmd.Run(rc, refundcmd.Input{Package: "com.example.app", OrderID: "  ", Confirm: true})
	assertExit(t, err, 2)
	if len(fake.Calls()) != 0 || fake.TokenExchanges() != 0 {
		t.Errorf("must not reach the network; calls=%v tokens=%d", fake.Calls(), fake.TokenExchanges())
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

func TestRun_emptyBody_printsAck_bodyPassesThrough(t *testing.T) {
	for body, want := range map[string]string{
		"":   "{\n  \"ok\": true,\n  \"refunded\": \"GPA.1234\",\n  \"revoked\": false\n}\n",
		"{}": "{}",
	} {
		rc := newRC(t, newFake(http.StatusOK, body))
		res, err := refundcmd.Run(rc, refundcmd.Input{Package: "com.example.app", OrderID: "GPA.1234", Confirm: true})
		if err != nil {
			t.Fatalf("Run with body %q: %v", body, err)
		}
		var out bytes.Buffer
		if err := res.Renderers().JSON(&out); err != nil {
			t.Fatalf("JSON: %v", err)
		}
		if out.String() != want {
			t.Errorf("body %q: json = %q, want %q", body, out.String(), want)
		}
	}
}
