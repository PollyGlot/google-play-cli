package apply_test

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
	"sync"
	"testing"

	"golang.org/x/oauth2"

	applycmd "github.com/PollyGlot/google-play-cli/commands/subscriptions/apply"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const liveCatalog = `{"subscriptions":[
  {"productId":"premium","packageName":"com.example.app","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"ACTIVE"}]},
  {"productId":"gone","packageName":"com.example.app","listings":[{"languageCode":"en-US","title":"Gone"}]}
]}`

type subsRT struct {
	mu         sync.Mutex
	calls      []string
	urls       []string
	bodies     map[string]string
	offersBody string
	liveBody   string
	refuse     map[string]scriptedRefusal
}

type scriptedRefusal struct {
	status int
	body   string
}

func (r *subsRT) serve(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if resp, ok := testkit.TokenResponse(req); ok {
		return resp, nil
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
	for suffix, ref := range r.refuse {
		if strings.HasPrefix(suffix, req.Method+" ") && strings.HasSuffix(key, strings.TrimPrefix(suffix, req.Method+" ")) {
			return testkit.Response(ref.status, ref.body), nil
		}
	}
	if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/subscriptions/-/basePlans/-/offers") {
		body := r.offersBody
		if body == "" {
			body = `{}`
		}
		return testkit.Response(http.StatusOK, body), nil
	}
	if req.Method == http.MethodGet {
		body := r.liveBody
		if body == "" {
			body = liveCatalog
		}
		return testkit.Response(http.StatusOK, body), nil
	}
	return testkit.Response(http.StatusOK, `{}`), nil
}

func (r *subsRT) saw(method, fragment string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if strings.HasPrefix(c, method+" ") && strings.Contains(c, fragment) {
			return true
		}
	}
	return false
}

func (r *subsRT) mutations() []string {
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

func signedSAJSON(t *testing.T) []byte {
	t.Helper()
	key := testkit.RSAKey(t)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, _ := json.Marshal(map[string]any{"type": "service_account", "project_id": "p", "private_key": string(pemBytes), "client_email": "ci@p.iam.gserviceaccount.com", "token_uri": "https://oauth2.googleapis.com/token"})
	return raw
}

func newRC(t *testing.T, rt *subsRT) (*kernel.RunContext, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: testkit.RoundTripFunc(rt.serve)})
	stderr := &bytes.Buffer{}
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}, Stderr: stderr}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc, stderr
}

func newRCWithLive(t *testing.T, rt *subsRT, live string) (*kernel.RunContext, *bytes.Buffer) {
	t.Helper()
	rt.liveBody = live
	return newRC(t, rt)
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

func matchedCatalog(t *testing.T) string {
	t.Helper()
	return writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly"}]}`,
		"gone.json":    `{"productId":"gone","listings":[{"languageCode":"en-US","title":"Gone"}]}`,
	})
}

func driftedCatalog(t *testing.T) string {
	t.Helper()
	return writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium+"}],"basePlans":[{"basePlanId":"monthly"}]}`,
		"newbie.json":  `{"productId":"newbie","listings":[{"languageCode":"en-US","title":"New"}]}`,
	})
}

func TestRun_noChanges_isNoOp(t *testing.T) {
	rt := &subsRT{}
	rc, _ := newRC(t, rt)
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
	var js bytes.Buffer
	if err := r.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var view struct {
		DryRun  bool           `json:"dryRun"`
		Summary map[string]int `json:"summary"`
	}
	if err := json.Unmarshal(js.Bytes(), &view); err != nil {
		t.Fatalf("json %s: %v", js.String(), err)
	}
	if view.DryRun {
		t.Errorf("no-op real apply must report dryRun=false, got %s", js.String())
	}
	if view.Summary["unchanged"] != 2 {
		t.Errorf("summary = %v, want unchanged=2", view.Summary)
	}
}

func TestRun_dryRun_printsPlanWithoutMutating(t *testing.T) {
	rt := &subsRT{}
	rc, _ := newRC(t, rt)
	r, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: driftedCatalog(t), DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if m := rt.mutations(); len(m) != 0 {
		t.Errorf("dry-run must not mutate; got %v", m)
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var view struct {
		DryRun  bool `json:"dryRun"`
		Changes []struct {
			Op        string   `json:"op"`
			ProductID string   `json:"productId"`
			Fields    []string `json:"fields"`
		} `json:"changes"`
		Summary  map[string]int `json:"summary"`
		Requires []string       `json:"requires"`
	}
	if err := json.Unmarshal(out.Bytes(), &view); err != nil {
		t.Fatalf("json %s: %v", out.String(), err)
	}
	if !view.DryRun || len(view.Changes) != 3 {
		t.Fatalf("view = %+v, want dryRun with 3 changes", view)
	}
	if view.Summary["create"] != 1 || view.Summary["patch"] != 1 || view.Summary["delete"] != 1 {
		t.Errorf("summary = %v, want create=1 patch=1 delete=1", view.Summary)
	}
	if len(view.Requires) != 1 || view.Requires[0] != "confirm" {
		t.Errorf("requires = %v, want [confirm] (plan has a delete)", view.Requires)
	}
	var tbl bytes.Buffer
	if err := r.Renderers().Table(&tbl); err != nil {
		t.Fatalf("Table: %v", err)
	}
	got := tbl.String()
	for _, want := range []string{"plan for com.example.app", "create newbie", "patch premium (listings)", "delete gone", "requires: confirm"} {
		if !strings.Contains(got, want) {
			t.Errorf("table %q missing %q", got, want)
		}
	}
}

func TestRun_destructivePlan_refusesWithoutConfirm(t *testing.T) {
	rt := &subsRT{}
	rc, _ := newRC(t, rt)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: driftedCatalog(t)})
	assertExit(t, err, 3)
	if !strings.Contains(err.Error(), "--confirm") {
		t.Errorf("refusal %q must name --confirm", err.Error())
	}
	if m := rt.mutations(); len(m) != 0 {
		t.Errorf("refusal must not mutate; got %v", m)
	}
}

