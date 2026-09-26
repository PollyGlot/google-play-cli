package pull_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	pullcmd "github.com/PollyGlot/google-play-cli/commands/iap/pull"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// iapLive holds the bodies the three list surfaces (v2 products, legacy
// inappproducts, wildcard offers) answer with; serve routes a call to them.
type iapLive struct {
	v2Body     string
	legacyBody string
	offersBody string
}

func (l *iapLive) serve(c testkit.Call) (int, string, bool) {
	switch {
	case strings.Contains(c.Path, "/purchaseOptions/-/offers"):
		return http.StatusOK, orBody(l.offersBody), true
	case strings.Contains(c.Path, "/inappproducts"):
		return http.StatusOK, orBody(l.legacyBody), true
	default:
		return http.StatusOK, orBody(l.v2Body), true
	}
}

func orBody(b string) string {
	if b == "" {
		return `{}`
	}
	return b
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

// TestRun_unionsV2AndLegacy asserts pull writes one file per v2 product (with
// offers embedded) and one per unmigrated legacy product, dedupes a shared ID
// in favor of v2, and reports the composite json envelope.
func TestRun_unionsV2AndLegacy(t *testing.T) {
	dir := t.TempDir()
	live := &iapLive{
		v2Body:     `{"oneTimeProducts":[{"productId":"coins100","packageName":"com.example.app","listings":[],"purchaseOptions":[{"purchaseOptionId":"buy","state":"ACTIVE"}]}]}`,
		legacyBody: `{"inappproduct":[{"sku":"coins100","purchaseType":"managedUser"},{"sku":"old_gems","purchaseType":"managedUser","status":"active","listings":{"en-US":{"title":"Gems & <more>"}}}]}`,
		offersBody: `{"oneTimeProductOffers":[{"packageName":"com.example.app","productId":"coins100","purchaseOptionId":"buy","offerId":"promo","state":"ACTIVE"}]}`,
	}
	rc := newRC(t, testkit.NewFake(live.serve))
	r, err := pullcmd.Run(rc, pullcmd.Input{Package: "com.example.app", Dir: dir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// coins100 = the v2 file (legacy row shadowed), old_gems = the legacy file.
	b, err := os.ReadFile(filepath.Join(dir, "coins100.json"))
	if err != nil {
		t.Fatalf("coins100.json: %v", err)
	}
	var v2 struct {
		ProductID       string `json:"productId"`
		SKU             string `json:"sku"`
		PurchaseOptions []struct {
			Offers []map[string]any `json:"offers"`
		} `json:"purchaseOptions"`
	}
	if err := json.Unmarshal(b, &v2); err != nil {
		t.Fatal(err)
	}
	if v2.ProductID != "coins100" || v2.SKU != "" {
		t.Errorf("coins100.json must be the v2 resource, got %s", b)
	}
	if len(v2.PurchaseOptions) != 1 || len(v2.PurchaseOptions[0].Offers) != 1 {
		t.Errorf("coins100.json must embed the promo offer, got %s", b)
	}
	lb, err := os.ReadFile(filepath.Join(dir, "old_gems.json"))
	if err != nil {
		t.Fatalf("old_gems.json: %v", err)
	}
	if !strings.Contains(string(lb), `"sku"`) {
		t.Errorf("old_gems.json must keep the legacy shape (sku), got %s", lb)
	}
	var js bytes.Buffer
	if err := r.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var envelope struct {
		OneTimeProducts []map[string]any `json:"oneTimeProducts"`
		InAppProduct    []map[string]any `json:"inappproduct"`
	}
	if err := json.Unmarshal(js.Bytes(), &envelope); err != nil {
		t.Fatalf("json %s: %v", js.String(), err)
	}
	if len(envelope.OneTimeProducts) != 1 || len(envelope.InAppProduct) != 2 {
		t.Errorf("composite envelope = %s, want 1 v2 + 2 legacy verbatim", js.String())
	}
	// The composite envelope and the written file keep the API's & and <>
	// literal (#622): neither may re-encode them as \u0026 or \u003c.
	for name, b := range map[string][]byte{"json output": js.Bytes(), "old_gems.json": lb} {
		if !strings.Contains(string(b), "Gems & <more>") {
			t.Errorf("%s must carry the title verbatim, got %s", name, b)
		}
	}
	var tbl bytes.Buffer
	if err := r.Renderers().Table(&tbl); err != nil {
		t.Fatalf("Table: %v", err)
	}
	if !strings.Contains(tbl.String(), "shadowed by the v2 product") {
		t.Errorf("table %q should note the shadowed legacy row", tbl.String())
	}
}

// TestRun_emptyLiveNonEmptyLocal_refuses asserts the erase guard covers the
// union of both live surfaces.
func TestRun_emptyLiveNonEmptyLocal_refuses(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "coins100.json"), []byte(`{"productId":"coins100"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	live := &iapLive{}
	rc := newRC(t, testkit.NewFake(live.serve))
	_, err := pullcmd.Run(rc, pullcmd.Input{Package: "com.example.app", Dir: dir})
	assertExit(t, err, 2)
	if _, statErr := os.Stat(filepath.Join(dir, "coins100.json")); statErr != nil {
		t.Errorf("coins100.json must survive the refusal: %v", statErr)
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
