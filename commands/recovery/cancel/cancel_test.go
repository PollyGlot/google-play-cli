package cancel_test

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

	cancelcmd "github.com/PollyGlot/google-play-cli/commands/recovery/cancel"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

type rt struct {
	t       *testing.T
	mu      sync.Mutex
	calls   []string
	postURL string
}

func (r *rt) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.calls = append(r.calls, "POST /token")
		return jsonResp(`{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	}
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	r.postURL = req.URL.String()
	return jsonResp(`{}`), nil
}

func jsonResp(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func saJSON(t *testing.T) []byte {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
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
	r := &rt{t: t}
	rc := newRC(t, r)
	_, err := cancelcmd.Run(rc, cancelcmd.Input{Package: "com.example.app", ID: "555"})
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) || c.ExitCode() != 3 {
		t.Errorf("err = %v, want exit 3", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("must not reach the network without --confirm; calls=%v", r.calls)
	}
}

// TestRun_confirmed_postsCancel asserts the confirmed path POSTs :cancel + ✓.
func TestRun_confirmed_postsCancel(t *testing.T) {
	r := &rt{t: t}
	rc := newRC(t, r)
	var stderr bytes.Buffer
	rc.Stderr = &stderr
	if _, err := cancelcmd.Run(rc, cancelcmd.Input{Package: "com.example.app", ID: "555", Confirm: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasSuffix(r.postURL, "/appRecoveries/555:cancel") {
		t.Errorf("url %q should POST :cancel", r.postURL)
	}
	if !strings.HasPrefix(stderr.String(), "✓ ") {
		t.Errorf("stderr missing ✓:\n%s", stderr.String())
	}
}
