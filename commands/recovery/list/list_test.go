package list_test

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

	listcmd "github.com/PollyGlot/google-play-cli/commands/recovery/list"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

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

// TestRun_happyPath_sendsVersionCode asserts the required version param + passthrough.
func TestRun_happyPath_sendsVersionCode(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, `{"recoveryActions":[{"appRecoveryId":"1","status":"RECOVERY_STATUS_ACTIVE"}]}`))
	rc := newRC(t, fake)
	r, err := listcmd.Run(rc, listcmd.Input{Package: "com.example.app", VersionCode: 142})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || !strings.Contains(calls[0].URL, "versionCode=142") {
		t.Errorf("calls = %+v, want one GET carrying versionCode=142", calls)
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(out.String(), `"appRecoveryId":"1"`) {
		t.Errorf("json %s should pass the action through", out.String())
	}
}

// TestRun_missingVersionCode_exit2_noNetwork asserts --version-code is required.
func TestRun_missingVersionCode_exit2_noNetwork(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, `{"recoveryActions":[{"appRecoveryId":"1","status":"RECOVERY_STATUS_ACTIVE"}]}`))
	rc := newRC(t, fake)
	_, err := listcmd.Run(rc, listcmd.Input{Package: "com.example.app"})
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) || c.ExitCode() != 2 {
		t.Errorf("err = %v, want exit 2", err)
	}
	if len(fake.Calls()) != 0 || fake.TokenExchanges() != 0 {
		t.Errorf("must not reach the network; calls=%v tokens=%d", fake.Calls(), fake.TokenExchanges())
	}
}
