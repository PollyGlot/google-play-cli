package status_test

// The --live path (#544): one edits.get on the pinned id, mocked through a
// RoundTripper so the suite stays offline. The default path keeps its
// network-free tests in status_test.go.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	statuscmd "github.com/PollyGlot/google-play-cli/commands/edits/status"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// liveRT serves the token exchange and the edits.get GET; getStatus (0 → 200)
// forces a non-2xx. Anything else (an insert, a delete) is a contract breach:
// a status probe must never open or drop an Edit.
type liveRT struct {
	t         *testing.T
	getStatus int

	mu       sync.Mutex
	getCalls int
}

func (r *liveRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		return jsonResp(200, `{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	}
	if req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/edits/edit-55") {
		r.getCalls++
		if r.getStatus != 0 {
			return jsonResp(r.getStatus, `{"error":{"code":404,"message":"Edit not found."}}`), nil
		}
		return jsonResp(200, `{"id":"edit-55","expiryTimeSeconds":"1700000000"}`), nil
	}
	r.t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
	return nil, nil
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func signedSAJSON(t *testing.T) []byte {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, _ := json.Marshal(map[string]any{
		"type": "service_account", "project_id": "p", "private_key": string(pemBytes),
		"client_email": "ci@p.iam.gserviceaccount.com", "token_uri": "https://oauth2.googleapis.com/token",
	})
	return raw
}

// newLiveRC is newRC plus an Account and a transport-injected HTTP client, the
// two things --live needs and the default path must never touch.
func newLiveRC(t *testing.T, rt http.RoundTripper) (*kernel.RunContext, string) {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	gplayDir := filepath.Join(t.TempDir(), ".gplay")
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	rc.Resolved = &config.Resolved{Pin: pkg, ProjectSharedPath: filepath.Join(gplayDir, "config.json")}
	return rc, gplayDir
}

func TestRun_live_open_reportsExpiry(t *testing.T) {
	rt := &liveRT{t: t}
	rc, gplayDir := newLiveRC(t, rt)
	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-55"); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	r, err := statuscmd.Run(rc, statuscmd.Input{Package: pkg, Live: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rt.getCalls != 1 {
		t.Errorf("getCalls = %d, want 1", rt.getCalls)
	}
	out := render(t, r, output.FormatJSON)
	for _, want := range []string{`"open": true`, `"editId": "edit-55"`, `"live": true`, `"expiryTimeSeconds": "1700000000"`} {
		if !strings.Contains(out, want) {
			t.Errorf("json = %q, want it to contain %s", out, want)
		}
	}
	if out := render(t, r, output.FormatTable); !strings.Contains(out, "edit-55") || !strings.Contains(out, "2023-11-14T22:13:20Z") {
		t.Errorf("table = %q, want the edit id and the RFC 3339 expiry", out)
	}
}

func TestRun_live_404_reportsClosedWithDiscardHint_keepsPin(t *testing.T) {
	rt := &liveRT{t: t, getStatus: 404}
	rc, gplayDir := newLiveRC(t, rt)
	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-55"); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	r, err := statuscmd.Run(rc, statuscmd.Input{Package: pkg, Live: true})
	if err != nil {
		t.Fatalf("Run: a vanished Edit is an answer, not a failure; got %v", err)
	}
	out := render(t, r, output.FormatJSON)
	for _, want := range []string{`"open": false`, `"editId": "edit-55"`, `"live": true`} {
		if !strings.Contains(out, want) {
			t.Errorf("json = %q, want it to contain %s", out, want)
		}
	}
	if strings.Contains(out, "expiryTimeSeconds") {
		t.Errorf("json = %q, must not carry an expiry for a vanished Edit", out)
	}
	if out := render(t, r, output.FormatTable); !strings.Contains(out, "gplay edits discard") {
		t.Errorf("table = %q, want the discard hint", out)
	}
	// The pin is the user's to clear (via discard): status never mutates it.
	if _, ok, _ := editpin.Lookup(config.OSFS{}, gplayDir, pkg); !ok {
		t.Error("status --live cleared the pin; it must only report")
	}
}

func TestRun_live_otherError_propagates(t *testing.T) {
	rt := &liveRT{t: t, getStatus: 403}
	rc, gplayDir := newLiveRC(t, rt)
	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-55"); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	_, err := statuscmd.Run(rc, statuscmd.Input{Package: pkg, Live: true})
	if err == nil {
		t.Fatal("a 403 on edits.get must propagate, not read as a closed Edit")
	}
}

func TestRun_live_noPin_staysOffline(t *testing.T) {
	// No Account, no transport: any network or credential access would fail
	// loudly, which is the point: --live with nothing pinned needs neither.
	rc, _ := newRC(t)

	r, err := statuscmd.Run(rc, statuscmd.Input{Package: pkg, Live: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out := render(t, r, output.FormatJSON); !strings.Contains(out, `"open": false`) || strings.Contains(out, `"live"`) {
		t.Errorf("json = %q, want open:false and no live marker (nothing was probed)", out)
	}
}
