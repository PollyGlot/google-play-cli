package apply_test

import (
	"bytes"
	"context"
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
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// liveV2 serves one product with one purchase option (state ACTIVE): the
// normalizer keeps that state out of the product diff, planStates reconciles
// it separately when a file declares it.
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
		b := testkit.ReadBody(req)
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
// --confirm and deletes with it (offers excluded: the parent delete takes
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

// liveV2Inactive serves the same product as liveV2 with its purchase option
// INACTIVE: the seed for activation tests.
const liveV2Inactive = `{"oneTimeProducts":[
  {"productId":"coins100","packageName":"com.example.app","listings":[{"languageCode":"en-US","title":"Coins"}],"purchaseOptions":[{"purchaseOptionId":"buy","state":"INACTIVE"}]}
]}`

// TestRun_purchaseOptionState_ridesBatchUpdateStates asserts a declared
// state: ACTIVE on a live INACTIVE purchase option is one state change (no
// phantom product patch) whose body hits purchaseOptions:batchUpdateStates
// with the activate oneof, and that the unchanged offer state plans nothing.
func TestRun_purchaseOptionState_ridesBatchUpdateStates(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"coins100.json": `{"productId":"coins100","listings":[{"languageCode":"en-US","title":"Coins"}],"purchaseOptions":[{"purchaseOptionId":"buy","state":"ACTIVE","offers":[{"productId":"coins100","purchaseOptionId":"buy","offerId":"promo","state":"ACTIVE","regionalPricingAndAvailabilityConfigs":[{"regionCode":"US"}]}]}]}`,
		"old_gems.json": `{"sku":"old_gems","purchaseType":"managedUser","status":"active"}`,
	})
	rt := &iapRT{v2Body: liveV2Inactive}
	rc := newRC(t, rt)
	r, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	m := rt.mutations()
	if len(m) != 1 || !strings.HasSuffix(m[0], "/oneTimeProducts/coins100/purchaseOptions:batchUpdateStates") {
		t.Fatalf("mutations = %v, want exactly the purchase-option batchUpdateStates call", m)
	}
	body := rt.bodies[m[0]]
	want := `{"requests":[{"activatePurchaseOptionRequest":{"packageName":"com.example.app","productId":"coins100","purchaseOptionId":"buy"}}]}`
	if body != want {
		t.Errorf("batchUpdateStates body = %s, want %s", body, want)
	}
	var js bytes.Buffer
	if err := r.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	got := js.String()
	for _, frag := range []string{`"op": "activate"`, `"kind": "purchaseOption"`, `"from": "INACTIVE"`, `"to": "ACTIVE"`, `"state": 1`} {
		if !strings.Contains(got, frag) {
			t.Errorf("json %s missing %s", got, frag)
		}
	}
	if strings.Contains(got, `"op": "patch"`) {
		t.Errorf("json %s must not carry a phantom patch for the output-only state", got)
	}
}

// TestRun_offerCancel_gated asserts a declared CANCELLED offer refuses
// without --confirm (exit 3, no mutation) and calls :cancel with it.
func TestRun_offerCancel_gated(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"coins100.json": `{"productId":"coins100","listings":[{"languageCode":"en-US","title":"Coins"}],"purchaseOptions":[{"purchaseOptionId":"buy","offers":[{"productId":"coins100","purchaseOptionId":"buy","offerId":"promo","state":"CANCELLED","regionalPricingAndAvailabilityConfigs":[{"regionCode":"US"}]}]}]}`,
		"old_gems.json": `{"sku":"old_gems","purchaseType":"managedUser","status":"active"}`,
	})
	rt := &iapRT{}
	rc := newRC(t, rt)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir})
	assertExit(t, err, 3)
	if !strings.Contains(err.Error(), "--confirm") || !strings.Contains(err.Error(), "cancels 1") {
		t.Errorf("refusal %q should name --confirm and the cancel", err.Error())
	}
	if m := rt.mutations(); len(m) != 0 {
		t.Errorf("refusal must not mutate; got %v", m)
	}

	rt2 := &iapRT{}
	rc2 := newRC(t, rt2)
	r, err := applycmd.Run(rc2, applycmd.Input{Package: "com.example.app", Dir: dir, Confirm: true})
	if err != nil {
		t.Fatalf("Run --confirm: %v", err)
	}
	m := rt2.mutations()
	if len(m) != 1 || !strings.HasSuffix(m[0], "/oneTimeProducts/coins100/purchaseOptions/buy/offers/promo:cancel") {
		t.Fatalf("mutations = %v, want exactly the offer :cancel call", m)
	}
	if body := rt2.bodies[m[0]]; !strings.Contains(body, `"offerId":"promo"`) {
		t.Errorf(":cancel body %s must echo the offer identity", body)
	}
	var out bytes.Buffer
	if err := r.Renderers().Table(&out); err != nil {
		t.Fatalf("Table: %v", err)
	}
	if !strings.Contains(out.String(), "cancelled offer coins100/buy/promo (ACTIVE → CANCELLED)") {
		t.Errorf("table %q should list the cancel", out.String())
	}
}