func TestRun_confirmedPlan_executesInOrder(t *testing.T) {
	rt := &subsRT{}
	rc, stderr := newRC(t, rt)
	r, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: driftedCatalog(t), Confirm: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rt.saw("POST", "/subscriptions") || !rt.saw("PATCH", "/subscriptions/premium") || !rt.saw("DELETE", "/subscriptions/gone") {
		t.Fatalf("calls = %v, want create+patch+delete", rt.calls)
	}
	var createURL, patchURL string
	for _, u := range rt.urls {
		if strings.Contains(u, "productId=newbie") {
			createURL = u
		}
		if strings.Contains(u, "/subscriptions/premium?") {
			patchURL = u
		}
	}
	if !strings.Contains(createURL, "regionsVersion.version=2022%2F02") {
		t.Errorf("create url %q must pin the regions version", createURL)
	}
	if !strings.Contains(patchURL, "updateMask=listings") || strings.Contains(patchURL, "basePlans") {
		t.Errorf("patch url %q must scope the updateMask to the changed managed fields", patchURL)
	}
	if !strings.Contains(stderr.String(), "1 created, 1 patched, 1 deleted") {
		t.Errorf("stderr %q should confirm the applied plan", stderr.String())
	}
	for _, c := range rt.calls {
		if strings.Contains(c, ":migratePrices") {
			t.Fatalf("apply must never call migratePrices; calls=%v", rt.calls)
		}
	}
	var out bytes.Buffer
	if err := r.Renderers().Table(&out); err != nil {
		t.Fatalf("Table: %v", err)
	}
	for _, want := range []string{"applied to com.example.app", "created newbie", "patched premium (listings)", "deleted gone"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("table %q missing %q", out.String(), want)
		}
	}
}

func TestRun_basePlanPriceDrift_patchesBasePlans(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","regionalConfigs":[{"regionCode":"US","price":{"currencyCode":"USD","units":"9"}}]}]}`,
		"gone.json":    `{"productId":"gone","listings":[{"languageCode":"en-US","title":"Gone"}]}`,
	})
	rt := &subsRT{}
	rc, _ := newRC(t, rt)
	if _, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var patchURL string
	for _, u := range rt.urls {
		if strings.Contains(u, "/subscriptions/premium?") {
			patchURL = u
		}
	}
	if !strings.Contains(patchURL, "updateMask=basePlans") {
		t.Errorf("patch url %q must carry basePlans in the updateMask", patchURL)
	}
}

func TestRun_nonDestructivePlan_runsWithoutConfirm(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium+"}],"basePlans":[{"basePlanId":"monthly"}]}`,
		"gone.json":    `{"productId":"gone","listings":[{"languageCode":"en-US","title":"Gone"}]}`,
	})
	rt := &subsRT{}
	rc, _ := newRC(t, rt)
	if _, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rt.saw("PATCH", "/subscriptions/premium") {
		t.Errorf("calls = %v, want the patch to run without --confirm", rt.calls)
	}
}

const liveOffers = `{"subscriptionOffers":[
  {"packageName":"com.example.app","productId":"premium","basePlanId":"monthly","offerId":"intro","state":"ACTIVE","phases":[{"duration":"P1W"}]}
]}`

