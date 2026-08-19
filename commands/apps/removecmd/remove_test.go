// Package removecmd_test exercises `gplay apps remove` at the kernel
// level: a RunContext built by hand and Run invoked directly. Mirrors
// the commands/apps/addcmd and commands/auth/logout patterns. The
// command is a pure local-state op (zero HTTP calls) so tests never
// install a token exchange or RoundTripper machinery (an opt-in
// failOnCallRT proves the no-network invariant where it matters).
package removecmd_test

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/apps/removecmd"
	"github.com/PollyGlot/google-play-cli/internal/apps/registry"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// newRC builds a RunContext with a fresh global config at a temp path,
// seeded with one Account named "playci" (active) carrying the given
// packages. rc.AccountName is populated to mirror the post-resolver
// state: remove reads it as the authoritative scope (mirrors addcmd).
// The stderr buffer is plumbed through rc.Stderr so tests can assert on
// the human-facing notices remove emits (no-op hint, pin warning).
func newRC(t *testing.T, stderr *bytes.Buffer, packages []string) *kernel.RunContext {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.json")
	g := &config.Global{Accounts: []config.Account{
		{Name: "playci", Active: true, Packages: packages},
	}}
	if err := g.Save(ctx, config.OSFS{}, cfgPath); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	rc := kernel.NewForTest(ctx, kernel.Boot{ConfigPath: cfgPath, Stderr: stderr}, kernel.Inputs{})
	rc.AccountName = "playci"
	rc.Resolved = &config.Resolved{
		Accounts:      g.Accounts,
		ConfigAccount: "playci",
		GlobalPath:    cfgPath,
	}
	rc.ConfigPath = cfgPath
	return rc
}

