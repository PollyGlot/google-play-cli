package addtargeting_test

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

	addtargeting "github.com/PollyGlot/google-play-cli/commands/recovery/add-targeting"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

type rt struct {
	t       *testing.T
	mu      sync.Mutex
	calls   []string
	postURL string
	body    []byte
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
	r.body, _ = io.ReadAll(req.Body)
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

func exitOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) {
		t.Fatalf("err = %v (%T), want ExitCode()", err, err)
	}
	return c.ExitCode()
}

// TestRun_missingSelector_exit2 asserts a targeting selector is required, offline.
func TestRun_missingSelector_exit2(t *testing.T) {
	r := &rt{t: t}
	rc := newRC(t, r)
	_, err := addtargeting.Run(rc, addtargeting.Input{Package: "com.example.app", ID: "555", Confirm: true})
	if got := exitOf(t, err); got != 2 {
		t.Errorf("exit = %d, want 2; err=%v", got, err)
	}
	if len(r.calls) != 0 {
		t.Errorf("must not reach the network; calls=%v", r.calls)
	}
}

// TestRun_missingConfirm_exit3 asserts the destructive gate fires (with a valid
// selector) before any HTTP.
func TestRun_missingConfirm_exit3(t *testing.T) {
	r := &rt{t: t}
	rc := newRC(t, r)
	_, err := addtargeting.Run(rc, addtargeting.Input{Package: "com.example.app", ID: "555", Regions: []string{"DE"}})
	if got := exitOf(t, err); got != 3 {
		t.Errorf("exit = %d, want 3; err=%v", got, err)
	}
	if len(r.calls) != 0 {
		t.Errorf("must not reach the network without --confirm; calls=%v", r.calls)
	}
}

// TestRun_confirmed_postsAddTargeting asserts the confirmed path POSTs
// :addTargeting with the widening body and emits ✓.
func TestRun_confirmed_postsAddTargeting(t *testing.T) {
	r := &rt{t: t}
	rc := newRC(t, r)
	var stderr bytes.Buffer
	rc.Stderr = &stderr
	if _, err := addtargeting.Run(rc, addtargeting.Input{Package: "com.example.app", ID: "555", Regions: []string{"DE"}, Confirm: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasSuffix(r.postURL, "/appRecoveries/555:addTargeting") {
		t.Errorf("url %q should POST :addTargeting", r.postURL)
	}
	if !strings.Contains(string(r.body), `"targetingUpdate"`) || !strings.Contains(string(r.body), "DE") {
		t.Errorf("body %q should carry the targetingUpdate", r.body)
	}
	if !strings.HasPrefix(stderr.String(), "✓ ") {
		t.Errorf("stderr missing ✓:\n%s", stderr.String())
	}
}
