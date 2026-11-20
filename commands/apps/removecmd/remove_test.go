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
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

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
	if !registry.Has(g.Accounts, "playci", "com.example.other") {
		t.Errorf("sibling package was wrongly removed; accounts=%+v", g.Accounts)
	}
}

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
	g, err := config.LoadGlobalOrEmpty(rc.Ctx, config.OSFS{}, rc.ConfigPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	if !registry.Has(g.Accounts, "playci", "com.example.kept") {
		t.Errorf("registry was perturbed by an empty-package call; accounts=%+v", g.Accounts)
	}
}

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

func TestRun_inlineCredential_refusesWithExit2(t *testing.T) {
	var stderr bytes.Buffer
	rc := kernel.NewForTest(context.Background(), kernel.Boot{Stderr: &stderr}, kernel.Inputs{})
	rc.Account = &serviceaccount.ServiceAccount{}
	rc.AccountName = ""
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

func TestRun_accountNotInGlobal_refusesWithExit10(t *testing.T) {
	var stderr bytes.Buffer
	rc := newRC(t, &stderr, nil)
	rc.AccountName = "ghost"

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

func TestRun_noCredentialAtAll_refusesWithExit10(t *testing.T) {
	var stderr bytes.Buffer
	rc := kernel.NewForTest(context.Background(), kernel.Boot{Stderr: &stderr}, kernel.Inputs{})
	rc.Resolved = &config.Resolved{}

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

func TestRun_pinnedPackage_warnsButDoesNotTouchProjectConfig(t *testing.T) {
	var stderr bytes.Buffer
	rc := newRC(t, &stderr, []string{"com.example.pinned"})

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

	se := stderr.String()
	if !strings.Contains(se, ".gplay/config.json") {
		t.Errorf("stderr should reference .gplay/config.json in the pin warning; got %q", se)
	}
	if !strings.Contains(se, "com.example.pinned") {
		t.Errorf("stderr should name the package; got %q", se)
	}

	after, err := os.ReadFile(sharedPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(after) != string(original) {
		t.Errorf("project config was rewritten by apps remove; before=%q after=%q", original, after)
	}
}

func runCmd(t *testing.T, cfgPath string, stdout, stderr *bytes.Buffer, args ...string) error {
	t.Helper()
	boot := kernel.Boot{Stdout: stdout, Stderr: stderr, ConfigPath: cfgPath}
	sub := removecmd.NewCommand(boot)
	root := &cobra.Command{Use: "gplay"}
	root.PersistentFlags().String("service-account", "", "")
	root.PersistentFlags().String("account", "", "")
	root.PersistentFlags().BoolP("verbose", "v", false, "")
	root.AddCommand(sub)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(append([]string{"remove"}, args...))
	return root.Execute()
}

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

func TestCobra_persistentFlags_inheritedFromRoot(t *testing.T) {
	cfgPath := seedGlobal(t, nil)
	for _, flag := range []string{"--verbose", "--account=foo", "--service-account=foo"} {
		t.Run(flag, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := runCmd(t, cfgPath, &stdout, &stderr, flag, "com.example.app")
			if err != nil && strings.Contains(err.Error(), "unknown flag") {
				t.Errorf("cobra rejected %s as unknown: runCmd drifted from production root: %v", flag, err)
			}
		})
	}
}

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

func TestCobra_extraArgs_isRejectedByExactArgs(t *testing.T) {
	cfgPath := seedGlobal(t, []string{"com.example.app"})
	var stdout, stderr bytes.Buffer
	err := runCmd(t, cfgPath, &stdout, &stderr, "com.example.app", "com.example.other")
	if err == nil {
		t.Fatal("Execute: expected error for too many positional args, got nil")
	}
}

func failOnCall(t *testing.T) http.RoundTripper {
	t.Helper()
	return testkit.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("apps remove must not make HTTP calls, but saw: %s %s", req.Method, req.URL)
		return nil, nil
	})
}

func TestRun_makesZeroHTTPCalls(t *testing.T) {
	var stderr bytes.Buffer
	rc := newRC(t, &stderr, []string{"com.example.myapp"})
	rc.Ctx = context.WithValue(rc.Ctx, oauth2.HTTPClient, &http.Client{Transport: failOnCall(t)})

	if _, err := removecmd.Run(rc, removecmd.Input{Package: "com.example.myapp"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

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
	g, err := config.LoadGlobalOrEmpty(rc.Ctx, config.OSFS{}, rc.ConfigPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	if !registry.Has(g.Accounts, "playci", "com.example.kept") {
		t.Errorf("existing package was perturbed by a no-op remove; accounts=%+v", g.Accounts)
	}
}

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
	if !strings.Contains(err.Error(), "invalid character") {
		t.Errorf("error should preserve the underlying JSON parse cause; got %q", err.Error())
	}
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
