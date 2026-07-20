// Package addcmd_test — variadic-path coverage for `gplay apps add`
// (ADR-0040). These exercise the multi-package contract: partial success,
// the non-retryable-wins exit code, argument dedup, and offline
// --no-verify batches. The single-package non-regression cases live in
// add_test.go; newRC / signedSAJSON / jsonResp are shared from there.
package addcmd_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/apps/addcmd"
	"github.com/PollyGlot/google-play-cli/internal/apps/registry"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
)

// saveFailFS is an OSFS whose WriteFile always fails, so config.Global.Save
// returns an error — used to exercise the batch Save-failure report path.
type saveFailFS struct{ config.OSFS }

func (saveFailFS) WriteFile(string, []byte, iofs.FileMode) error {
	return errors.New("simulated disk failure")
}

// variadicRT routes the edits.insert+delete probe per package: an insert
// for a package listed in insertStatusByPkg returns that status (a
// simulated API failure), every other insert returns 200 with a shared
// editID, and every delete returns 204. insertsByPkg counts inserts per
// package so a test can assert dedup probed a repeated argument only once.
type variadicRT struct {
	t                 *testing.T
	editID            string
	insertStatusByPkg map[string]int

	mu           sync.Mutex
	insertsByPkg map[string]int
}

// pkgFromPath extracts the package from a `/applications/{pkg}/edits...`
// URL path — the shape internal/play/edits builds.
func pkgFromPath(path string) string {
	const marker = "/applications/"
	i := strings.Index(path, marker)
	if i < 0 {
		return ""
	}
	rest := path[i+len(marker):]
	if j := strings.Index(rest, "/"); j >= 0 {
		return rest[:j]
	}
	return rest
}

func (r *variadicRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		return jsonResp(200, `{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`), nil
	}

	pkg := pkgFromPath(req.URL.Path)
	switch {
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits"):
		if r.insertsByPkg == nil {
			r.insertsByPkg = map[string]int{}
		}
		r.insertsByPkg[pkg]++
		if status, bad := r.insertStatusByPkg[pkg]; bad {
			return jsonResp(status, fmt.Sprintf(`{"error":{"code":%d,"message":"stub"}}`, status)), nil
		}
		return jsonResp(200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, r.editID)), nil
	case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/"):
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	r.t.Fatalf("variadicRT: unexpected request: %s %s", req.Method, req.URL)
	return nil, nil
}

// TestRun_variadic_allSucceed_registersAll drives three packages that all
// probe clean and asserts each lands in the registry and the command
// returns a clean nil error (exit 0).
func TestRun_variadic_allSucceed_registersAll(t *testing.T) {
	rt := &variadicRT{t: t, editID: "edit-multi"}
	rc := newRC(t, rt)

	pkgs := []string{"com.example.a", "com.example.b", "com.example.c"}
	if _, err := addcmd.Run(rc, addcmd.Input{Packages: pkgs}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	g, err := config.LoadGlobalOrEmpty(rc.Ctx, config.OSFS{}, rc.ConfigPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	for _, p := range pkgs {
		if !registry.Has(g.Accounts, "playci", p) {
			t.Errorf("package %q not registered; accounts=%+v", p, g.Accounts)
		}
	}
}

// TestRun_variadic_partialFailure_registersSuccessesAndAggregates is the
// core partial-success contract: a middle package is refused (403 → exit
// 11) while its neighbors register. The successes must persist, the failed
// package must NOT, and the returned error must carry exit 11.
func TestRun_variadic_partialFailure_registersSuccessesAndAggregates(t *testing.T) {
	rt := &variadicRT{
		t:                 t,
		editID:            "edit-multi",
		insertStatusByPkg: map[string]int{"com.example.b": http.StatusForbidden},
	}
	rc := newRC(t, rt)

	pkgs := []string{"com.example.a", "com.example.b", "com.example.c"}
	_, err := addcmd.Run(rc, addcmd.Input{Packages: pkgs})
	if err == nil {
		t.Fatal("Run: expected aggregate error for the refused package, got nil")
	}
	if got := exit.For(err); got != 11 {
		t.Errorf("exit.For = %d, want 11 (single non-retryable failure); err = %v", got, err)
	}
	if !strings.Contains(err.Error(), "com.example.b") {
		t.Errorf("aggregate error should name the failed package; got %q", err.Error())
	}

	g, err2 := config.LoadGlobalOrEmpty(rc.Ctx, config.OSFS{}, rc.ConfigPath)
	if err2 != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err2)
	}
	if !registry.Has(g.Accounts, "playci", "com.example.a") {
		t.Error("com.example.a (succeeded) should be registered")
	}
	if !registry.Has(g.Accounts, "playci", "com.example.c") {
		t.Error("com.example.c (succeeded) should be registered")
	}
	if registry.Has(g.Accounts, "playci", "com.example.b") {
		t.Error("com.example.b (refused) must NOT be registered")
	}
}