// TestRun_stateDryRun_andOrdering asserts --dry-run lists state changes
// without sending them, and that on a real run a product created in the same
// pass is activated after its upsert (option before offer).
func TestRun_stateDryRun_andOrdering(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"coins100.json": `{"productId":"coins100","listings":[{"languageCode":"en-US","title":"Coins"}],"purchaseOptions":[{"purchaseOptionId":"buy","offers":[{"productId":"coins100","purchaseOptionId":"buy","offerId":"promo","regionalPricingAndAvailabilityConfigs":[{"regionCode":"US"}]}]}]}`,
		"gems50.json":   `{"productId":"gems50","listings":[{"languageCode":"en-US","title":"Gems"}],"purchaseOptions":[{"purchaseOptionId":"buy","state":"ACTIVE","offers":[{"offerId":"launch","state":"ACTIVE"}]}]}`,
		"old_gems.json": `{"sku":"old_gems","purchaseType":"managedUser","status":"active"}`,
	})
	rt := &iapRT{}
	rc := newRC(t, rt)
	r, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir, DryRun: true})
	if err != nil {
		t.Fatalf("Run --dry-run: %v", err)
	}
	if m := rt.mutations(); len(m) != 0 {
		t.Errorf("dry-run must not mutate; got %v", m)
	}
	var out bytes.Buffer
	if err := r.Renderers().Table(&out); err != nil {
		t.Fatalf("Table: %v", err)
	}
	for _, want := range []string{"activate purchase option gems50/buy (DRAFT → ACTIVE)", "activate offer gems50/buy/launch (DRAFT → ACTIVE)", "state=2"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("table %q missing %q", out.String(), want)
		}
	}

	rt2 := &iapRT{}
	rc2 := newRC(t, rt2)
	if _, err := applycmd.Run(rc2, applycmd.Input{Package: "com.example.app", Dir: dir}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var order []string
	for _, c := range rt2.mutations() {
		switch {
		case strings.Contains(c, "/onetimeproducts/gems50"):
			order = append(order, "upsert")
		case strings.HasSuffix(c, "/gems50/purchaseOptions/buy/offers:batchUpdate"):
			order = append(order, "offerUpsert")
		case strings.HasSuffix(c, "/gems50/purchaseOptions:batchUpdateStates"):
			order = append(order, "optionActivate")
		case strings.HasSuffix(c, "/gems50/purchaseOptions/buy/offers/launch:activate"):
			order = append(order, "offerActivate")
		}
	}
	if got := strings.Join(order, ","); got != "upsert,offerUpsert,optionActivate,offerActivate" {
		t.Errorf("mutation order = %s, want upsert,offerUpsert,optionActivate,offerActivate (calls %v)", got, rt2.mutations())
	}
}

