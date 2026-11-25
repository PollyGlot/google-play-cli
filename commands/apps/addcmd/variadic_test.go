package addcmd_test

import (
	"bytes"
	"errors"
	iofs "io/fs"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/apps/addcmd"
	"github.com/PollyGlot/google-play-cli/internal/apps/registry"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
)

type saveFailFS struct{ config.OSFS }

func (saveFailFS) WriteFile(string, []byte, iofs.FileMode) error {
	return errors.New("simulated disk failure")
}

func TestRun_variadic_allSucceed_registersAll(t *testing.T) {
	rc := newRC(t, probeFake("edit-multi", nil))

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

func TestRun_variadic_partialFailure_registersSuccessesAndAggregates(t *testing.T) {
	rc := newRC(t, probeFake("edit-multi", map[string]int{"com.example.b": http.StatusForbidden}))

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

func TestRun_variadic_nonRetryableWins(t *testing.T) {
	rc := newRC(t, probeFake("edit-multi", map[string]int{
		"com.example.transient": http.StatusServiceUnavailable,
		"com.example.forbidden": http.StatusForbidden,
	}))

	pkgs := []string{"com.example.transient", "com.example.forbidden"}
	_, err := addcmd.Run(rc, addcmd.Input{Packages: pkgs})
	if err == nil {
		t.Fatal("Run: expected aggregate error, got nil")
	}
	if got := exit.For(err); got != 11 {
		t.Errorf("exit.For = %d, want 11 (non-retryable wins over retryable 40); err = %v", got, err)
	}
}

func TestRun_variadic_allRetryable_reportsRetryable(t *testing.T) {
	rc := newRC(t, probeFake("edit-multi", map[string]int{
		"com.example.a": http.StatusServiceUnavailable,
		"com.example.b": http.StatusBadGateway,
	}))

	_, err := addcmd.Run(rc, addcmd.Input{Packages: []string{"com.example.a", "com.example.b"}})
	if err == nil {
		t.Fatal("Run: expected aggregate error, got nil")
	}
	if got := exit.For(err); got != 40 {
		t.Errorf("exit.For = %d, want 40 (all failures retryable); err = %v", got, err)
	}
}

func TestRun_variadic_dedupProbesOnce(t *testing.T) {
	fake := probeFake("edit-multi", nil)
	rc := newRC(t, fake)

	_, err := addcmd.Run(rc, addcmd.Input{Packages: []string{"com.example.a", "com.example.a", "com.example.b"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := 0
	for _, c := range fake.Calls() {
		if c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits") && pkgFromPath(c.Path) == "com.example.a" {
			got++
		}
	}
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

func TestRun_variadic_saveFailure_reportsBeforePropagating(t *testing.T) {
	rc := newRC(t, probeFake("edit-multi", nil))
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
	out := stderr.String()
	if !strings.Contains(out, "com.example.a") || !strings.Contains(out, "com.example.b") {
		t.Errorf("stderr should carry the per-package report before the Save error; got %q", out)
	}
	if !strings.Contains(out, "0 registered, 2 failed") {
		t.Errorf("stderr should count every package as failed; got %q", out)
	}
	if strings.Contains(out, "registered \"") {
		t.Errorf("stderr reports a package as registered although the save failed; got %q", out)
	}
	for _, p := range []string{"com.example.a", "com.example.b"} {
		if !strings.Contains(out, "✗ "+p+": simulated disk failure") {
			t.Errorf("stderr should carry the save-failure line for %s; got %q", p, out)
		}
	}
}

func TestRun_variadic_saveFailure_keepsValidationLines(t *testing.T) {
	rc := newRC(t, failOnCall(t))
	var stderr bytes.Buffer
	rc.Stderr = &stderr
	rc.FS = saveFailFS{}

	_, err := addcmd.Run(rc, addcmd.Input{Packages: []string{"nodot", "com.example.a"}, NoVerify: true})
	if err == nil || !strings.Contains(err.Error(), "simulated disk failure") {
		t.Fatalf("Run: want the Save error, got %v", err)
	}
	out := stderr.String()
	if !strings.Contains(out, "✗ nodot: apps add: \"nodot\" is not a valid Android package name") {
		t.Errorf("the validation failure lost its own line; got %q", out)
	}
	if !strings.Contains(out, "✗ com.example.a: simulated disk failure") {
		t.Errorf("the accepted package should carry the save-failure line; got %q", out)
	}
	if strings.Contains(out, "registered (unverified) \"com.example.a\"") {
		t.Errorf("com.example.a reported as registered although the save failed; got %q", out)
	}
	if !strings.Contains(out, "0 registered, 2 failed") {
		t.Errorf("stderr should carry the batch tally; got %q", out)
	}
}

func TestRun_variadic_noVerify_registersAllOffline(t *testing.T) {
	rc := newRC(t, failOnCall(t))

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