func offersCatalog(t *testing.T, introFragment string) string {
	t.Helper()
	return writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"ACTIVE","offers":[` + introFragment + `]}]}`,
		"gone.json":    `{"productId":"gone","listings":[{"languageCode":"en-US","title":"Gone"}]}`,
	})
}

const introAsPulled = `{"productId":"premium","basePlanId":"monthly","offerId":"intro","state":"ACTIVE","phases":[{"duration":"P1W"}]}`

func TestRun_offersMatch_isNoOp(t *testing.T) {
	rt := &subsRT{offersBody: liveOffers}
	rc, _ := newRC(t, rt)
	r, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: offersCatalog(t, introAsPulled)})
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

func TestRun_offerPatch_scopedMask(t *testing.T) {
	edited := `{"productId":"premium","basePlanId":"monthly","offerId":"intro","state":"ACTIVE","phases":[{"duration":"P2W"}]}`
	rt := &subsRT{offersBody: liveOffers}
	rc, _ := newRC(t, rt)
	if _, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: offersCatalog(t, edited)}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var patchURL string
	for _, u := range rt.urls {
		if strings.Contains(u, "/offers/intro?") {
			patchURL = u
		}
	}
	if !strings.Contains(patchURL, "updateMask=phases") || strings.Contains(patchURL, "state") {
		t.Errorf("offer patch url %q must scope the mask to phases", patchURL)
	}
}

func TestRun_offerCreateAndStates(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"ACTIVE","offers":[
			{"productId":"premium","basePlanId":"monthly","offerId":"intro","state":"INACTIVE","phases":[{"duration":"P1W"}]},
			{"offerId":"neo","phases":[{"duration":"P3D"}]}
		]},{"basePlanId":"yearly","state":"ACTIVE"}]}`,
		"gone.json": `{"productId":"gone","listings":[{"languageCode":"en-US","title":"Gone"}]}`,
	})
	rt := &subsRT{offersBody: liveOffers}
	live := `{"subscriptions":[
	  {"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"ACTIVE"},{"basePlanId":"yearly","state":"DRAFT"}]},
	  {"productId":"gone","listings":[{"languageCode":"en-US","title":"Gone"}]}
	]}`
	rc, _ := newRCWithLive(t, rt, live)
	r, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rt.saw("POST", "/offers") || !rt.saw("POST", "/basePlans/yearly:activate") || !rt.saw("POST", "/offers/intro:deactivate") {
		t.Fatalf("calls = %v, want offer create + yearly activate + intro deactivate", rt.calls)
	}
	var createURL string
	for _, u := range rt.urls {
		if strings.Contains(u, "offerId=neo") {
			createURL = u
		}
	}
	if !strings.Contains(createURL, "/subscriptions/premium/basePlans/monthly/offers?") {
		t.Errorf("offer create url %q must address premium/monthly", createURL)
	}
	var js bytes.Buffer
	if err := r.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	got := js.String()
	for _, want := range []string{`"op": "activate"`, `"op": "deactivate"`, `"kind": "offer"`, `"from": "DRAFT"`} {
		if !strings.Contains(got, want) {
			t.Errorf("json %s missing %s", got, want)
		}
	}
}

func liveOfferIn(state string) string {
	return `{"subscriptionOffers":[
  {"packageName":"com.example.app","productId":"premium","basePlanId":"monthly","offerId":"intro","state":"` + state + `","phases":[{"duration":"P1W"}]}
]}`
}

func droppedIntroCatalog(t *testing.T) string {
	t.Helper()
	return writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"ACTIVE"}]}`,
		"gone.json":    `{"productId":"gone","listings":[{"languageCode":"en-US","title":"Gone"}]}`,
	})
}

func TestRun_offerDelete_gated(t *testing.T) {
	dir := droppedIntroCatalog(t)
	rt := &subsRT{offersBody: liveOfferIn("DRAFT")}
	rc, _ := newRC(t, rt)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir})
	assertExit(t, err, 3)
	if m := rt.mutations(); len(m) != 0 {
		t.Errorf("refusal must not mutate; got %v", m)
	}

	rt2 := &subsRT{offersBody: liveOfferIn("DRAFT")}
	rc2, _ := newRC(t, rt2)
	if _, err := applycmd.Run(rc2, applycmd.Input{Package: "com.example.app", Dir: dir, Confirm: true}); err != nil {
		t.Fatalf("Run --confirm: %v", err)
	}
	if !rt2.saw("DELETE", "/offers/intro") {
		t.Errorf("calls = %v, want the offer DELETE", rt2.calls)
	}
}

func TestRun_offerDelete_active_refusedAtPlan(t *testing.T) {
	dir := droppedIntroCatalog(t)
	for _, in := range []applycmd.Input{
		{Package: "com.example.app", Dir: dir, DryRun: true},
		{Package: "com.example.app", Dir: dir, Confirm: true},
	} {
		rt := &subsRT{offersBody: liveOfferIn("ACTIVE")}
		rc, _ := newRC(t, rt)
		_, err := applycmd.Run(rc, in)
		assertExit(t, err, 2)
		if m := rt.mutations(); len(m) != 0 {
			t.Errorf("dryRun=%v: refusal must precede every write; got %v", in.DryRun, m)
		}
		for _, want := range []string{"1 ACTIVE offer(s)", "premium/monthly/intro", "keep the offer", "state: INACTIVE"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("dryRun=%v: error %q missing %q", in.DryRun, err.Error(), want)
			}
		}
	}
}

func TestRun_offerDelete_inactive_acceptedByAPI(t *testing.T) {
	rt := &subsRT{offersBody: liveOfferIn("INACTIVE")}
	rc, _ := newRC(t, rt)
	if _, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: droppedIntroCatalog(t), Confirm: true}); err != nil {
		t.Fatalf("Run --confirm: %v", err)
	}
	if !rt.saw("DELETE", "/offers/intro") {
		t.Errorf("calls = %v, want the INACTIVE offer DELETE", rt.calls)
	}
}

func TestRun_offerDelete_refusedByAPI_continues(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"ACTIVE"}]}`,
	})
	rt := &subsRT{offersBody: liveOfferIn("INACTIVE"), refuse: map[string]scriptedRefusal{
		"DELETE /offers/intro": {400, `{"error":{"code":400,"message":"Offer is not in draft state","errors":[{"reason":"badRequest"}]}}`},
	}}
	rc, stderr := newRC(t, rt)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir, Confirm: true})
	assertExit(t, err, 30)
	if !rt.saw("DELETE", "/offers/intro") || !rt.saw("DELETE", "/subscriptions/gone") {
		t.Fatalf("calls = %v, want the refused offer DELETE and the subscription DELETE that follows it", rt.calls)
	}
	d := exit.Classify(err)
	if d.Code != exit.CodeOfferNotDraft || d.ExitCode != 30 || d.Retryable || d.Operation != "monetization.subscriptions.basePlans.offers.delete" || d.Package != "com.example.app" {
		t.Errorf("diagnostic = %+v, want OFFER_NOT_DRAFT (exit 30, not retryable) on offers.delete for the package", d)
	}
	if len(d.Reasons) != 1 || d.Reasons[0] != "badRequest" {
		t.Errorf("reasons = %v, want the verbatim upstream reason", d.Reasons)
	}
	for _, want := range []string{"premium/monthly/intro", "DRAFT", "keep the offer", "state: INACTIVE", "rest of the plan was applied"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
	if !strings.Contains(stderr.String(), "premium/monthly/intro") {
		t.Errorf("stderr %q should warn about the refused offer delete", stderr.String())
	}
}

