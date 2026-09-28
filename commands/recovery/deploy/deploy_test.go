package deploy_test

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

	deploycmd "github.com/PollyGlot/google-play-cli/commands/recovery/deploy"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func saJSON(t *testing.T) []byte {
	t.Helper()
	key := testkit.RSAKey(t)
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
	fake := testkit.NewFake(testkit.Any(http.StatusOK, `{}`))
	rc := newRC(t, fake)
	_, err := deploycmd.Run(rc, deploycmd.Input{Package: "com.example.app", ID: "555"})
	if got := exitOf(t, err); got != 3 {
		t.Errorf("exit = %d, want 3; err=%v", got, err)
	}
	var sf interface{ ExitCode() int }
	if errors.As(err, &sf) && !strings.Contains(err.Error(), "--confirm") {
		t.Errorf("error %q should name --confirm", err.Error())
	}
	if len(fake.Calls()) != 0 || fake.TokenExchanges() != 0 {
		t.Errorf("must not reach the network without --confirm; calls=%v tokens=%d", fake.Calls(), fake.TokenExchanges())
	}
}

// TestRun_dryRun_noNetwork_requiresConfirm asserts --dry-run rehearses offline
// and the JSON view advertises requires:["confirm"].
func TestRun_dryRun_noNetwork_requiresConfirm(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, `{}`))
	rc := newRC(t, fake)
	var stderr bytes.Buffer
	rc.Stderr = &stderr
	res, err := deploycmd.Run(rc, deploycmd.Input{Package: "com.example.app", ID: "555", DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fake.Calls()) != 0 || fake.TokenExchanges() != 0 {
		t.Errorf("dry-run must make no network call; calls=%v tokens=%d", fake.Calls(), fake.TokenExchanges())
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
	fake := testkit.NewFake(testkit.Any(http.StatusOK, `{}`))
	rc := newRC(t, fake)
	var stderr bytes.Buffer
	rc.Stderr = &stderr
	res, err := deploycmd.Run(rc, deploycmd.Input{Package: "com.example.app", ID: "555", Confirm: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Method != http.MethodPost || !strings.HasSuffix(calls[0].URL, "/appRecoveries/555:deploy") {
		t.Fatalf("calls = %+v, want one POST :deploy", calls)
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
