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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	createcmd "github.com/PollyGlot/google-play-cli/commands/device-tiers/create"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

const createdBody = `{"deviceTierConfigId":"42","deviceGroups":[{"name":"high"}],"deviceTierSet":{"deviceTiers":[{"level":0}]}}`

type dtRT struct {
	t *testing.T

	mu      sync.Mutex
	calls   []string
	postURL string
}

func (r *dtRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.calls = append(r.calls, "POST /token")
		return jsonResp(200, `{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	}
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/deviceTierConfigs") {
		r.postURL = req.URL.String()
		return jsonResp(200, createdBody), nil
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

func writeJSON(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
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

// TestRun_happyPath_postsAndPassesThrough asserts a create POSTs the file body
// (no /edits/ segment) and the JSON view is the verbatim response, plus a ✓.
func TestRun_happyPath_postsAndPassesThrough(t *testing.T) {
	rt := &dtRT{t: t}
	rc := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	r, err := createcmd.Run(rc, createcmd.Input{Package: "com.example.app", File: writeJSON(t, `{"deviceGroups":[{"name":"high"}]}`)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(rt.postURL, "/edits/") {
		t.Errorf("create must not use an Edit; url=%s", rt.postURL)
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if strings.TrimSpace(out.String()) != createdBody {
		t.Errorf("json = %s, want verbatim pass-through", out.String())
	}
	if !strings.HasPrefix(stderr.String(), "✓ ") || !strings.Contains(stderr.String(), "42") {
		t.Errorf("stderr missing ✓ with new id:\n%s", stderr.String())
	}
}

// TestRun_rejectsDeviceTierConfigId_exit20 asserts a body carrying the
// output-only id is refused offline (no update API).
func TestRun_rejectsDeviceTierConfigId_exit20(t *testing.T) {
	rt := &dtRT{t: t}
	rc := newRC(t, rt)
	_, err := createcmd.Run(rc, createcmd.Input{Package: "com.example.app", File: writeJSON(t, `{"deviceTierConfigId":"7","deviceGroups":[]}`)})
	if got := exitOf(t, err); got != 20 {
		t.Errorf("exit = %d, want 20; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("must not reach the network; calls=%v", rt.calls)
	}
}

// TestRun_malformedJSON_exit20 asserts a non-JSON body fails offline.
func TestRun_malformedJSON_exit20(t *testing.T) {
	rt := &dtRT{t: t}
	rc := newRC(t, rt)
	_, err := createcmd.Run(rc, createcmd.Input{Package: "com.example.app", File: writeJSON(t, `not json`)})
	if got := exitOf(t, err); got != 20 {
		t.Errorf("exit = %d, want 20; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("must not reach the network; calls=%v", rt.calls)
	}
}

// TestRun_dryRun_noNetwork_noConfirm asserts --dry-run validates offline and
// emits a dryRun view with no ✓.
func TestRun_dryRun_noNetwork_noConfirm(t *testing.T) {
	rt := &dtRT{t: t}
	rc := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	r, err := createcmd.Run(rc, createcmd.Input{Package: "com.example.app", File: writeJSON(t, `{"deviceGroups":[]}`), DryRun: true})
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

// TestRun_stdinBody asserts the body is read from stdin when --file is empty.
func TestRun_stdinBody(t *testing.T) {
	rt := &dtRT{t: t}
	rc := newRC(t, rt)
	rc.Stdin = strings.NewReader(`{"deviceGroups":[{"name":"x"}]}`)
	if _, err := createcmd.Run(rc, createcmd.Input{Package: "com.example.app"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rt.calls) == 0 {
		t.Error("stdin body should have been posted")
	}
}

// TestRun_missingPackage_exit2 asserts a missing package is CLI misuse, offline.
func TestRun_missingPackage_exit2(t *testing.T) {
	rt := &dtRT{t: t}
	rc := newRC(t, rt)
	_, err := createcmd.Run(rc, createcmd.Input{File: writeJSON(t, `{"deviceGroups":[]}`)})
	if got := exitOf(t, err); got != 2 {
		t.Errorf("exit = %d, want 2; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("must not reach the network; calls=%v", rt.calls)
	}
}

// TestNewCommand_flags asserts the flag surface. The [experimental] label is
// applied at registration by kernel.Experimental and pinned centrally by
// TestStabilityRegistry_pinsPublicContract (ADR-0010/ADR-0042).
func TestNewCommand_flags(t *testing.T) {
	cmd := createcmd.NewCommand(kernel.Boot{})
	for _, n := range []string{"package", "file", "allow-unknown-devices", "dry-run", "output"} {
		if cmd.Flags().Lookup(n) == nil {
			t.Errorf("missing --%s", n)
		}
	}
}