func TestRun_offerDelete_notFound_aborts(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"ACTIVE"}]}`,
	})
	rt := &subsRT{offersBody: liveOfferIn("INACTIVE"), refuse: map[string]scriptedRefusal{
		"DELETE /offers/intro": {404, `{"error":{"code":404,"message":"Offer not found","errors":[{"reason":"notFound"}]}}`},
	}}
	rc, _ := newRC(t, rt)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir, Confirm: true})
	assertExit(t, err, 30)
	if d := exit.Classify(err); d.Code != exit.CodeNotFound {
		t.Errorf("diagnostic = %+v, want NOT_FOUND", d)
	}
	assertNamesTarget(t, err, `product "premium" base plan "monthly" offer "intro"`)
	if rt.saw("DELETE", "/subscriptions/gone") {
		t.Errorf("calls = %v, a 404 must abort before the subscription delete", rt.calls)
	}
}

func TestRun_offerDelete_followingHint_converges(t *testing.T) {
	kept := offersCatalog(t, `{"productId":"premium","basePlanId":"monthly","offerId":"intro","state":"INACTIVE","phases":[{"duration":"P1W"}]}`)

	rt := &subsRT{offersBody: liveOfferIn("ACTIVE")}
	rc, _ := newRC(t, rt)
	if _, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: kept}); err != nil {
		t.Fatalf("run following the hint from ACTIVE: %v", err)
	}
	if m := rt.mutations(); len(m) != 1 || !strings.HasSuffix(m[0], "/offers/intro:deactivate") {
		t.Errorf("mutations = %v, want the single offer deactivate", m)
	}

	rt2 := &subsRT{offersBody: liveOfferIn("INACTIVE")}
	rc2, _ := newRC(t, rt2)
	r, err := applycmd.Run(rc2, applycmd.Input{Package: "com.example.app", Dir: kept})
	if err != nil {
		t.Fatalf("run on the INACTIVE offer: %v", err)
	}
	if m := rt2.mutations(); len(m) != 0 {
		t.Errorf("run on the INACTIVE offer must not write; got %v", m)
	}
	if p := r.(applycmd.Payload); p.Plan.HasChanges() {
		t.Errorf("run on the INACTIVE offer = %+v, want no changes", p.Plan)
	}
}

func TestRun_impossibleStateTransition_exit2(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"INACTIVE"}]}`,
		"gone.json":    `{"productId":"gone","listings":[{"languageCode":"en-US","title":"Gone"}]}`,
	})
	live := `{"subscriptions":[
	  {"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"DRAFT"}]},
	  {"productId":"gone","listings":[{"languageCode":"en-US","title":"Gone"}]}
	]}`
	rt := &subsRT{}
	rc, _ := newRCWithLive(t, rt, live)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir, DryRun: true})
	assertExit(t, err, 2)
	if !strings.Contains(err.Error(), "INACTIVE") {
		t.Errorf("refusal %q should name the impossible transition", err.Error())
	}
}

func TestRun_emptyLocalNonEmptyLive_refuses(t *testing.T) {
	rt := &subsRT{}
	rc, _ := newRC(t, rt)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: t.TempDir(), DryRun: true})
	assertExit(t, err, 2)
	if !strings.Contains(err.Error(), "delete them all") {
		t.Errorf("refusal %q should explain the delete-everything risk", err.Error())
	}
}

const liveWithDraftPlan = `{"subscriptions":[
  {"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"ACTIVE"},{"basePlanId":"trial","state":"DRAFT"}]},
  {"productId":"gone","listings":[{"languageCode":"en-US","title":"Gone"}]}
]}`

