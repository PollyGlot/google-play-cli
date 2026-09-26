package cancel_test

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

	cancelcmd "github.com/PollyGlot/google-play-cli/commands/recovery/cancel"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func saJSON(t *testing.T) []byte {
	t.Helper()
	key := testkit.RSAKey(t)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, _ := json.Marshal(map[string]any{"type": "service_account", "project_id": "p", "private_key": string(pemBytes), "client_email": "ci@p.iam.gserviceaccount.com", "token_uri": "https://oauth2.googleapis.com/token"})
	return raw
}

func newRC(t *testing.T, transport http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(saJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: transport})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc
}

// TestRun_missingConfirm_exit3_noNetwork asserts cancel's irreversible gate.
func TestRun_missingConfirm_exit3_noNetwork(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, `{}`))
	rc := newRC(t, fake)
	_, err := cancelcmd.Run(rc, cancelcmd.Input{Package: "com.example.app", ID: "555"})
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) || c.ExitCode() != 3 {
		t.Errorf("err = %v, want exit 3", err)
	}
	if len(fake.Calls()) != 0 || fake.TokenExchanges() != 0 {
		t.Errorf("must not reach the network without --confirm; calls=%v tokens=%d", fake.Calls(), fake.TokenExchanges())
	}
}

// TestRun_confirmed_postsCancel asserts the confirmed path POSTs :cancel + ✓.
func TestRun_confirmed_postsCancel(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, `{}`))
	rc := newRC(t, fake)
	var stderr bytes.Buffer
	rc.Stderr = &stderr
	if _, err := cancelcmd.Run(rc, cancelcmd.Input{Package: "com.example.app", ID: "555", Confirm: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Method != http.MethodPost || !strings.HasSuffix(calls[0].URL, "/appRecoveries/555:cancel") {
		t.Fatalf("calls = %+v, want one POST :cancel", calls)
	}
	if !strings.HasPrefix(stderr.String(), "✓ ") {
		t.Errorf("stderr missing ✓:\n%s", stderr.String())
	}
}