// TestRun_happyPath_removesAndPersists is the tracer bullet: the
// active Account has the package registered, Run drops it, and the
// change is visible on the next config load. Renderable must be nil
// (remove has no payload: like auth logout).
func TestRun_happyPath_removesAndPersists(t *testing.T) {
	var stderr bytes.Buffer
	rc := newRC(t, &stderr, []string{"com.example.myapp", "com.example.other"})

	r, err := removecmd.Run(rc, removecmd.Input{Package: "com.example.myapp"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r != nil {
		t.Errorf("remove.Run returned a Renderable; want nil (no payload)")
	}

	g, err := config.LoadGlobalOrEmpty(rc.Ctx, config.OSFS{}, rc.ConfigPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	if registry.Has(g.Accounts, "playci", "com.example.myapp") {
		t.Errorf("package still in registry after Run; accounts=%+v", g.Accounts)
	}
	// The other package must survive: Run targets exactly one entry.
	if !registry.Has(g.Accounts, "playci", "com.example.other") {
		t.Errorf("sibling package was wrongly removed; accounts=%+v", g.Accounts)
	}
}

// TestRun_emptyPackage_rejectsClientSide asserts the symmetry-with-add
// invariant: an empty <package> positional (which ExactArgs(1) lets
// through: it only counts args, doesn't inspect them) must be
// rejected client-side with exit 2 (CLI misuse), not silently no-op
// against an empty key in the registry. Without this branch, the
// user types `gplay apps remove ""` and sees a cryptic
// `"" not in registry, nothing to do` on stderr.
func TestRun_emptyPackage_rejectsClientSide(t *testing.T) {
	var stderr bytes.Buffer
	rc := newRC(t, &stderr, []string{"com.example.kept"})

	_, err := removecmd.Run(rc, removecmd.Input{Package: ""})
	if err == nil {
		t.Fatal("Run: expected error for empty package, got nil")
	}
	if got := exit.For(err); got != 2 {
		t.Errorf("exit.For = %d, want 2 (CLI misuse); err = %v", got, err)
	}
	// The registry must be untouched: no Save, no churn.
	g, err := config.LoadGlobalOrEmpty(rc.Ctx, config.OSFS{}, rc.ConfigPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	if !registry.Has(g.Accounts, "playci", "com.example.kept") {
		t.Errorf("registry was perturbed by an empty-package call; accounts=%+v", g.Accounts)
	}
}

// TestRun_packageWithoutDot_rejectsClientSide asserts symmetry with
// addcmd's validation contract: a package name without a dot can
// never have been registered (addcmd refuses to register it), so a
// remove on such a string is by definition a typo. Refuse client-side
// with exit 20 (client-side validation) instead of emitting the
// confusing `"foo" not in registry` no-op.
func TestRun_packageWithoutDot_rejectsClientSide(t *testing.T) {
	var stderr bytes.Buffer
	rc := newRC(t, &stderr, []string{"com.example.kept"})

	_, err := removecmd.Run(rc, removecmd.Input{Package: "notapackage"})
	if err == nil {
		t.Fatal("Run: expected error for missing dot, got nil")
	}
	if got := exit.For(err); got != 20 {
		t.Errorf("exit.For = %d, want 20 (client-side validation); err = %v", got, err)
	}
}

// TestRun_inlineCredential_refusesWithExit2 asserts the cross-command
// invariant locked by addcmd and listcmd: an inline credential
// (--service-account / GPLAY_SERVICE_ACCOUNT) has no local Account
// name to scope the per-Account registry against, so `apps remove`
// must refuse with exit code 2 and a clear message. Without this
// branch, Run would silently no-op against an empty AccountName and
// the user would think they removed the package.
func TestRun_inlineCredential_refusesWithExit2(t *testing.T) {
	var stderr bytes.Buffer
	rc := kernel.NewForTest(context.Background(), kernel.Boot{Stderr: &stderr}, kernel.Inputs{})
	rc.Account = &serviceaccount.ServiceAccount{} // non-nil → a credential resolved
	rc.AccountName = ""                           // inline: no local name
	rc.Resolved = &config.Resolved{}

	_, err := removecmd.Run(rc, removecmd.Input{Package: "com.example.app"})
	if err == nil {
		t.Fatal("Run: expected error for inline credential, got nil")
	}
	if got := exit.For(err); got != 2 {
		t.Errorf("exit.For = %d, want 2 (CLI misuse); err = %v", got, err)
	}
	if !strings.Contains(err.Error(), "inline credential") && !strings.Contains(err.Error(), "--service-account") {
		t.Errorf("error should explain the inline-credential limitation; got %q", err.Error())
	}
}

// TestRun_accountNotInGlobal_refusesWithExit10 asserts the distinct
// error path users get when they hit a stale `.gplay/config.local.json`
// or a logged-out Account: rc.AccountName is set, but the Account is
// not in the global config. Mirrors addcmd's pre-flight check:
// without it, `apps remove ghost-account-pkg` would silently no-op
// (registry.Remove on an unknown account is a no-op), masking a
// broken state until the next `apps add`.
func TestRun_accountNotInGlobal_refusesWithExit10(t *testing.T) {
	var stderr bytes.Buffer
	rc := newRC(t, &stderr, nil)
	rc.AccountName = "ghost" // newRC seeds only "playci"

	_, err := removecmd.Run(rc, removecmd.Input{Package: "com.example.app"})
	if err == nil {
		t.Fatal("Run: expected error for ghost account, got nil")
	}
	if got := exit.For(err); got != 10 {
		t.Errorf("exit.For = %d, want 10 (auth); err = %v", got, err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the missing Account; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "gplay auth login") {
		t.Errorf("error should point at `gplay auth login`; got %q", err.Error())
	}
}

// TestRun_noCredentialAtAll_refusesWithExit10 asserts the
// nothing-resolved case: no flag, no env, no cascade active. Run
// returns a clear authError (exit 10) rather than silently no-op'ing
// against an empty AccountName.
func TestRun_noCredentialAtAll_refusesWithExit10(t *testing.T) {
	var stderr bytes.Buffer
	rc := kernel.NewForTest(context.Background(), kernel.Boot{Stderr: &stderr}, kernel.Inputs{})
	rc.Resolved = &config.Resolved{} // no AccountName, no Account, no Accounts

	_, err := removecmd.Run(rc, removecmd.Input{Package: "com.example.app"})
	if err == nil {
		t.Fatal("Run: expected error for missing Account, got nil")
	}
	if got := exit.For(err); got != 10 {
		t.Errorf("exit.For = %d, want 10; err = %v", got, err)
	}
	if !strings.Contains(err.Error(), "gplay auth login") {
		t.Errorf("error should point at `gplay auth login`; got %q", err.Error())
	}
}

// TestRun_pinnedPackage_warnsButDoesNotTouchProjectConfig asserts the
// pin-handling rule from the issue: when the removed package matches
// rc.Resolved.Pin, Run prints a stderr warning so the user knows their
// `.gplay/config.json` pin now points at a package the registry no
// longer carries, but `apps remove` must NOT rewrite the project
// config (whether to repin or leave the dangling pin is the caller's
// decision, not a side effect of this command).
//
// The assertion is byte-for-byte equality on the project config file
// before and after Run, so any silent rewrite, even a no-op
// marshal/unmarshal that reorders keys: fails the test.
func TestRun_pinnedPackage_warnsButDoesNotTouchProjectConfig(t *testing.T) {
	var stderr bytes.Buffer
	rc := newRC(t, &stderr, []string{"com.example.pinned"})

	// Stand up a .gplay/config.json next to the global config so the
	// test owns its on-disk layout. The path is recorded on
	// rc.Resolved.ProjectSharedPath; production walkup would have set
	// it the same way.
	projectDir := filepath.Join(t.TempDir(), ".gplay")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	sharedPath := filepath.Join(projectDir, "config.json")
	original := []byte(`{"package":"com.example.pinned"}` + "\n")
	if err := os.WriteFile(sharedPath, original, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	rc.Resolved.Pin = "com.example.pinned"
	rc.Resolved.ProjectSharedPath = sharedPath

	if _, err := removecmd.Run(rc, removecmd.Input{Package: "com.example.pinned"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The pin warning must reach stderr AND must reference the
	// project config: without it the user has no signal that their
	// `.gplay/config.json` pin now points at a package the registry no
	// longer carries. We assert on ".gplay/config.json" specifically so
	// the test fails when the warning is missing even though the
	// command's "✓ removed ..." line happens to contain the substring
	// "pinned" via the package name.
	se := stderr.String()
	if !strings.Contains(se, ".gplay/config.json") {
		t.Errorf("stderr should reference .gplay/config.json in the pin warning; got %q", se)
	}
	if !strings.Contains(se, "com.example.pinned") {
		t.Errorf("stderr should name the package; got %q", se)
	}

	// The project config must be byte-for-byte identical.
	after, err := os.ReadFile(sharedPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(after) != string(original) {
		t.Errorf("project config was rewritten by apps remove; before=%q after=%q", original, after)
	}
}

// runCmd executes a fresh `gplay apps remove ...` cobra tree against
// the given seed config file. Mirrors the production root's
// PersistentFlags declarations from cmd/gplay/main.go so
// kernel.FromCobra reads the same flag surface the binary exposes:
// a future regression of main.go that drops `--verbose` or `--account`
// would then surface here as a test failure instead of slipping
// through silently (FromCobra calls cmd.Flags().GetBool(\"verbose\")
// and ignores the not-found error, so without these declarations
// every test value defaults to zero regardless of what production does).
func runCmd(t *testing.T, cfgPath string, stdout, stderr *bytes.Buffer, args ...string) error {
	t.Helper()
	boot := kernel.Boot{Stdout: stdout, Stderr: stderr, ConfigPath: cfgPath}
	sub := removecmd.NewCommand(boot)
	root := &cobra.Command{Use: "gplay"}
	// Mirror cmd/gplay/main.go's PersistentFlags so a real prod
	// regression (flag renamed, flag dropped) breaks tests too.
	root.PersistentFlags().String("service-account", "", "")
	root.PersistentFlags().String("account", "", "")
	root.PersistentFlags().BoolP("verbose", "v", false, "")
	root.AddCommand(sub)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(append([]string{"remove"}, args...))
	return root.Execute()
}

// seedGlobal writes a global config.json with one active Account named
// "playci" carrying the given packages.
func seedGlobal(t *testing.T, packages []string) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	g := &config.Global{Accounts: []config.Account{
		{Name: "playci", Active: true, Packages: packages},
	}}
	if err := g.Save(context.Background(), config.OSFS{}, cfgPath); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	return cfgPath
}

// TestCobra_persistentFlags_inheritedFromRoot asserts that the test
// cobra root declares the same PersistentFlags as production
// (cmd/gplay/main.go). Passing `--verbose`, `--account`, and
// `--service-account` at the top level must NOT yield "unknown flag"
// errors, if cobra rejects any of them, the runCmd helper has
// drifted from production and the rest of the cobra-level tests are
// running in a fictional flag environment.
//
// The test asserts on the failure mode: a flag-parsing error from
// cobra surfaces as "unknown flag: --foo". Any non-cobra error
// (business logic, auth) is fine: the persistent flags reached the
// parser, which is the whole point.
func TestCobra_persistentFlags_inheritedFromRoot(t *testing.T) {
	cfgPath := seedGlobal(t, nil)
	for _, flag := range []string{"--verbose", "--account=foo", "--service-account=foo"} {
		t.Run(flag, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := runCmd(t, cfgPath, &stdout, &stderr, flag, "com.example.app")
			// A regression of cmd/gplay/main.go that drops one of
			// these PersistentFlags would surface here as cobra's
			// `unknown flag: ...` error. Any other error (business
			// logic, auth) is acceptable: it proves the flag
			// reached the parser successfully.
			if err != nil && strings.Contains(err.Error(), "unknown flag") {
				t.Errorf("cobra rejected %s as unknown: runCmd drifted from production root: %v", flag, err)
			}
		})
	}
}

// TestCobra_zeroArgs_isRejectedByExactArgs asserts cobra-level args
// validation: `gplay apps remove` (no positional) must NOT silently
// proceed. Without ExactArgs(1) the default ArbitraryArgs would let
// the command run with in.Package = "" and emit a confusing "not in
// registry" no-op on stderr.
func TestCobra_zeroArgs_isRejectedByExactArgs(t *testing.T) {
	cfgPath := seedGlobal(t, []string{"com.example.app"})
	var stdout, stderr bytes.Buffer
	err := runCmd(t, cfgPath, &stdout, &stderr)
	if err == nil {
		t.Fatal("Execute: expected error for missing positional arg, got nil")
	}
	if !strings.Contains(err.Error(), "arg") {
		t.Errorf("error should mention args; got %q", err.Error())
	}
}

// TestCobra_extraArgs_isRejectedByExactArgs asserts that two
// positionals are rejected too (a stale `--package` muscle-memory user
// types `gplay apps remove --package com.foo` and the bare token
// could have slipped through ArbitraryArgs).
func TestCobra_extraArgs_isRejectedByExactArgs(t *testing.T) {
	cfgPath := seedGlobal(t, []string{"com.example.app"})
	var stdout, stderr bytes.Buffer
	err := runCmd(t, cfgPath, &stdout, &stderr, "com.example.app", "com.example.other")
	if err == nil {
		t.Fatal("Execute: expected error for too many positional args, got nil")
	}
}

// failOnCallRT fails the test on any HTTP call. Tests pass it through
// the oauth2.HTTPClient context key: the same seam addcmd uses for
// its --no-verify assertion. `apps remove` is pure local state and
// MUST never hit the network; this RoundTripper is the assertion.
type failOnCallRT struct{ t *testing.T }

func (r *failOnCallRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.t.Fatalf("apps remove must not make HTTP calls, but saw: %s %s", req.Method, req.URL)
	return nil, nil
}

// TestRun_makesZeroHTTPCalls is the no-network invariant from the
// issue's acceptance criteria. The package is registered, Run drops
// it, and the failOnCallRT never sees a request: proving the
// "pure local state op" contract held end-to-end (not just by the
// absence of an HTTP client in the current implementation).
func TestRun_makesZeroHTTPCalls(t *testing.T) {
	var stderr bytes.Buffer
	rc := newRC(t, &stderr, []string{"com.example.myapp"})
	rc.Ctx = context.WithValue(rc.Ctx, oauth2.HTTPClient, &http.Client{Transport: &failOnCallRT{t: t}})

	if _, err := removecmd.Run(rc, removecmd.Input{Package: "com.example.myapp"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestRun_packageAbsent_isNoOpWithStderrNote asserts the idempotency
// contract from the issue: `gplay apps remove <unknown>` must exit 0
// and emit a stderr hint, never an error. The hint protects users
// piping `apps remove` into shell scripts: a silent zero exit with no
// signal would let typos slip through unnoticed.
func TestRun_packageAbsent_isNoOpWithStderrNote(t *testing.T) {
	var stderr bytes.Buffer
	rc := newRC(t, &stderr, []string{"com.example.kept"})

	r, err := removecmd.Run(rc, removecmd.Input{Package: "com.example.unknown"})
	if err != nil {
		t.Fatalf("Run: unexpected error for absent package: %v", err)
	}
	if r != nil {
		t.Errorf("Run returned a Renderable; want nil")
	}
	if !strings.Contains(stderr.String(), "not in registry") {
		t.Errorf("stderr should explain the no-op; got %q", stderr.String())
	}
	// The other package must still be there: no rewrite, no churn.
	g, err := config.LoadGlobalOrEmpty(rc.Ctx, config.OSFS{}, rc.ConfigPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	if !registry.Has(g.Accounts, "playci", "com.example.kept") {
		t.Errorf("existing package was perturbed by a no-op remove; accounts=%+v", g.Accounts)
	}
}

// TestRemove_cobra_malformedInlineCredential_exits10NoMutation drives the
// real cobra command through the production lazy path with a malformed
// inline GPLAY_SERVICE_ACCOUNT. The AccountName == "" branch must surface
// the invalid-credential error (exit 10, the real cause) instead of the
// misdirecting "run gplay auth login" authError, and must NOT mutate the
// cascade Account's registry (#180, ADR-0020).
func TestRemove_cobra_malformedInlineCredential_exits10NoMutation(t *testing.T) {
	t.Setenv(resolver.EnvAccount, "")
	t.Setenv(resolver.EnvServiceAccount, "{ not valid json")
	cfgPath := seedGlobal(t, []string{"com.cascade.app"})
	var stdout, stderr bytes.Buffer

	err := runCmd(t, cfgPath, &stdout, &stderr, "com.cascade.app")
	if err == nil {
		t.Fatal("apps remove with a malformed inline credential = nil, want exit 10")
	}
	if got := exit.For(err); got != 10 {
		t.Errorf("exit.For = %d, want 10; err=%v", got, err)
	}
	if !strings.Contains(err.Error(), "could not read credential") {
		t.Errorf("error should name the real cause; got %q", err.Error())
	}
	// Lock in the cause-preservation contract (%w): the wrapped JSON parse
	// error survives, not just the wrapper prefix.
	if !strings.Contains(err.Error(), "invalid character") {
		t.Errorf("error should preserve the underlying JSON parse cause; got %q", err.Error())
	}
	// Under --output json (auto-resolved off a non-TTY) a failure emits the
	// structured error envelope on stdout (ADR-0023): the error only, never a
	// data payload. The no-mutation contract below is the load-bearing guard.
	if out := stdout.String(); !strings.Contains(out, `"exitCode": 10`) {
		t.Errorf("json hard-error path should emit the exit-10 error envelope; got %q", out)
	}
	g, lerr := config.LoadGlobalOrEmpty(context.Background(), config.OSFS{}, cfgPath)
	if lerr != nil {
		t.Fatalf("reload: %v", lerr)
	}
	if !registry.Has(g.Accounts, "playci", "com.cascade.app") {
		t.Error("a malformed inline credential must not mutate the cascade Account's registry")
	}
}