func droppedTrialCatalog(t *testing.T) string {
	t.Helper()
	return writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"ACTIVE"}]}`,
		"gone.json":    `{"productId":"gone","listings":[{"languageCode":"en-US","title":"Gone"}]}`,
	})
}

func TestRun_basePlanDelete_plannedAndGated(t *testing.T) {
	dir := droppedTrialCatalog(t)

	rt := &subsRT{}
	rc, _ := newRCWithLive(t, rt, liveWithDraftPlan)
	r, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir, DryRun: true})
	if err != nil {
		t.Fatalf("Run --dry-run: %v", err)
	}
	if m := rt.mutations(); len(m) != 0 {
		t.Errorf("dry-run must not mutate; got %v", m)
	}
	var js bytes.Buffer
	if err := r.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var view struct {
		Changes []struct {
			Op         string `json:"op"`
			Kind       string `json:"kind"`
			ProductID  string `json:"productId"`
			BasePlanID string `json:"basePlanId"`
		} `json:"changes"`
		Summary  map[string]int `json:"summary"`
		Requires []string       `json:"requires"`
	}
	if err := json.Unmarshal(js.Bytes(), &view); err != nil {
		t.Fatalf("json %s: %v", js.String(), err)
	}
	var sawDelete bool
	for _, c := range view.Changes {
		if c.Op == "delete" && c.Kind == "basePlan" && c.ProductID == "premium" && c.BasePlanID == "trial" {
			sawDelete = true
		}
	}
	if !sawDelete || view.Summary["basePlanDelete"] != 1 || view.Summary["delete"] != 0 {
		t.Errorf("json %s must carry one basePlan delete of premium/trial and no subscription delete", js.String())
	}
	if len(view.Requires) != 1 || view.Requires[0] != "confirm" {
		t.Errorf("requires = %v, want [confirm]", view.Requires)
	}
	var tbl bytes.Buffer
	if err := r.Renderers().Table(&tbl); err != nil {
		t.Fatalf("Table: %v", err)
	}
	if !strings.Contains(tbl.String(), "delete base plan premium/trial") {
		t.Errorf("table %q must list the base plan delete", tbl.String())
	}

	rt2 := &subsRT{}
	rc2, _ := newRCWithLive(t, rt2, liveWithDraftPlan)
	_, err = applycmd.Run(rc2, applycmd.Input{Package: "com.example.app", Dir: dir})
	assertExit(t, err, 3)
	if !strings.Contains(err.Error(), "1 base plan(s)") {
		t.Errorf("refusal %q must count the base plan delete", err.Error())
	}
	if m := rt2.mutations(); len(m) != 0 {
		t.Errorf("refusal must not mutate; got %v", m)
	}

	rt3 := &subsRT{}
	rc3, stderr := newRCWithLive(t, rt3, liveWithDraftPlan)
	r3, err := applycmd.Run(rc3, applycmd.Input{Package: "com.example.app", Dir: dir, Confirm: true})
	if err != nil {
		t.Fatalf("Run --confirm: %v", err)
	}
	m := rt3.mutations()
	if len(m) != 2 || m[0] != "PATCH /androidpublisher/v3/applications/com.example.app/subscriptions/premium" || m[1] != "DELETE /androidpublisher/v3/applications/com.example.app/subscriptions/premium/basePlans/trial" {
		t.Fatalf("mutations = %v, want the parent patch then the base plan DELETE", m)
	}
	if !strings.Contains(stderr.String(), "1 base plan(s) deleted") {
		t.Errorf("stderr %q should confirm the base plan delete", stderr.String())
	}
	var out bytes.Buffer
	if err := r3.Renderers().Table(&out); err != nil {
		t.Fatalf("Table: %v", err)
	}
	if !strings.Contains(out.String(), "deleted base plan premium/trial") {
		t.Errorf("table %q must report the deleted base plan", out.String())
	}
}

func TestRun_basePlanDelete_refusedByAPI_continues(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"ACTIVE"}]}`,
	})
	rt := &subsRT{refuse: map[string]scriptedRefusal{
		"DELETE /subscriptions/premium/basePlans/trial": {400, `{"error":{"code":400,"message":"Base plan is not in draft state","errors":[{"reason":"badRequest"}]}}`},
	}}
	rc, stderr := newRCWithLive(t, rt, liveWithDraftPlan)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir, Confirm: true})
	assertExit(t, err, 30)
	if !rt.saw("DELETE", "/basePlans/trial") || !rt.saw("DELETE", "/subscriptions/gone") {
		t.Fatalf("calls = %v, want the refused base plan DELETE and the subscription DELETE that follows it", rt.calls)
	}
	d := exit.Classify(err)
	if d.Code != exit.CodeBasePlanNotDraft || d.ExitCode != 30 || d.Retryable || d.Operation != "monetization.subscriptions.basePlans.delete" || d.Package != "com.example.app" {
		t.Errorf("diagnostic = %+v, want BASE_PLAN_NOT_DRAFT (exit 30, not retryable) on basePlans.delete for the package", d)
	}
	if len(d.Reasons) != 1 || d.Reasons[0] != "badRequest" {
		t.Errorf("reasons = %v, want the verbatim upstream reason", d.Reasons)
	}
	for _, want := range []string{"premium/trial", "DRAFT", "keep the base plan", "state: INACTIVE", "rest of the plan was applied"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
	if !strings.Contains(stderr.String(), "premium/trial") {
		t.Errorf("stderr %q should warn about the refused base plan delete", stderr.String())
	}
}

