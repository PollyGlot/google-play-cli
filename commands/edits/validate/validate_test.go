package validate_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	validatecmd "github.com/PollyGlot/google-play-cli/commands/edits/validate"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const pkg = "com.example.app"

// validateRT serves the token exchange and the edits.validate POST;
// validateStatus (0 → 200) forces a non-2xx for the failure test. Any other
// request (an insert, a commit, a delete) is a contract breach.
type validateRT struct {
	t              *testing.T
	validateStatus int

	mu            sync.Mutex
	validateCalls int
}

func (r *validateRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		return jsonResp(200, `{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	}
	if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits/edit-9:validate") {
		r.validateCalls++
		if r.validateStatus != 0 {
			return jsonResp(r.validateStatus, `{"error":{"code":400,"message":"The release notes exceed 500 characters"}}`), nil
		}
		return jsonResp(200, `{"id":"edit-9","expiryTimeSeconds":"1700000000"}`), nil
	}
	r.t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
	return nil, nil
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func signedSAJSON(t *testing.T) []byte {
	t.Helper()
	key := testkit.RSAKey(t)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, _ := json.Marshal(map[string]any{
		"type": "service_account", "project_id": "p", "private_key": string(pemBytes),
		"client_email": "ci@p.iam.gserviceaccount.com", "token_uri": "https://oauth2.googleapis.com/token",
	})
	return raw
}

func newRC(t *testing.T, rt http.RoundTripper) (*kernel.RunContext, string) {
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

func exitOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) {
		t.Fatalf("err %v (%T) has no ExitCode", err, err)
	}
	return c.ExitCode()
}

func render(t *testing.T, r output.Renderable, f output.Format) string {
	t.Helper()
	var buf bytes.Buffer
	if err := output.Render(&buf, f, r.Renderers()); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

func TestRun_valid_mirrorsAPIBodyAndKeepsPin(t *testing.T) {
	rt := &validateRT{t: t}
	rc, gplayDir := newRC(t, rt)
	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-9"); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	r, err := validatecmd.Run(rc, validatecmd.Input{Package: pkg})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rt.validateCalls != 1 {
		t.Errorf("validateCalls = %d, want 1", rt.validateCalls)
	}
	// ADR-0003: --output json is the API body, not a gplay envelope.
	if got, want := strings.TrimSpace(render(t, r, output.FormatJSON)), `{"id":"edit-9","expiryTimeSeconds":"1700000000"}`; got != want {
		t.Errorf("json = %q, want the verbatim AppEdit %q", got, want)
	}
	if out := render(t, r, output.FormatTable); !strings.Contains(out, "validated explicit edit edit-9") {
		t.Errorf("table = %q", out)
	}
	// Validate never commits: the Edit is still open, so the pin must survive.
	if _, ok, _ := editpin.Lookup(config.OSFS{}, gplayDir, pkg); !ok {
		t.Error("pin cleared by validate; it must stay for the later commit/discard")
	}
}

func TestRun_invalid_400_exit30_keepsPin(t *testing.T) {
	rt := &validateRT{t: t, validateStatus: 400}
	rc, gplayDir := newRC(t, rt)
	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-9"); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	_, err := validatecmd.Run(rc, validatecmd.Input{Package: pkg})
	if code := exitOf(t, err); code != 30 {
		t.Fatalf("exit = %d, want 30 (API 4xx); err = %v", code, err)
	}
	if !strings.Contains(err.Error(), "release notes exceed") {
		t.Errorf("err = %q, want the API's message surfaced", err)
	}
	if _, ok, _ := editpin.Lookup(config.OSFS{}, gplayDir, pkg); !ok {
		t.Error("a failed validate must leave the pin in place for a fix-and-retry")
	}
}

func TestRun_noOpenEdit_exit60_noNetwork(t *testing.T) {
	rt := &validateRT{t: t}
	rc, _ := newRC(t, rt)

	_, err := validatecmd.Run(rc, validatecmd.Input{Package: pkg})
	if code := exitOf(t, err); code != 60 {
		t.Fatalf("exit = %d, want 60 (no open edit)", code)
	}
	if rt.validateCalls != 0 {
		t.Errorf("no open edit must fail before the network; validateCalls = %d", rt.validateCalls)
	}
}
