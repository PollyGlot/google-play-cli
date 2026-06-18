package deploy_test

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

	deploycmd "github.com/PollyGlot/google-play-cli/commands/recovery/deploy"
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

// TestRun_missingConfirm_exit3_noNetwork asserts the destructive gate fires
// before any HTTP and is exit 3 (deterministically resolvable).
func TestRun_missingConfirm_exit3_noNetwork(t *testing.T) {
	r := &rt{t: t}
	rc := newRC(t, r)
	_, err := deploycmd.Run(rc, deploycmd.Input{Package: "com.example.app", ID: "555"})
	if got := exitOf(t, err); got != 3 {
		t.Errorf("exit = %d, want 3; err=%v", got, err)
	}
	var sf interface{ ExitCode() int }
	if errors.As(err, &sf) {
		if fe, ok := err.(interface{ Error() string }); ok && !strings.Contains(fe.Error(), "--confirm") {
			t.Errorf("error %q should name --confirm", fe.Error())
		}
	}
	if len(r.calls) != 0 {
		t.Errorf("must not reach the network without --confirm; calls=%v", r.calls)
	}
}

// TestRun_dryRun_noNetwork_requiresConfirm asserts --dry-run rehearses offline
// and the JSON view advertises requires:["confirm"].
func TestRun_dryRun_noNetwork_requiresConfirm(t *testing.T) {
	r := &rt{t: t}
	rc := newRC(t, r)
	var stderr bytes.Buffer
	rc.Stderr = &stderr
	res, err := deploycmd.Run(rc, deploycmd.Input{Package: "com.example.app", ID: "555", DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("dry-run must make no network call; calls=%v", r.calls)
	}
	if strings.Contains(stderr.String(), "✓") {
		t.Errorf("dry-run emitted a ✓; stderr=%q", stderr.String())
	}
	var view struct {
		DryRun   bool     `json:"dryRun"`
		Requires []string `json:"requires"`
	}
	var out bytes.Buffer
	_ = res.Renderers().JSON(&out)
	if err := json.Unmarshal(out.Bytes(), &view); err != nil {
		t.Fatalf("dry-run json: %v", err)
	}
	if !view.DryRun || len(view.Requires) != 1 || view.Requires[0] != "confirm" {
		t.Errorf("dry-run view = %+v, want dryRun + requires [confirm]", view)
	}
}

// TestRun_confirmed_postsDeploy_andPassesThrough asserts the confirmed path
// POSTs :deploy, emits ✓, and passes the empty {} through verbatim.
func TestRun_confirmed_postsDeploy_andPassesThrough(t *testing.T) {
	r := &rt{t: t}
	rc := newRC(t, r)
	var stderr bytes.Buffer
	rc.Stderr = &stderr
	res, err := deploycmd.Run(rc, deploycmd.Input{Package: "com.example.app", ID: "555", Confirm: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasSuffix(r.postURL, "/appRecoveries/555:deploy") {
		t.Errorf("url %q should POST :deploy", r.postURL)
	}
	if !strings.HasPrefix(stderr.String(), "✓ ") || !strings.Contains(stderr.String(), "555") {
		t.Errorf("stderr missing ✓ with id:\n%s", stderr.String())
	}
	var out bytes.Buffer
	if err := res.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if strings.TrimSpace(out.String()) != `{}` {
		t.Errorf("json = %s, want verbatim empty {}", out.String())
	}
}
