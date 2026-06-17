package list_test

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

	listcmd "github.com/PollyGlot/google-play-cli/commands/recovery/list"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

type recRT struct {
	t      *testing.T
	mu     sync.Mutex
	calls  []string
	getURL string
}

func (r *recRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.calls = append(r.calls, "POST /token")
		return jsonResp(`{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	}
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	r.getURL = req.URL.String()
	return jsonResp(`{"recoveryActions":[{"appRecoveryId":"1","status":"RECOVERY_STATUS_ACTIVE"}]}`), nil
}

func jsonResp(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
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

// TestRun_happyPath_sendsVersionCode asserts the required version param + passthrough.
func TestRun_happyPath_sendsVersionCode(t *testing.T) {
	rt := &recRT{t: t}
	rc := newRC(t, rt)
	r, err := listcmd.Run(rc, listcmd.Input{Package: "com.example.app", VersionCode: 142})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(rt.getURL, "versionCode=142") {
		t.Errorf("url %q missing versionCode=142", rt.getURL)
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
	rt := &recRT{t: t}
	rc := newRC(t, rt)
	_, err := listcmd.Run(rc, listcmd.Input{Package: "com.example.app"})
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) || c.ExitCode() != 2 {
		t.Errorf("err = %v, want exit 2", err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("must not reach the network; calls=%v", rt.calls)
	}
}
