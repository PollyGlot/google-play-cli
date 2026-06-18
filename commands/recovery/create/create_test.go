package create_test

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

	createcmd "github.com/PollyGlot/google-play-cli/commands/recovery/create"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

const draftBody = `{"appRecoveryId":"555","status":"RECOVERY_STATUS_DRAFT"}`

type recRT struct {
	t       *testing.T
	mu      sync.Mutex
	calls   []string
	postURL string
	body    []byte
}

func (r *recRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.calls = append(r.calls, "POST /token")
		return jsonResp(200, `{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	}
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/appRecoveries") {
		r.postURL = req.URL.String()
		r.body, _ = io.ReadAll(req.Body)
		return jsonResp(200, draftBody), nil
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

// TestRun_happyPath_postsDraft asserts a draft is posted with the targeting and
// the response passes through, with a ✓ line.
func TestRun_happyPath_postsDraft(t *testing.T) {
	rt := &recRT{t: t}
	rc := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	r, err := createcmd.Run(rc, createcmd.Input{Package: "com.example.app", VersionCode: 142, Regions: []string{"US"}, RemoteInAppUpdate: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(string(rt.body), `"versionCodes"`) || !strings.Contains(string(rt.body), "142") {
		t.Errorf("request body %q should carry the version targeting", rt.body)
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if strings.TrimSpace(out.String()) != draftBody {
		t.Errorf("json = %s, want verbatim", out.String())
	}
	if !strings.HasPrefix(stderr.String(), "✓ ") || !strings.Contains(stderr.String(), "555") {
		t.Errorf("stderr missing ✓ with id:\n%s", stderr.String())
	}
}

// TestRun_missingVersionCode_exit2 asserts the bad version is required, offline.
func TestRun_missingVersionCode_exit2(t *testing.T) {
	rt := &recRT{t: t}
	rc := newRC(t, rt)
	_, err := createcmd.Run(rc, createcmd.Input{Package: "com.example.app", AllUsers: true})
	if got := exitOf(t, err); got != 2 {
		t.Errorf("exit = %d, want 2; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("must not reach the network; calls=%v", rt.calls)
	}
}

// TestRun_missingTargeting_exit2 asserts an audience selector is required, offline.
func TestRun_missingTargeting_exit2(t *testing.T) {
	rt := &recRT{t: t}
	rc := newRC(t, rt)
	_, err := createcmd.Run(rc, createcmd.Input{Package: "com.example.app", VersionCode: 142})
	if got := exitOf(t, err); got != 2 {
		t.Errorf("exit = %d, want 2; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("must not reach the network; calls=%v", rt.calls)
	}
}

// TestRun_dryRun_noNetwork_noConfirm asserts --dry-run rehearses offline.
func TestRun_dryRun_noNetwork_noConfirm(t *testing.T) {
	rt := &recRT{t: t}
	rc := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr
	r, err := createcmd.Run(rc, createcmd.Input{Package: "com.example.app", VersionCode: 142, AllUsers: true, DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("dry-run must make no network call; calls=%v", rt.calls)
	}
	if strings.Contains(stderr.String(), "✓") {
		t.Errorf("dry-run emitted a ✓; stderr=%q", stderr.String())
	}
	var view struct {
		DryRun bool `json:"dryRun"`
	}
	var out bytes.Buffer
	_ = r.Renderers().JSON(&out)
	if err := json.Unmarshal(out.Bytes(), &view); err != nil || !view.DryRun {
		t.Errorf("dry-run json = %s (err %v), want dryRun:true", out.String(), err)
	}
}