func TestRun_basePlanDelete_notFound_keepsNotFound(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"ACTIVE"}]}`,
	})
	rt := &subsRT{refuse: map[string]scriptedRefusal{
		"DELETE /subscriptions/premium/basePlans/trial": {404, `{"error":{"code":404,"message":"Base plan not found","errors":[{"reason":"notFound"}]}}`},
	}}
	rc, _ := newRCWithLive(t, rt, liveWithDraftPlan)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir, Confirm: true})
	assertExit(t, err, 30)
	if d := exit.Classify(err); d.Code != exit.CodeNotFound || d.Operation != "monetization.subscriptions.basePlans.delete" {
		t.Errorf("diagnostic = %+v, want NOT_FOUND on basePlans.delete", d)
	}
	assertNamesTarget(t, err, `product "premium" base plan "trial"`)
	if rt.saw("DELETE", "/subscriptions/gone") {
		t.Errorf("calls = %v, a 404 must abort before the subscription delete", rt.calls)
	}
}

func TestRun_basePlanDelete_forbidden_keepsPermissionDenied(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"ACTIVE"}]}`,
	})
	rt := &subsRT{refuse: map[string]scriptedRefusal{
		"DELETE /subscriptions/premium/basePlans/trial": {403, `{"error":{"code":403,"message":"forbidden","errors":[{"reason":"forbidden"}]}}`},
	}}
	rc, _ := newRCWithLive(t, rt, liveWithDraftPlan)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir, Confirm: true})
	assertExit(t, err, 11)
	if d := exit.Classify(err); d.Code != exit.CodePermissionDenied {
		t.Errorf("diagnostic = %+v, want PERMISSION_DENIED on a 403", d)
	}
	if !rt.saw("DELETE", "/subscriptions/gone") {
		t.Errorf("calls = %v, a 403 refusal must still continue with the subscription delete", rt.calls)
	}
}

func TestRun_mixedDeleteRefusals_permissionWins(t *testing.T) {
	const (
		badRequest = `{"error":{"code":400,"message":"not in draft state","errors":[{"reason":"badRequest"}]}}`
		forbidden  = `{"error":{"code":403,"message":"forbidden","errors":[{"reason":"forbidden"}]}}`
	)
	for _, tc := range []struct {
		name          string
		offer, plan   scriptedRefusal
		wantOperation string
		wantInMessage string
	}{
		{"offer400ThenPlan403", scriptedRefusal{400, badRequest}, scriptedRefusal{403, forbidden}, "monetization.subscriptions.basePlans.delete", "premium/trial"},
		{"offer403ThenPlan400", scriptedRefusal{403, forbidden}, scriptedRefusal{400, badRequest}, "monetization.subscriptions.basePlans.offers.delete", "premium/monthly/intro"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeCatalog(t, map[string]string{
				"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"ACTIVE"}]}`,
			})
			rt := &subsRT{offersBody: liveOfferIn("INACTIVE"), refuse: map[string]scriptedRefusal{
				"DELETE /offers/intro":                          tc.offer,
				"DELETE /subscriptions/premium/basePlans/trial": tc.plan,
			}}
			rc, stderr := newRCWithLive(t, rt, liveWithDraftPlan)
			_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir, Confirm: true})
			assertExit(t, err, 11)
			if !rt.saw("DELETE", "/offers/intro") || !rt.saw("DELETE", "/basePlans/trial") || !rt.saw("DELETE", "/subscriptions/gone") {
				t.Fatalf("calls = %v, want both refused DELETEs and the subscription DELETE", rt.calls)
			}
			if d := exit.Classify(err); d.Code != exit.CodePermissionDenied || d.Operation != tc.wantOperation {
				t.Errorf("diagnostic = %+v, want PERMISSION_DENIED on %s", d, tc.wantOperation)
			}
			for _, want := range []string{tc.wantInMessage, "1 more delete(s) were refused"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q missing %q", err.Error(), want)
				}
			}
			for _, want := range []string{"premium/monthly/intro", "premium/trial"} {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr %q should warn about %s", stderr.String(), want)
				}
			}
		})
	}
}

func TestRun_basePlanDelete_serverError_aborts(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"ACTIVE"}]}`,
	})
	rt := &subsRT{refuse: map[string]scriptedRefusal{
		"DELETE /subscriptions/premium/basePlans/trial": {503, `{"error":{"code":503,"message":"backend error"}}`},
	}}
	rc, _ := newRCWithLive(t, rt, liveWithDraftPlan)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir, Confirm: true})
	assertExit(t, err, 40)
	if rt.saw("DELETE", "/subscriptions/gone") {
		t.Errorf("calls = %v, a 5xx must abort before the subscription delete", rt.calls)
	}
}

const liveWithPublishedPlans = `{"subscriptions":[
  {"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"ACTIVE"},{"basePlanId":"yearly","state":"INACTIVE"}]}
]}`

func TestRun_basePlanDelete_active_refusedAtPlan(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}]}`,
	})
	for _, in := range []applycmd.Input{
		{Package: "com.example.app", Dir: dir, DryRun: true},
		{Package: "com.example.app", Dir: dir, Confirm: true},
	} {
		rt := &subsRT{}
		rc, _ := newRCWithLive(t, rt, liveWithPublishedPlans)
		_, err := applycmd.Run(rc, in)
		assertExit(t, err, 2)
		if m := rt.mutations(); len(m) != 0 {
			t.Errorf("dryRun=%v: refusal must precede every write; got %v", in.DryRun, m)
		}
		for _, want := range []string{"1 ACTIVE base plan(s)", "premium/monthly", "keep the base plan", "state: INACTIVE"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("dryRun=%v: error %q missing %q", in.DryRun, err.Error(), want)
			}
		}
		for _, banned := range []string{"premium/yearly", "then remove it"} {
			if strings.Contains(err.Error(), banned) {
				t.Errorf("dryRun=%v: error %q must not carry %q", in.DryRun, err.Error(), banned)
			}
		}
	}
}

const liveWithInactivePlan = `{"subscriptions":[
  {"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"ACTIVE"},{"basePlanId":"yearly","state":"INACTIVE"}]},
  {"productId":"gone","listings":[{"languageCode":"en-US","title":"Gone"}]}
]}`

