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

	applycmd "github.com/PollyGlot/google-play-cli/commands/subscriptions/apply"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// liveCatalog is what subscriptions.list serves: premium (patchable) and gone
// (delete candidate). premium's base plan carries the output-only state the
// files never declare — the engine normalizer must keep it out of every diff.
const liveCatalog = `{"subscriptions":[
  {"productId":"premium","packageName":"com.example.app","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","state":"ACTIVE"}]},
  {"productId":"gone","packageName":"com.example.app","listings":[{"languageCode":"en-US","title":"Gone"}]}
]}`

// subsRT terminates /token, serves the live catalog on GET, echoes writes, and
// records every mutation (method + URL + body).
type subsRT struct {
	mu     sync.Mutex
	calls  []string
	urls   []string
	bodies map[string]string
}

func (r *subsRT) RoundTrip(req *http.Request) (*http.Response, error) {
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
	if req.Method == http.MethodGet {
		return jsonResp(200, liveCatalog), nil
	}
	return jsonResp(200, `{}`), nil
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

func newRC(t *testing.T, rt http.RoundTripper) (*kernel.RunContext, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	stderr := &bytes.Buffer{}
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}, Stderr: stderr}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc, stderr
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

// matchedCatalog mirrors liveCatalog exactly (as a pull would have written it).
func matchedCatalog(t *testing.T) string {
	t.Helper()
	return writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly"}]}`,
		"gone.json":    `{"productId":"gone","listings":[{"languageCode":"en-US","title":"Gone"}]}`,
	})
}

// driftedCatalog declares one new product, one edited listing (basePlans kept
// as pulled, so only listings drift), and omits "gone" (a delete).
func driftedCatalog(t *testing.T) string {
	t.Helper()
	return writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium+"}],"basePlans":[{"basePlanId":"monthly"}]}`,
		"newbie.json":  `{"productId":"newbie","listings":[{"languageCode":"en-US","title":"New"}]}`,
	})
}

// TestRun_noChanges_isNoOp asserts a catalog matching live plans nothing and
// mutates nothing — the pull-then-apply invariant.
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
	// The json contract reflects the invocation: this was a real apply, not a
	// --dry-run, so dryRun is false even though nothing ran.
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

// TestRun_dryRun_printsPlanWithoutMutating asserts --dry-run computes the full
// create/patch/delete plan online but issues no write, and the json view is
// the flat plan schema with requires.
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
	// Human view names the plan.
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

// TestRun_destructivePlan_refusesWithoutConfirm asserts a plan containing a
// delete exits 3 naming --confirm and mutates nothing.
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

// TestRun_confirmedPlan_executesInOrder asserts the confirmed plan creates with
// the regions-version pin, patches with the scoped updateMask, deletes the
// live-only product, and confirms on stderr.
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

// TestRun_basePlanPriceDrift_patchesBasePlans asserts a base-plan edit (price)
// produces a patch whose updateMask carries basePlans — while a live-only
// output-only state never does (covered by the engine normalizer): the file
// here omits state, live carries state=ACTIVE, and only the price difference
// drives the diff.
func TestRun_basePlanPriceDrift_patchesBasePlans(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"premium.json": `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly","regionalConfigs":[{"regionCode":"US","price":{"currencyCode":"USD","units":"9"}}]}]}`,
		"gone.json":    `{"productId":"gone","listings":[{"languageCode":"en-US","title":"Gone"}]}`,
	}) // file omits state; live carries state=ACTIVE — only the price drives the diff
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

// TestRun_nonDestructivePlan_runsWithoutConfirm asserts creates/patches alone
// need no --confirm (the gate is for deletes).
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

// TestRun_emptyLocalNonEmptyLive_refuses asserts the mis-pointed --dir guard:
// an empty catalog dir against a live catalog refuses (exit 2) even in
// dry-run, instead of planning a delete-everything.
func TestRun_emptyLocalNonEmptyLive_refuses(t *testing.T) {
	rt := &subsRT{}
	rc, _ := newRC(t, rt)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: t.TempDir(), DryRun: true})
	assertExit(t, err, 2)
	if !strings.Contains(err.Error(), "delete them all") {
		t.Errorf("refusal %q should explain the delete-everything risk", err.Error())
	}
}

// TestRun_missingDir_exit2 asserts a nonexistent --dir is CLI misuse with the
// pull hint, before any network call.
func TestRun_missingDir_exit2(t *testing.T) {
	rt := &subsRT{}
	rc, _ := newRC(t, rt)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: filepath.Join(t.TempDir(), "nope")})
	assertExit(t, err, 2)
	if len(rt.calls) != 0 {
		t.Errorf("must not reach the network; calls=%v", rt.calls)
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
