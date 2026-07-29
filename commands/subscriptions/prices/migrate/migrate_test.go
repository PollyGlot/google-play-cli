package migrate_test

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

	migratecmd "github.com/PollyGlot/google-play-cli/commands/subscriptions/prices/migrate"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

type migrateRT struct {
	mu    sync.Mutex
	calls []string
	body  string
}

func (r *migrateRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		return jsonResp(200, `{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	}
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		r.body = string(b)
	}
	return jsonResp(200, `{}`), nil
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

func validInput() migratecmd.Input {
	return migratecmd.Input{
		Package:  "com.example.app",
		Product:  "premium",
		BasePlan: "monthly",
		Regions:  []string{"us", "FR"},
		Oldest:   "2026-01-01T00:00:00Z",
	}
}

// TestRun_dryRun_offlinePreview asserts --dry-run makes no HTTP call and lists
// the confirm gate in requires.
func TestRun_dryRun_offlinePreview(t *testing.T) {
	rt := &migrateRT{}
	rc := newRC(t, rt)
	in := validInput()
	in.DryRun = true
	r, err := migratecmd.Run(rc, in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("dry-run must be offline; calls=%v", rt.calls)
	}
	var js bytes.Buffer
	if err := r.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var view struct {
		DryRun   bool     `json:"dryRun"`
		Requires []string `json:"requires"`
		Regions  []string `json:"regions"`
	}
	if err := json.Unmarshal(js.Bytes(), &view); err != nil {
		t.Fatalf("json %s: %v", js.String(), err)
	}
	if !view.DryRun || len(view.Requires) != 1 || view.Requires[0] != "confirm" {
		t.Errorf("view = %+v, want dryRun with requires [confirm]", view)
	}
	if len(view.Regions) != 2 || view.Regions[0] != "US" {
		t.Errorf("regions = %v, want normalized [US FR]", view.Regions)
	}
}

// TestRun_refusesWithoutConfirm asserts exit 3 naming --confirm, no network.
func TestRun_refusesWithoutConfirm(t *testing.T) {
	rt := &migrateRT{}
	rc := newRC(t, rt)
	_, err := migratecmd.Run(rc, validInput())
	assertExit(t, err, 3)
	if !strings.Contains(err.Error(), "--confirm") {
		t.Errorf("refusal %q must name --confirm", err.Error())
	}
	if len(rt.calls) != 0 {
		t.Errorf("refusal must not reach the network; calls=%v", rt.calls)
	}
}

// TestRun_confirmed_migrates asserts the confirmed run POSTs :migratePrices
// with one RegionalPriceMigration per region and the path identity echoed.
func TestRun_confirmed_migrates(t *testing.T) {
	rt := &migrateRT{}
	rc := newRC(t, rt)
	in := validInput()
	in.Confirm = true
	in.PriceIncreaseType = "opt-out"
	r, err := migratecmd.Run(rc, in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rt.calls) != 1 || !strings.HasSuffix(rt.calls[0], "/subscriptions/premium/basePlans/monthly:migratePrices") {
		t.Fatalf("calls = %v, want the :migratePrices POST", rt.calls)
	}
	for _, want := range []string{`"regionCode":"US"`, `"regionCode":"FR"`, `"packageName":"com.example.app"`, `"priceIncreaseType":"PRICE_INCREASE_TYPE_OPT_OUT"`, `"version":"2022/02"`} {
		if !strings.Contains(rt.body, want) {
			t.Errorf("body %q missing %s", rt.body, want)
		}
	}
	var js bytes.Buffer
	if err := r.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(js.String(), `"ok": true`) {
		t.Errorf("json %s should be the gplay-shaped success object", js.String())
	}
}

// TestRun_validation_exit2_noNetwork asserts each bad input is CLI misuse
// caught offline.
func TestRun_validation_exit2_noNetwork(t *testing.T) {
	cases := []func(*migratecmd.Input){
		func(in *migratecmd.Input) { in.Product = "" },
		func(in *migratecmd.Input) { in.Regions = nil },
		func(in *migratecmd.Input) { in.Oldest = "yesterday" },
		func(in *migratecmd.Input) { in.PriceIncreaseType = "mandatory" },
	}
	for i, mutate := range cases {
		rt := &migrateRT{}
		rc := newRC(t, rt)
		in := validInput()
		in.Confirm = true
		mutate(&in)
		_, err := migratecmd.Run(rc, in)
		assertExit(t, err, 2)
		if len(rt.calls) != 0 {
			t.Errorf("case %d must not reach the network; calls=%v", i, rt.calls)
		}
	}
}

func assertExit(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error with exit %d, got nil", want)
	}
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) {
		t.Fatalf("err %v has no ExitCode", err)
	}
	if got := c.ExitCode(); got != want {
		t.Errorf("exit = %d, want %d", got, want)
	}
}
