package create_test

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

	createcmd "github.com/PollyGlot/google-play-cli/commands/recovery/create"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const draftBody = `{"appRecoveryId":"555","status":"RECOVERY_STATUS_DRAFT"}`

// newFake serves the draft on the create POST; any other request fails the
// round trip.
func newFake() *testkit.Fake {
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		return http.StatusOK, draftBody, c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/appRecoveries")
	})
}

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
	fake := newFake()
	rc := newRC(t, fake)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	r, err := createcmd.Run(rc, createcmd.Input{Package: "com.example.app", VersionCode: 142, Regions: []string{"US"}, RemoteInAppUpdate: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	calls := fake.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want the one create POST", calls)
	}
	if body := calls[0].Body; !strings.Contains(string(body), `"versionCodes"`) || !strings.Contains(string(body), "142") {
		t.Errorf("request body %q should carry the version targeting", body)
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
	fake := newFake()
	rc := newRC(t, fake)
	_, err := createcmd.Run(rc, createcmd.Input{Package: "com.example.app", AllUsers: true})
	if got := exitOf(t, err); got != 2 {
		t.Errorf("exit = %d, want 2; err=%v", got, err)
	}
	if len(fake.Calls()) != 0 || fake.TokenExchanges() != 0 {
		t.Errorf("must not reach the network; calls=%v tokens=%d", fake.Calls(), fake.TokenExchanges())
	}
}

// TestRun_missingTargeting_exit2 asserts an audience selector is required, offline.
func TestRun_missingTargeting_exit2(t *testing.T) {
	fake := newFake()
	rc := newRC(t, fake)
	_, err := createcmd.Run(rc, createcmd.Input{Package: "com.example.app", VersionCode: 142})
	if got := exitOf(t, err); got != 2 {
		t.Errorf("exit = %d, want 2; err=%v", got, err)
	}
	if len(fake.Calls()) != 0 || fake.TokenExchanges() != 0 {
		t.Errorf("must not reach the network; calls=%v tokens=%d", fake.Calls(), fake.TokenExchanges())
	}
}

// TestRun_dryRun_noNetwork_noConfirm asserts --dry-run rehearses offline.
func TestRun_dryRun_noNetwork_noConfirm(t *testing.T) {
	fake := newFake()
	rc := newRC(t, fake)
	var stderr bytes.Buffer
	rc.Stderr = &stderr
	r, err := createcmd.Run(rc, createcmd.Input{Package: "com.example.app", VersionCode: 142, AllUsers: true, DryRun: true})
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
		DryRun bool `json:"dryRun"`
	}
	var out bytes.Buffer
	_ = r.Renderers().JSON(&out)
	if err := json.Unmarshal(out.Bytes(), &view); err != nil || !view.DryRun {
		t.Errorf("dry-run json = %s (err %v), want dryRun:true", out.String(), err)
	}
}