// TestRun_unreachableState_isUsageError asserts a declared state the verbs
// cannot reach (INACTIVE on a DRAFT offer, CANCELLED on a purchase option) is
// refused before any call (exit 2).
func TestRun_unreachableState_isUsageError(t *testing.T) {
	for name, file := range map[string]string{
		"inactive from draft offer":        `{"productId":"coins100","listings":[{"languageCode":"en-US","title":"Coins"}],"purchaseOptions":[{"purchaseOptionId":"buy","offers":[{"productId":"coins100","purchaseOptionId":"buy","offerId":"promo","regionalPricingAndAvailabilityConfigs":[{"regionCode":"US"}]},{"offerId":"new","state":"INACTIVE"}]}]}`,
		"cancelled purchase option":        `{"productId":"coins100","listings":[{"languageCode":"en-US","title":"Coins"}],"purchaseOptions":[{"purchaseOptionId":"buy","state":"CANCELLED","offers":[{"productId":"coins100","purchaseOptionId":"buy","offerId":"promo","regionalPricingAndAvailabilityConfigs":[{"regionCode":"US"}]}]}]}`,
		"unknown state on purchase option": `{"productId":"coins100","listings":[{"languageCode":"en-US","title":"Coins"}],"purchaseOptions":[{"purchaseOptionId":"buy","state":"INACTIVE_PUBLISHED","offers":[{"productId":"coins100","purchaseOptionId":"buy","offerId":"promo","regionalPricingAndAvailabilityConfigs":[{"regionCode":"US"}]}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := writeCatalog(t, map[string]string{
				"coins100.json": file,
				"old_gems.json": `{"sku":"old_gems","purchaseType":"managedUser","status":"active"}`,
			})
			rt := &iapRT{}
			rc := newRC(t, rt)
			_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir, DryRun: true})
			assertExit(t, err, 2)
			if !strings.Contains(err.Error(), "state") {
				t.Errorf("error %q should name the state: field", err.Error())
			}
			if m := rt.mutations(); len(m) != 0 {
				t.Errorf("refusal must not mutate; got %v", m)
			}
		})
	}
}

// liveOffersGameReward serves the live promo offer as a game reward offer
// capped at 5 redemptions: the seed for the gameRewardOffer diff (#537).
const liveOffersGameReward = `{"oneTimeProductOffers":[
  {"packageName":"com.example.app","productId":"coins100","purchaseOptionId":"buy","offerId":"promo","state":"ACTIVE","regionalPricingAndAvailabilityConfigs":[{"regionCode":"US"}],"gameRewardOffer":{"redemptionLimit":"5"}}
]}`

// TestRun_offerGameReward_isManaged asserts gameRewardOffer sits in the
// offer projection like its discountedOffer/preOrderOffer siblings (#537): a
// changed or removed value plans an offer patch whose fields, and the
// updateMask actually sent on offers:batchUpdate, carry gameRewardOffer, while
// an identical value stays unchanged (no phantom patch).
func TestRun_offerGameReward_isManaged(t *testing.T) {
	const offerHead = `{"productId":"coins100","purchaseOptionId":"buy","offerId":"promo","state":"ACTIVE","regionalPricingAndAvailabilityConfigs":[{"regionCode":"US"}]`
	catalogWith := func(offer string) string {
		return writeCatalog(t, map[string]string{
			"coins100.json": `{"productId":"coins100","listings":[{"languageCode":"en-US","title":"Coins"}],"purchaseOptions":[{"purchaseOptionId":"buy","state":"ACTIVE","offers":[` + offer + `]}]}`,
			"old_gems.json": `{"sku":"old_gems","purchaseType":"managedUser","status":"active"}`,
		})
	}
	const batchSuffix = "/oneTimeProducts/coins100/purchaseOptions/buy/offers:batchUpdate"

	for name, tc := range map[string]struct {
		offer      string
		wantPatch  bool
		wantReward string // redemptionLimit the outgoing offer must carry; "" = field absent
	}{
		"changed":   {offer: offerHead + `,"gameRewardOffer":{"redemptionLimit":"10"}}`, wantPatch: true, wantReward: "10"},
		"removed":   {offer: offerHead + `}`, wantPatch: true},
		"unchanged": {offer: offerHead + `,"gameRewardOffer":{"redemptionLimit":"5"}}`},
	} {
		t.Run(name, func(t *testing.T) {
			rt := &iapRT{offersBody: liveOffersGameReward}
			rc := newRC(t, rt)
			r, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: catalogWith(tc.offer)})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			var js bytes.Buffer
			if err := r.Renderers().JSON(&js); err != nil {
				t.Fatalf("JSON: %v", err)
			}
			var view struct {
				Changes []struct {
					Op      string   `json:"op"`
					Kind    string   `json:"kind"`
					OfferID string   `json:"offerId"`
					Fields  []string `json:"fields"`
				} `json:"changes"`
				Summary map[string]int `json:"summary"`
			}
			if err := json.Unmarshal(js.Bytes(), &view); err != nil {
				t.Fatalf("decode plan %s: %v", js.String(), err)
			}
			m := rt.mutations()

			if !tc.wantPatch {
				if len(m) != 0 {
					t.Errorf("unchanged gameRewardOffer must not mutate; got %v", m)
				}
				if len(view.Changes) != 0 || view.Summary["offerPatch"] != 0 {
					t.Errorf("plan %s must carry no phantom offer patch", js.String())
				}
				return
			}

			if len(view.Changes) != 1 || view.Changes[0].Op != "patch" || view.Changes[0].Kind != "offer" || view.Changes[0].OfferID != "promo" ||
				strings.Join(view.Changes[0].Fields, ",") != "gameRewardOffer" {
				t.Fatalf("plan %s: want exactly one offer patch on promo with fields [gameRewardOffer]", js.String())
			}
			if len(m) != 1 || !strings.HasSuffix(m[0], batchSuffix) {
				t.Fatalf("mutations = %v, want exactly the offers:batchUpdate call", m)
			}
			var sent struct {
				Requests []struct {
					OneTimeProductOffer struct {
						OfferID         string `json:"offerId"`
						GameRewardOffer *struct {
							RedemptionLimit string `json:"redemptionLimit"`
						} `json:"gameRewardOffer"`
					} `json:"oneTimeProductOffer"`
					AllowMissing bool   `json:"allowMissing"`
					UpdateMask   string `json:"updateMask"`
				} `json:"requests"`
			}
			body := rt.bodies[m[0]]
			if err := json.Unmarshal([]byte(body), &sent); err != nil {
				t.Fatalf("decode batchUpdate body %s: %v", body, err)
			}
			if len(sent.Requests) != 1 {
				t.Fatalf("batchUpdate body %s: want one request", body)
			}
			req := sent.Requests[0]
			if req.UpdateMask != "gameRewardOffer" || req.AllowMissing || req.OneTimeProductOffer.OfferID != "promo" {
				t.Errorf("batchUpdate request %s: want updateMask=gameRewardOffer on promo, no allowMissing", body)
			}
			switch got := req.OneTimeProductOffer.GameRewardOffer; {
			case tc.wantReward == "" && got != nil:
				t.Errorf("batchUpdate body %s: a removed gameRewardOffer must be absent (the mask clears it)", body)
			case tc.wantReward != "" && (got == nil || got.RedemptionLimit != tc.wantReward):
				t.Errorf("batchUpdate body %s: want gameRewardOffer.redemptionLimit=%s", body, tc.wantReward)
			}
		})
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
