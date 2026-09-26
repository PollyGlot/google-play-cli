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

	listcmd "github.com/PollyGlot/google-play-cli/commands/releases/generated/list"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const generatedBody = `{"generatedApks":[{"certificateSha256Hash":"0123456789abcdef","generatedUniversalApk":{"downloadId":"dl-univ"}}]}`

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

// TestRun_happyPath_versionScoped_noEdit asserts the GET addresses the version
// code on the package axis (no Edit) and passes the response through verbatim.
func TestRun_happyPath_versionScoped_noEdit(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, generatedBody))
	rc := newRC(t, fake)
	r, err := listcmd.Run(rc, listcmd.Input{Package: "com.example.app", VersionCode: 142})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	calls := fake.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want the single generatedApks GET", len(calls))
	}
	getURL := calls[0].URL
	if !strings.HasSuffix(getURL, "/applications/com.example.app/generatedApks/142") {
		t.Errorf("url %q is not the version-scoped generatedApks endpoint", getURL)
	}
	if strings.Contains(getURL, "/edits/") {
		t.Errorf("url %q must not open an Edit", getURL)
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(out.String(), `"dl-univ"`) {
		t.Errorf("json %s should pass the downloadId through", out.String())
	}
}

// TestRun_missingVersionCode_exit2_noNetwork asserts --version-code is required.
func TestRun_missingVersionCode_exit2_noNetwork(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, generatedBody))
	rc := newRC(t, fake)
	_, err := listcmd.Run(rc, listcmd.Input{Package: "com.example.app"})
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) || c.ExitCode() != 2 {
		t.Errorf("err = %v, want exit 2", err)
	}
	if n := len(fake.Calls()) + fake.TokenExchanges(); n != 0 {
		t.Errorf("must not reach the network; calls=%v tokens=%d", fake.Calls(), fake.TokenExchanges())
	}
}
