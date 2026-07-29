// Package apply_test drives `gplay metadata apply` at the kernel level: a
// RunContext built by hand, a RoundTripper injected via the
// oauth2.HTTPClient context key (so one transport covers /token + the
// androidpublisher calls), and Run invoked directly. The local tree is
// written into t.TempDir() and passed via Input.Dir.
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
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/metadata/apply"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/metadata/tree"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// applyRT terminates the /token exchange and routes the apply sequence,
// recording every request line and each PATCH body.
type applyRT struct {
	t            *testing.T
	editID       string
	listingsBody string
	detailsLang  string

	mu        sync.Mutex
	calls     []string
	patchBody map[string]string
	tokenHits int
}

func (r *applyRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.patchBody == nil {
		r.patchBody = map[string]string{}
	}
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.tokenHits++
		r.calls = append(r.calls, "POST /token")
		return jsonResp(200, `{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	}
	path := req.URL.Path
	r.calls = append(r.calls, req.Method+" "+path)

	switch {
	case req.Method == http.MethodPost && strings.HasSuffix(path, ":commit"):
		return jsonResp(200, `{}`), nil
	case req.Method == http.MethodPost && strings.HasSuffix(path, "/edits"):
		return jsonResp(200, `{"id":"`+r.editID+`","expiryTimeSeconds":"1700000000"}`), nil
	case req.Method == http.MethodGet && strings.HasSuffix(path, "/listings"):
		return jsonResp(200, r.listingsBody), nil
	case req.Method == http.MethodGet && strings.HasSuffix(path, "/details"):
		return jsonResp(200, `{"defaultLanguage":"`+r.detailsLang+`","contactEmail":"x@y.z"}`), nil
	case req.Method == http.MethodPatch && strings.Contains(path, "/listings/"):
		loc := path[strings.LastIndex(path, "/")+1:]
		b, _ := io.ReadAll(req.Body)
		r.patchBody[loc] = string(b)
		return jsonResp(200, `{"language":"`+loc+`","title":"echo"}`), nil
	case req.Method == http.MethodDelete && strings.Contains(path, "/listings/"):
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	case req.Method == http.MethodDelete && strings.Contains(path, "/edits/"):
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	r.t.Fatalf("unexpected request: %s %s", req.Method, path)
	return nil, nil
}

func (r *applyRT) saw(method, substr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if strings.HasPrefix(c, method+" ") && strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func signedSAJSON(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, err := json.Marshal(map[string]any{
		"type":         "service_account",
		"project_id":   "test-proj",
		"private_key":  string(pemBytes),
		"client_email": "playci@test-proj.iam.gserviceaccount.com",
		"token_uri":    "https://oauth2.googleapis.com/token",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func newRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc
}

func exitCodeOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) {
		t.Fatalf("err %v (%T) has no ExitCode()", err, err)
	}
	return c.ExitCode()
}

// writeTree writes tr into a fresh temp dir and returns the path.
func writeTree(t *testing.T, tr listing.Tree) string {
	t.Helper()
	dir := t.TempDir()
	if err := tree.Write(dir, tr); err != nil {
		t.Fatalf("tree.Write: %v", err)
	}
	return dir
}

func ml(code string, fv ...string) listing.Listing {
	l := listing.NewListing(code)
	keys := map[string]listing.Field{
		"title": listing.Title, "short": listing.ShortDescription,
		"full": listing.FullDescription, "video": listing.Video,
	}
	for i := 0; i+1 < len(fv); i += 2 {
		l.Set(keys[fv[i]], fv[i+1])
	}
	return l
}

// TestRun_dryRun_jsonDiffSchema asserts dry-run emits the gplay diff schema
// {package, changes[], summary} and never commits or patches.
func TestRun_dryRun_jsonDiffSchema(t *testing.T) {
	dir := writeTree(t, listing.Tree{"en-US": ml("en-US", "title", "New", "full", "Body")})
	rt := &applyRT{t: t, editID: "e1",
		listingsBody: `{"listings":[{"language":"en-US","fullDescription":"Body"}]}`} // title create, full unchanged
	rc := newRC(t, rt)

	r, err := apply.Run(rc, apply.Input{Package: "com.x", Dir: dir, DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var buf bytes.Buffer
	if err := r.Renderers().JSON(&buf); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	var got struct {
		Package string `json:"package"`
		Changes []struct {
			Locale, Field, Op string
		} `json:"changes"`
		Summary struct{ Create, Update int } `json:"summary"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("diff JSON did not parse: %v\n%s", err, buf.String())
	}
	if got.Package != "com.x" || got.Summary.Create != 1 {
		t.Errorf("diff = %+v, want package com.x, summary.create 1", got)
	}
	if rt.saw("POST", ":commit") || rt.saw("PATCH", "/listings/") {
		t.Errorf("dry-run mutated Play; calls=%v", rt.calls)
	}
}