func droppedInactiveCatalog(t *testing.T) string {
	t.Helper()
	return writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"ACTIVE"}]}`,
	})
}

func TestRun_basePlanDelete_inactive_acceptedByAPI(t *testing.T) {
	rt := &subsRT{}
	rc, _ := newRCWithLive(t, rt, liveWithInactivePlan)
	if _, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: droppedInactiveCatalog(t), Confirm: true}); err != nil {
		t.Fatalf("Run --confirm: %v", err)
	}
	if !rt.saw("DELETE", "/subscriptions/premium/basePlans/yearly") || !rt.saw("DELETE", "/subscriptions/gone") {
		t.Errorf("calls = %v, want the INACTIVE base plan DELETE and the subscription DELETE", rt.calls)
	}
}

func TestRun_basePlanDelete_inactive_refusedByAPI(t *testing.T) {
	rt := &subsRT{refuse: map[string]scriptedRefusal{
		"DELETE /subscriptions/premium/basePlans/yearly": {400, `{"error":{"code":400,"message":"Base plan is not in draft state","errors":[{"reason":"badRequest"}]}}`},
	}}
	rc, _ := newRCWithLive(t, rt, liveWithInactivePlan)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: droppedInactiveCatalog(t), Confirm: true})
	assertExit(t, err, 30)
	if d := exit.Classify(err); d.Code != exit.CodeBasePlanNotDraft || d.Operation != "monetization.subscriptions.basePlans.delete" {
		t.Errorf("diagnostic = %+v, want BASE_PLAN_NOT_DRAFT on basePlans.delete", d)
	}
	if !rt.saw("DELETE", "/subscriptions/gone") {
		t.Errorf("calls = %v, the rest of the plan must still run", rt.calls)
	}
	for _, want := range []string{"premium/yearly", "keep the base plan", "state: INACTIVE", "rest of the plan was applied"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestRun_basePlanDelete_followingHint_converges(t *testing.T) {
	const liveActive = `{"subscriptions":[
  {"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"ACTIVE"}]}
]}`
	const liveInactive = `{"subscriptions":[
  {"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"INACTIVE"}]}
]}`

	dropped := writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}]}`,
	})
	rt := &subsRT{}
	rc, _ := newRCWithLive(t, rt, liveActive)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dropped, Confirm: true})
	assertExit(t, err, 2)

	refusing := &subsRT{refuse: map[string]scriptedRefusal{
		"DELETE /subscriptions/premium/basePlans/monthly": {400, `{"error":{"code":400,"message":"Base plan is not in draft state","errors":[{"reason":"badRequest"}]}}`},
	}}
	rcR, _ := newRCWithLive(t, refusing, liveInactive)
	_, err = applycmd.Run(rcR, applycmd.Input{Package: "com.example.app", Dir: dropped, Confirm: true})
	assertExit(t, err, 30)

	kept := writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"INACTIVE"}]}`,
	})
	rt2 := &subsRT{}
	rc2, _ := newRCWithLive(t, rt2, liveActive)
	if _, err := applycmd.Run(rc2, applycmd.Input{Package: "com.example.app", Dir: kept}); err != nil {
		t.Fatalf("run following the hint: %v", err)
	}
	if m := rt2.mutations(); len(m) != 1 || !strings.HasSuffix(m[0], "/basePlans/monthly:deactivate") {
		t.Fatalf("mutations = %v, want the monthly deactivation alone", m)
	}

	rt3 := &subsRT{}
	rc3, _ := newRCWithLive(t, rt3, liveInactive)
	r, err := applycmd.Run(rc3, applycmd.Input{Package: "com.example.app", Dir: kept})
	if err != nil {
		t.Fatalf("run on the INACTIVE plan: %v", err)
	}
	if m := rt3.mutations(); len(m) != 0 {
		t.Errorf("run on the INACTIVE plan must not write; got %v", m)
	}
	if p := r.(applycmd.Payload); p.Plan.HasChanges() {
		t.Errorf("run on the INACTIVE plan = %+v, want no changes", p.Plan)
	}
}

func TestRun_missingDir_exit2(t *testing.T) {
	rt := &subsRT{}
	rc, _ := newRC(t, rt)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: filepath.Join(t.TempDir(), "nope")})
	assertExit(t, err, 2)
	if len(rt.calls) != 0 {
		t.Errorf("must not reach the network; calls=%v", rt.calls)
	}
}

func TestRun_productDelete_notFound_namesProduct(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"ACTIVE"}]}`,
	})
	rt := &subsRT{refuse: map[string]scriptedRefusal{
		"DELETE /subscriptions/gone": {404, `{"error":{"code":404,"message":"Subscription not found","errors":[{"reason":"notFound"}]}}`},
	}}
	rc, _ := newRC(t, rt)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir, Confirm: true})
	assertExit(t, err, 30)
	if d := exit.Classify(err); d.Code != exit.CodeNotFound {
		t.Errorf("diagnostic = %+v, want NOT_FOUND", d)
	}
	assertNamesTarget(t, err, `product "gone"`)
}

