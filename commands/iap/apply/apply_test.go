package apply_test

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

	applycmd "github.com/PollyGlot/google-play-cli/commands/iap/apply"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// liveV2 serves one product with one purchase option (state ACTIVE) — files
// never declare that state, the normalizer keeps it out of every diff.
const liveV2 = `{"oneTimeProducts":[
  {"productId":"coins100","packageName":"com.example.app","listings":[{"languageCode":"en-US","title":"Coins"}],"purchaseOptions":[{"purchaseOptionId":"buy","state":"ACTIVE"}]}
]}`

const liveLegacy = `{"inappproduct":[{"sku":"old_gems","purchaseType":"managedUser","status":"active"}]}`

const liveOffers = `{"oneTimeProductOffers":[
  {"packageName":"com.example.app","productId":"coins100","purchaseOptionId":"buy","offerId":"promo","state":"ACTIVE","regionalPricingAndAvailabilityConfigs":[{"regionCode":"US"}]}
]}`

type iapRT struct {
	mu         sync.Mutex
	calls      []string
	urls       []string
	bodies     map[string]string
	v2Body     string
	legacyBody string
	offersBody string
}

func (r *iapRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		return jsonResp(200, `{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	}
	key := req.Method + " " + req.URL.Path
	r.calls = append(r.calls, key)
	r.urls = append(r.urls, req.URL.String())
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		if r.bodies == nil {
			r.bodies = map[string]string{}
		}
		r.bodies[key] = string(b)
	}
	if req.Method != http.MethodGet {
		return jsonResp(200, `{}`), nil
	}
	switch {
	case strings.Contains(req.URL.Path, "/purchaseOptions/-/offers"):
		return jsonResp(200, orDefault(r.offersBody, liveOffers)), nil
	case strings.Contains(req.URL.Path, "/inappproducts"):
		return jsonResp(200, orDefault(r.legacyBody, liveLegacy)), nil
	default:
		return jsonResp(200, orDefault(r.v2Body, liveV2)), nil
	}
}

func orDefault(b, def string) string {
	if b == "" {
		return def
	}
	return b
}

func (r *iapRT) saw(method, fragment string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if strings.HasPrefix(c, method+" ") && strings.Contains(c, fragment) {
			return true
		}
	}
	return false
}

func (r *iapRT) mutations() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var m []string
	for _, c := range r.calls {
		if !strings.HasPrefix(c, "GET ") {
			m = append(m, c)
		}
	}
	return m
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
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc
}

func writeCatalog(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// matchedCatalog mirrors the live surfaces exactly (as a pull would have
// written them: packageName stripped, offers embedded, legacy kept verbatim).
func matchedCatalog(t *testing.T) string {
	t.Helper()
	return writeCatalog(t, map[string]string{
		"coins100.json": `{"productId":"coins100","listings":[{"languageCode":"en-US","title":"Coins"}],"purchaseOptions":[{"purchaseOptionId":"buy","state":"ACTIVE","offers":[{"productId":"coins100","purchaseOptionId":"buy","offerId":"promo","state":"ACTIVE","regionalPricingAndAvailabilityConfigs":[{"regionCode":"US"}]}]}]}`,
		"old_gems.json": `{"sku":"old_gems","purchaseType":"managedUser","status":"active"}`,
	})
}

// TestRun_noChanges_isNoOp asserts the pull-then-apply invariant across both
// models: nothing mutates, the legacy file is accepted untouched.
func TestRun_noChanges_isNoOp(t *testing.T) {
	rt := &iapRT{}
	rc := newRC(t, rt)
	r, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: matchedCatalog(t)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if m := rt.mutations(); len(m) != 0 {
		t.Errorf("no-op apply must not mutate; got %v", m)
	}
	var out bytes.Buffer
	if err := r.Renderers().Table(&out); err != nil {
		t.Fatalf("Table: %v", err)
	}
	if !strings.Contains(out.String(), "no changes to apply") {
		t.Errorf("table %q should say no changes", out.String())
	}
}

// TestRun_v2CreateIsUpsert asserts a new v2 product creates via patch with
// allowMissing (the API has no insert) and the embedded offer creates via the
// per-option batchUpdate.
func TestRun_v2CreateIsUpsert(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"coins100.json": `{"productId":"coins100","listings":[{"languageCode":"en-US","title":"Coins"}],"purchaseOptions":[{"purchaseOptionId":"buy","offers":[{"productId":"coins100","purchaseOptionId":"buy","offerId":"promo","regionalPricingAndAvailabilityConfigs":[{"regionCode":"US"}]}]}]}`,
		"gems50.json":   `{"productId":"gems50","listings":[{"languageCode":"en-US","title":"Gems"}],"purchaseOptions":[{"purchaseOptionId":"buy","offers":[{"offerId":"launch"}]}]}`,
		"old_gems.json": `{"sku":"old_gems","purchaseType":"managedUser","status":"active"}`,
	})
	rt := &iapRT{}
	rc := newRC(t, rt)
	if _, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var createURL, batchBody string
	for i, u := range rt.urls {
		if strings.Contains(u, "/onetimeproducts/gems50?") {
			createURL = u
		}
		if strings.Contains(u, "/oneTimeProducts/gems50/purchaseOptions/buy/offers:batchUpdate") {
			batchBody = rt.bodies[rt.calls[i]]
		}
	}
	if !strings.Contains(createURL, "allowMissing=true") || strings.Contains(createURL, "updateMask") {
		t.Errorf("v2 create url %q must be an unmasked allowMissing patch", createURL)
	}
	if !strings.Contains(batchBody, `"allowMissing":true`) || !strings.Contains(batchBody, `"offerId":"launch"`) {
		t.Errorf("offer batchUpdate body %q must upsert the new offer", batchBody)
	}
	// The embedded offers array must never reach the product body.
	for k, b := range rt.bodies {
		if strings.Contains(k, "/onetimeproducts/") && strings.Contains(b, `"offers"`) {
			t.Errorf("product body %q must strip embedded offers", b)
		}
	}
}

// TestRun_deletes_gated asserts omitting a live v2 product refuses without
// --confirm and deletes with it (offers excluded — the parent delete takes
// them).
func TestRun_deletes_gated(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"old_gems.json": `{"sku":"old_gems","purchaseType":"managedUser","status":"active"}`,
	})
	rt := &iapRT{}
	rc := newRC(t, rt)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir})
	assertExit(t, err, 3)
	if m := rt.mutations(); len(m) != 0 {
		t.Errorf("refusal must not mutate; got %v", m)
	}

	rt2 := &iapRT{}
	rc2 := newRC(t, rt2)
	if _, err := applycmd.Run(rc2, applycmd.Input{Package: "com.example.app", Dir: dir, Confirm: true}); err != nil {
		t.Fatalf("Run --confirm: %v", err)
	}
	if !rt2.saw("DELETE", "/oneTimeProducts/coins100") {
		t.Errorf("calls = %v, want the product DELETE", rt2.calls)
	}
	if rt2.saw("POST", ":batchDelete") {
		t.Errorf("calls = %v, offers of a deleted product must ride the parent delete", rt2.calls)
	}
}

// TestRun_legacyEdit_refuses asserts an edited legacy file names the #372
// migrate gate.
func TestRun_legacyEdit_refuses(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"coins100.json": `{"productId":"coins100","listings":[{"languageCode":"en-US","title":"Coins"}],"purchaseOptions":[{"purchaseOptionId":"buy","offers":[{"productId":"coins100","purchaseOptionId":"buy","offerId":"promo","regionalPricingAndAvailabilityConfigs":[{"regionCode":"US"}]}]}]}`,
		"old_gems.json": `{"sku":"old_gems","purchaseType":"managedUser","status":"inactive"}`,
	})
	rt := &iapRT{}
	rc := newRC(t, rt)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir, DryRun: true})
	assertExit(t, err, 2)
	if !strings.Contains(err.Error(), "--migrate") {
		t.Errorf("refusal %q should name the --migrate gate", err.Error())
	}
	if m := rt.mutations(); len(m) != 0 {
		t.Errorf("refusal must not mutate; got %v", m)
	}
}

// TestRun_legacyOmission_refuses asserts deleting a legacy file refuses (gplay
// never deletes the legacy surface).
func TestRun_legacyOmission_refuses(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"coins100.json": `{"productId":"coins100","listings":[{"languageCode":"en-US","title":"Coins"}],"purchaseOptions":[{"purchaseOptionId":"buy","offers":[{"productId":"coins100","purchaseOptionId":"buy","offerId":"promo","regionalPricingAndAvailabilityConfigs":[{"regionCode":"US"}]}]}]}`,
	})
	rt := &iapRT{}
	rc := newRC(t, rt)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir, DryRun: true})
	assertExit(t, err, 2)
	if !strings.Contains(err.Error(), "old_gems") {
		t.Errorf("refusal %q should name the omitted legacy product", err.Error())
	}
}

// migrationCatalog redeclares the live legacy old_gems as a v2 product (the
// one-way promotion gesture), next to the matched coins100.
func migrationCatalog(t *testing.T) string {
	t.Helper()
	return writeCatalog(t, map[string]string{
		"coins100.json": `{"productId":"coins100","listings":[{"languageCode":"en-US","title":"Coins"}],"purchaseOptions":[{"purchaseOptionId":"buy","offers":[{"productId":"coins100","purchaseOptionId":"buy","offerId":"promo","regionalPricingAndAvailabilityConfigs":[{"regionCode":"US"}]}]}]}`,
		"old_gems.json": `{"productId":"old_gems","listings":[{"languageCode":"en-US","title":"Old Gems"}],"purchaseOptions":[{"purchaseOptionId":"buy"}]}`,
	})
}

// TestRun_migrate_refusesWithoutFlag asserts a legacy→v2 redeclaration on a
// real run exits 3 naming --migrate, mutating nothing.
func TestRun_migrate_refusesWithoutFlag(t *testing.T) {
	rt := &iapRT{}
	rc := newRC(t, rt)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: migrationCatalog(t)})
	assertExit(t, err, 3)
	if !strings.Contains(err.Error(), "--migrate") || !strings.Contains(err.Error(), "one-way") {
		t.Errorf("refusal %q must name --migrate and the one-way door", err.Error())
	}
	if m := rt.mutations(); len(m) != 0 {
		t.Errorf("refusal must not mutate; got %v", m)
	}
}

// TestRun_migrate_dryRunPreviews asserts --dry-run shows the promotion as a
// distinct "migrate" op with requires:["migrate"], without the flag.
func TestRun_migrate_dryRunPreviews(t *testing.T) {
	rt := &iapRT{}
	rc := newRC(t, rt)
	r, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: migrationCatalog(t), DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if m := rt.mutations(); len(m) != 0 {
		t.Errorf("dry-run must not mutate; got %v", m)
	}
	var js bytes.Buffer
	if err := r.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	got := js.String()
	for _, want := range []string{`"op": "migrate"`, `"old_gems"`, `"migrate"`} {
		if !strings.Contains(got, want) {
			t.Errorf("json %s missing %s", got, want)
		}
	}
	var view struct {
		Requires []string       `json:"requires"`
		Summary  map[string]int `json:"summary"`
	}
	if err := json.Unmarshal(js.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range view.Requires {
		if r == "migrate" {
			found = true
		}
	}
	if !found {
		t.Errorf("requires = %v, want migrate listed", view.Requires)
	}
	if view.Summary["migrate"] != 1 || view.Summary["create"] != 0 {
		t.Errorf("summary = %v, want migrate=1 create=0", view.Summary)
	}
}

// TestRun_migrate_promotesViaV2Upsert asserts --migrate executes the
// promotion as a v2 allowMissing patch and reports the migrated op.
func TestRun_migrate_promotesViaV2Upsert(t *testing.T) {
	rt := &iapRT{}
	rc := newRC(t, rt)
	r, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: migrationCatalog(t), Migrate: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var promoteURL string
	for _, u := range rt.urls {
		if strings.Contains(u, "/onetimeproducts/old_gems?") {
			promoteURL = u
		}
	}
	if !strings.Contains(promoteURL, "allowMissing=true") {
		t.Errorf("promotion url %q must be the v2 allowMissing upsert", promoteURL)
	}
	var out bytes.Buffer
	if err := r.Renderers().Table(&out); err != nil {
		t.Fatalf("Table: %v", err)
	}
	if !strings.Contains(out.String(), "migrated old_gems (legacy → v2") {
		t.Errorf("table %q should report the migrated op", out.String())
	}
}

// TestRun_dryRun_plansWithoutMutating asserts the offer patch mask stays
// scoped and no write happens under --dry-run.
func TestRun_dryRun_plansWithoutMutating(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"coins100.json": `{"productId":"coins100","listings":[{"languageCode":"en-US","title":"Coins"}],"purchaseOptions":[{"purchaseOptionId":"buy","offers":[{"productId":"coins100","purchaseOptionId":"buy","offerId":"promo","regionalPricingAndAvailabilityConfigs":[{"regionCode":"US"},{"regionCode":"FR"}]}]}]}`,
		"old_gems.json": `{"sku":"old_gems","purchaseType":"managedUser","status":"active"}`,
	})
	rt := &iapRT{}
	rc := newRC(t, rt)
	r, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir, DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if m := rt.mutations(); len(m) != 0 {
		t.Errorf("dry-run must not mutate; got %v", m)
	}
	var js bytes.Buffer
	if err := r.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	got := js.String()
	for _, want := range []string{`"op": "patch"`, `"kind": "offer"`, `"purchaseOptionId": "buy"`, `"regionalPricingAndAvailabilityConfigs"`} {
		if !strings.Contains(got, want) {
			t.Errorf("json %s missing %s", got, want)
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
