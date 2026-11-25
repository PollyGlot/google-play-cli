package migrate_test

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

	migratecmd "github.com/PollyGlot/google-play-cli/commands/subscriptions/prices/migrate"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

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

func validInput() migratecmd.Input {
	return migratecmd.Input{
		Package:  "com.example.app",
		Product:  "premium",
		BasePlan: "monthly",
		Regions:  []string{"us", "FR"},
		Oldest:   "2026-01-01T00:00:00Z",
	}
}

func TestRun_dryRun_offlinePreview(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, `{}`))
	rc := newRC(t, fake)
	in := validInput()
	in.DryRun = true
	r, err := migratecmd.Run(rc, in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("dry-run must be offline; calls=%v", fake.Calls())
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

func TestRun_refusesWithoutConfirm(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, `{}`))
	rc := newRC(t, fake)
	_, err := migratecmd.Run(rc, validInput())
	assertExit(t, err, 3)
	if !strings.Contains(err.Error(), "--confirm") {
		t.Errorf("refusal %q must name --confirm", err.Error())
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("refusal must not reach the network; calls=%v", fake.Calls())
	}
}

func TestRun_confirmed_migrates(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, ""))
	rc := newRC(t, fake)
	in := validInput()
	in.Confirm = true
	in.PriceIncreaseType = "opt-out"
	r, err := migratecmd.Run(rc, in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || !strings.HasSuffix(calls[0].Path, "/subscriptions/premium/basePlans/monthly:migratePrices") {
		t.Fatalf("calls = %v, want the :migratePrices POST", calls)
	}
	for _, want := range []string{`"regionCode":"US"`, `"regionCode":"FR"`, `"packageName":"com.example.app"`, `"priceIncreaseType":"PRICE_INCREASE_TYPE_OPT_OUT"`, `"version":"2022/02"`} {
		if !strings.Contains(string(calls[0].Body), want) {
			t.Errorf("body %q missing %s", calls[0].Body, want)
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

func TestRun_validation_exit2_noNetwork(t *testing.T) {
	cases := []func(*migratecmd.Input){
		func(in *migratecmd.Input) { in.Product = "" },
		func(in *migratecmd.Input) { in.Regions = nil },
		func(in *migratecmd.Input) { in.Oldest = "yesterday" },
		func(in *migratecmd.Input) { in.PriceIncreaseType = "mandatory" },
	}
	for i, mutate := range cases {
		fake := testkit.NewFake(testkit.Any(http.StatusOK, `{}`))
		rc := newRC(t, fake)
		in := validInput()
		in.Confirm = true
		mutate(&in)
		_, err := migratecmd.Run(rc, in)
		assertExit(t, err, 2)
		if len(fake.Calls()) != 0 {
			t.Errorf("case %d must not reach the network; calls=%v", i, fake.Calls())
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

func TestRun_emptyBody_printsAck_bodyPassesThrough(t *testing.T) {
	for body, want := range map[string]string{
		"":   "{\n  \"ok\": true,\n  \"productId\": \"premium\",\n  \"basePlanId\": \"monthly\",\n  \"regions\": [\n    \"US\",\n    \"FR\"\n  ]\n}\n",
		"{}": "{}",
	} {
		rc := newRC(t, testkit.NewFake(testkit.Any(http.StatusOK, body)))
		in := validInput()
		in.Confirm = true
		res, err := migratecmd.Run(rc, in)
		if err != nil {
			t.Fatalf("Run with body %q: %v", body, err)
		}
		var out bytes.Buffer
		if err := res.Renderers().JSON(&out); err != nil {
			t.Fatalf("JSON: %v", err)
		}
		if out.String() != want {
			t.Errorf("body %q: json = %q, want %q", body, out.String(), want)
		}
	}
}