// TestRun_variadic_nonRetryableWins asserts the exit-code rule: when a
// batch mixes a retryable failure (503 → exit 40) and a non-retryable one
// (403 → exit 11), the non-retryable code wins so an agent does not blindly
// retry a batch that carries a permanent failure.
func TestRun_variadic_nonRetryableWins(t *testing.T) {
	rt := &variadicRT{
		t:      t,
		editID: "edit-multi",
		insertStatusByPkg: map[string]int{
			"com.example.transient": http.StatusServiceUnavailable, // 40, retryable
			"com.example.forbidden": http.StatusForbidden,          // 11, non-retryable
		},
	}
	rc := newRC(t, rt)

	pkgs := []string{"com.example.transient", "com.example.forbidden"}
	_, err := addcmd.Run(rc, addcmd.Input{Packages: pkgs})
	if err == nil {
		t.Fatal("Run: expected aggregate error, got nil")
	}
	if got := exit.For(err); got != 11 {
		t.Errorf("exit.For = %d, want 11 (non-retryable wins over retryable 40); err = %v", got, err)
	}
}

// TestRun_variadic_allRetryable_reportsRetryable asserts the other side of
// the rule: when every failure is retryable (all 503 → 40), the batch
// exits 40 so an automated caller may retry once the transient condition
// clears.
func TestRun_variadic_allRetryable_reportsRetryable(t *testing.T) {
	rt := &variadicRT{
		t:      t,
		editID: "edit-multi",
		insertStatusByPkg: map[string]int{
			"com.example.a": http.StatusServiceUnavailable,
			"com.example.b": http.StatusBadGateway,
		},
	}
	rc := newRC(t, rt)

	_, err := addcmd.Run(rc, addcmd.Input{Packages: []string{"com.example.a", "com.example.b"}})
	if err == nil {
		t.Fatal("Run: expected aggregate error, got nil")
	}
	if got := exit.For(err); got != 40 {
		t.Errorf("exit.For = %d, want 40 (all failures retryable); err = %v", got, err)
	}
}

// TestRun_variadic_dedupProbesOnce asserts a repeated argument is a single
// unit of work: `add a a b` probes `a` exactly once and registers it once.
func TestRun_variadic_dedupProbesOnce(t *testing.T) {
	rt := &variadicRT{t: t, editID: "edit-multi"}
	rc := newRC(t, rt)

	_, err := addcmd.Run(rc, addcmd.Input{Packages: []string{"com.example.a", "com.example.a", "com.example.b"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rt.mu.Lock()
	got := rt.insertsByPkg["com.example.a"]
	rt.mu.Unlock()
	if got != 1 {
		t.Errorf("com.example.a probed %d times, want 1 (dedup)", got)
	}

	g, err := config.LoadGlobalOrEmpty(rc.Ctx, config.OSFS{}, rc.ConfigPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	if !registry.Has(g.Accounts, "playci", "com.example.a") || !registry.Has(g.Accounts, "playci", "com.example.b") {
		t.Errorf("both distinct packages should be registered; accounts=%+v", g.Accounts)
	}
}

// TestRun_variadic_saveFailure_reportsBeforePropagating asserts that when
// the terminal Save fails on a multi-package batch, the per-package ✓/✗
// report is still emitted (so the operator sees which packages probed
// clean) before the raw Save error propagates.
func TestRun_variadic_saveFailure_reportsBeforePropagating(t *testing.T) {
	rt := &variadicRT{t: t, editID: "edit-multi"}
	rc := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr
	rc.FS = saveFailFS{}

	_, err := addcmd.Run(rc, addcmd.Input{Packages: []string{"com.example.a", "com.example.b"}})
	if err == nil {
		t.Fatal("Run: expected the Save error to propagate, got nil")
	}
	if !strings.Contains(err.Error(), "simulated disk failure") {
		t.Errorf("returned error should be the raw Save error; got %q", err.Error())
	}
	// The per-package report must have been emitted despite the Save failure.
	out := stderr.String()
	if !strings.Contains(out, "com.example.a") || !strings.Contains(out, "com.example.b") {
		t.Errorf("stderr should carry the per-package report before the Save error; got %q", out)
	}
	if !strings.Contains(out, "2 registered, 0 failed") {
		t.Errorf("stderr should carry the batch tally; got %q", out)
	}
}

// TestRun_variadic_noVerify_registersAllOffline asserts a --no-verify
// batch touches no network (failOnCallRT is the assertion) yet persists
// every package.
func TestRun_variadic_noVerify_registersAllOffline(t *testing.T) {
	rt := &failOnCallRT{t: t}
	rc := newRC(t, rt)

	pkgs := []string{"com.example.a", "com.example.b", "com.example.c"}
	if _, err := addcmd.Run(rc, addcmd.Input{Packages: pkgs, NoVerify: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	g, err := config.LoadGlobalOrEmpty(rc.Ctx, config.OSFS{}, rc.ConfigPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	for _, p := range pkgs {
		if !registry.Has(g.Accounts, "playci", p) {
			t.Errorf("package %q not registered under --no-verify batch; accounts=%+v", p, g.Accounts)
		}
	}
}