// TestRun_applyWithoutConfirm_exit3 asserts a real apply refuses without
// --confirm, before opening any Edit, with exit 3 (safety flag required,
// docs/DESIGN.md §9 — NOT the generic usage exit 2, #408).
func TestRun_applyWithoutConfirm_exit3(t *testing.T) {
	dir := writeTree(t, listing.Tree{"en-US": ml("en-US", "title", "T", "full", "F")})
	rt := &applyRT{t: t, editID: "e1"}
	rc := newRC(t, rt)

	_, err := apply.Run(rc, apply.Input{Package: "com.x", Dir: dir})
	if code := exitCodeOf(t, err); code != 3 {
		t.Errorf("exit = %d, want 3", code)
	}
	var safety *exit.SafetyFlagError
	if !errors.As(err, &safety) || safety.Flag != "confirm" {
		t.Errorf("err = %v (%T), want *exit.SafetyFlagError naming \"confirm\"", err, err)
	}
	if !strings.Contains(err.Error(), "--dry-run") {
		t.Errorf("error %q should point at --dry-run", err.Error())
	}
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "POST /androidpublisher") {
			t.Errorf("opened an Edit despite missing --confirm; calls=%v", rt.calls)
		}
	}
}

// TestRun_applyConfirm_patchesAndCommits asserts a confirmed apply patches
// the changed locale, commits once, and --output json is the per-locale
// patch body.
func TestRun_applyConfirm_patchesAndCommits(t *testing.T) {
	dir := writeTree(t, listing.Tree{"fr-FR": ml("fr-FR", "title", "Bonjour", "full", "Desc")})
	rt := &applyRT{t: t, editID: "e7",
		listingsBody: `{"listings":[{"language":"fr-FR","title":"Salut","fullDescription":"Desc"}]}`} // title update
	rc := newRC(t, rt)

	r, err := apply.Run(rc, apply.Input{Package: "com.x", Dir: dir, Confirm: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rt.saw("PATCH", "/listings/fr-FR") || !rt.saw("POST", ":commit") {
		t.Errorf("expected PATCH fr-FR + commit; calls=%v", rt.calls)
	}
	// PATCH body carries only the changed title + language (missing≠empty).
	var body map[string]string
	_ = json.Unmarshal([]byte(rt.patchBody["fr-FR"]), &body)
	if body["title"] != "Bonjour" {
		t.Errorf("patch body = %v, want title=Bonjour", body)
	}
	var buf bytes.Buffer
	if err := r.Renderers().JSON(&buf); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("apply JSON did not parse: %v\n%s", err, buf.String())
	}
	if _, ok := out["fr-FR"]; !ok {
		t.Errorf("apply JSON missing fr-FR patch body: %s", buf.String())
	}
}

// TestRun_pruneConfirm_deletesOnlineOnly asserts --prune deletes an
// online-only locale and reports it.
func TestRun_pruneConfirm_deletesOnlineOnly(t *testing.T) {
	dir := writeTree(t, listing.Tree{"en-US": ml("en-US", "title", "T", "full", "F")})
	rt := &applyRT{t: t, editID: "ep", detailsLang: "en-US",
		listingsBody: `{"listings":[` +
			`{"language":"en-US","title":"T","fullDescription":"F"},` +
			`{"language":"it-IT","title":"Ciao","fullDescription":"Lunga"}]}`}
	rc := newRC(t, rt)

	r, err := apply.Run(rc, apply.Input{Package: "com.x", Dir: dir, Confirm: true, Prune: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rt.saw("DELETE", "/listings/it-IT") || !rt.saw("POST", ":commit") {
		t.Errorf("expected DELETE it-IT + commit; calls=%v", rt.calls)
	}
	var buf bytes.Buffer
	if err := r.Renderers().JSON(&buf); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if !strings.Contains(buf.String(), "it-IT") || !strings.Contains(buf.String(), "pruned") {
		t.Errorf("apply JSON should report the pruned it-IT: %s", buf.String())
	}
}

// TestRun_dirMissing_exit20 asserts an unreadable --dir is exit 20 before
// any network.
func TestRun_dirMissing_exit20(t *testing.T) {
	rt := &applyRT{t: t}
	rc := newRC(t, rt)
	_, err := apply.Run(rc, apply.Input{Package: "com.x", Dir: "/nonexistent/metadata/xyz", DryRun: true})
	if code := exitCodeOf(t, err); code != 20 {
		t.Errorf("exit = %d, want 20", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected 0 HTTP calls on dir error, saw %v", rt.calls)
	}
}

// TestRun_noPackage_exit2 and TestRun_noAccount_exit10 guard the pre-HTTP
// usage/auth gates.
func TestRun_noPackage_exit2(t *testing.T) {
	rt := &applyRT{t: t}
	rc := newRC(t, rt)
	_, err := apply.Run(rc, apply.Input{DryRun: true})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}

func TestRun_noAccount_exit10(t *testing.T) {
	dir := writeTree(t, listing.Tree{"en-US": ml("en-US", "title", "T", "full", "F")})
	rt := &applyRT{t: t}
	rc := newRC(t, rt)
	rc.Account = nil
	_, err := apply.Run(rc, apply.Input{Package: "com.x", Dir: dir, DryRun: true})
	if code := exitCodeOf(t, err); code != 10 {
		t.Errorf("exit = %d, want 10", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected 0 HTTP calls before auth, saw %v", rt.calls)
	}
}

// TestRun_applyConfirm_emitsConfirmationOnStderr asserts a committed apply
// prints a single ✓ line on stderr (DESIGN §8) naming the package, alongside
// the stdout payload.
func TestRun_applyConfirm_emitsConfirmationOnStderr(t *testing.T) {
	dir := writeTree(t, listing.Tree{"fr-FR": ml("fr-FR", "title", "Bonjour", "full", "Desc")})
	rt := &applyRT{t: t, editID: "e7",
		listingsBody: `{"listings":[{"language":"fr-FR","title":"Salut","fullDescription":"Desc"}]}`}
	rc := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := apply.Run(rc, apply.Input{Package: "com.x", Dir: dir, Confirm: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := stderr.String()
	if !strings.HasPrefix(got, "✓ ") || !strings.Contains(got, "com.x") {
		t.Errorf("apply ✓ line wrong:\n%s", got)
	}
}

// TestRun_dryRun_noConfirmationOnStderr asserts --dry-run never emits a ✓.
func TestRun_dryRun_noConfirmationOnStderr(t *testing.T) {
	dir := writeTree(t, listing.Tree{"en-US": ml("en-US", "title", "New", "full", "Body")})
	rt := &applyRT{t: t, editID: "e1",
		listingsBody: `{"listings":[]}`}
	rc := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := apply.Run(rc, apply.Input{Package: "com.x", Dir: dir, DryRun: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(stderr.String(), "✓") {
		t.Errorf("dry-run emitted a ✓ confirmation; stderr=%q", stderr.String())
	}
}