func TestRun_offerStateChange_notFound_namesOffer(t *testing.T) {
	dir := offersCatalog(t, `{"productId":"premium","basePlanId":"monthly","offerId":"intro","state":"INACTIVE","phases":[{"duration":"P1W"}]}`)
	rt := &subsRT{offersBody: liveOfferIn("ACTIVE"), refuse: map[string]scriptedRefusal{
		"POST /offers/intro:deactivate": {404, `{"error":{"code":404,"message":"Offer not found","errors":[{"reason":"notFound"}]}}`},
	}}
	rc, _ := newRC(t, rt)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir})
	assertExit(t, err, 30)
	assertNamesTarget(t, err, `product "premium" base plan "monthly" offer "intro"`)
}

func TestRun_listNotFound_blamesThePackage(t *testing.T) {
	rt := &subsRT{refuse: map[string]scriptedRefusal{
		"GET /applications/com.example.app/subscriptions": {404, `{"error":{"code":404,"message":"Package not found","errors":[{"reason":"notFound"}]}}`},
	}}
	rc, _ := newRC(t, rt)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: driftedCatalog(t), Confirm: true})
	assertExit(t, err, 30)
	if d := exit.Classify(err); d.Code != exit.CodeNotFound {
		t.Errorf("diagnostic = %+v, want NOT_FOUND", d)
	}
	if want := `package "com.example.app" not found`; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q should say %s", err.Error(), want)
	}
}

func assertNamesTarget(t *testing.T, err error, target string) {
	t.Helper()
	msg := err.Error()
	if want := "subscription " + target + ` not found in package "com.example.app"`; !strings.Contains(msg, want) {
		t.Errorf("error %q should name %s", msg, want)
	}
	if strings.Contains(msg, `package "com.example.app" not found`) {
		t.Errorf("error %q blames the package for a missing resource", msg)
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

func TestRun_latencyTolerance_ridesThePatchesAndStates(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium+"}],"basePlans":[{"basePlanId":"monthly","state":"ACTIVE","offers":[
			{"productId":"premium","basePlanId":"monthly","offerId":"intro","state":"INACTIVE","phases":[{"duration":"P1W"}]},
			{"offerId":"neo","phases":[{"duration":"P3D"}]}
		]},{"basePlanId":"yearly","state":"ACTIVE"}]}`,
		"gone.json": `{"productId":"gone","listings":[{"languageCode":"en-US","title":"Gone"}]}`,
	})
	rt := &subsRT{offersBody: liveOffers}
	live := `{"subscriptions":[
	  {"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"ACTIVE"},{"basePlanId":"yearly","state":"DRAFT"}]},
	  {"productId":"gone","listings":[{"languageCode":"en-US","title":"Gone"}]}
	]}`
	rc, _ := newRCWithLive(t, rt, live)
	if _, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir, LatencyTolerance: "TOLERANT"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	const tolerant = "PRODUCT_UPDATE_LATENCY_TOLERANCE_LATENCY_TOLERANT"
	seen := map[string]bool{}
	for i, u := range rt.urls {
		body := rt.bodies[rt.calls[i]]
		switch {
		case strings.Contains(u, "/subscriptions/premium?"):
			seen["patch"] = strings.Contains(u, "latencyTolerance="+tolerant)
		case strings.Contains(u, "offerId=neo"):
			seen["create"] = !strings.Contains(u+body, "latencyTolerance")
		case strings.Contains(u, "/basePlans/yearly:activate"):
			seen["activate"] = body == `{"latencyTolerance":"`+tolerant+`"}`
		case strings.Contains(u, "/offers/intro:deactivate"):
			seen["deactivate"] = body == `{"latencyTolerance":"`+tolerant+`"}`
		}
	}
	for _, w := range []string{"patch", "create", "activate", "deactivate"} {
		if !seen[w] {
			t.Errorf("%s: latencyTolerance not where expected; calls %v urls %v bodies %v", w, rt.calls, rt.urls, rt.bodies)
		}
	}
}

func TestRun_latencyToleranceUnknown_isUsageErrorBeforeAnyCall(t *testing.T) {
	rt := &subsRT{}
	rc, _ := newRC(t, rt)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: matchedCatalog(t), LatencyTolerance: "fast"})
	assertExit(t, err, 2)
	if len(rt.calls) != 0 {
		t.Errorf("refused input reached the API: %v", rt.calls)
	}
}

func TestRun_malformedCatalogFile_isUsageErrorBeforeAnyCall(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"invalid JSON":       {"x.json": `{not json`},
		"productId mismatch": {"premium.json": `{"productId":"other"}`},
		"empty stem":         {".json": `{}`},
	} {
		t.Run(name, func(t *testing.T) {
			rt := &subsRT{}
			rc, _ := newRC(t, rt)
			_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: writeCatalog(t, files)})
			assertExit(t, err, 2)
			if len(rt.calls) != 0 {
				t.Errorf("refused catalog reached the API: %v", rt.calls)
			}
		})
	}
}

func TestRun_danglingCatalogFile_namesTheFileNotTheDirectory(t *testing.T) {
	dir := writeCatalog(t, map[string]string{"premium.json": `{"productId":"premium"}`})
	if err := os.Symlink(filepath.Join(dir, "gone.json"), filepath.Join(dir, "gold.json")); err != nil {
		t.Fatal(err)
	}
	rt := &subsRT{}
	rc, _ := newRC(t, rt)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir})
	assertExit(t, err, 20)
	if !strings.Contains(err.Error(), "gold.json") || strings.Contains(err.Error(), "does not exist: run") {
		t.Errorf("err %q should name the dangling file, not call the directory missing", err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("refused catalog reached the API: %v", rt.calls)
	}
}
