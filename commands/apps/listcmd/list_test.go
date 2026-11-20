package listcmd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/apps/listcmd"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

func newRC(t *testing.T, stdout, stderr *bytes.Buffer, packages []string) *kernel.RunContext {
	t.Helper()
	rc := kernel.NewForTest(context.Background(), kernel.Boot{
		Stdout: stdout,
		Stderr: stderr,
	}, kernel.Inputs{Format: output.FormatJSON})
	rc.AccountName = "playci"
	rc.Resolved = &config.Resolved{
		Accounts:      []config.Account{{Name: "playci", Active: true, Packages: packages}},
		ConfigAccount: "playci",
	}
	return rc
}

func TestRun_emptyActiveAccount_returnsEmptyPayload(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rc := newRC(t, &stdout, &stderr, nil)

	r, err := listcmd.Run(rc, listcmd.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	p, ok := r.(listcmd.Payload)
	if !ok {
		t.Fatalf("Run returned %T, want listcmd.Payload", r)
	}
	if len(p.Apps) != 0 {
		t.Errorf("empty registry should produce zero rows; got %+v", p.Apps)
	}
}

func TestRun_withPackages_returnsRowsUnpinned(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rc := newRC(t, &stdout, &stderr, []string{"com.example.alpha", "com.example.beta"})

	r, err := listcmd.Run(rc, listcmd.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	p, ok := r.(listcmd.Payload)
	if !ok {
		t.Fatalf("Run returned %T, want listcmd.Payload", r)
	}
	if len(p.Apps) != 2 {
		t.Fatalf("len(Apps) = %d, want 2", len(p.Apps))
	}
	if p.Apps[0].Package != "com.example.alpha" || p.Apps[0].Pinned {
		t.Errorf("Apps[0] = %+v, want {com.example.alpha, false}", p.Apps[0])
	}
	if p.Apps[1].Package != "com.example.beta" || p.Apps[1].Pinned {
		t.Errorf("Apps[1] = %+v, want {com.example.beta, false}", p.Apps[1])
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr should be empty when there are packages; got %q", stderr.String())
	}
}

func TestRun_marksPinnedRow(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rc := newRC(t, &stdout, &stderr, []string{"com.example.alpha", "com.example.beta"})
	rc.Resolved.Pin = "com.example.beta"

	r, err := listcmd.Run(rc, listcmd.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	p := r.(listcmd.Payload)
	if p.Apps[0].Pinned {
		t.Errorf("Apps[0] (alpha) should not be pinned; got Pinned=true")
	}
	if !p.Apps[1].Pinned {
		t.Errorf("Apps[1] (beta) should be pinned; got Pinned=false")
	}
}

func TestRun_accountFlagOverride_listsAccountThatActuallyResolved(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rc := kernel.NewForTest(context.Background(), kernel.Boot{
		Stdout: &stdout,
		Stderr: &stderr,
	}, kernel.Inputs{Format: output.FormatJSON})
	rc.AccountName = "altname"
	rc.Resolved = &config.Resolved{
		Accounts: []config.Account{
			{Name: "playci", Active: true, Packages: []string{"com.playci.app"}},
			{Name: "altname", Packages: []string{"com.altname.app"}},
		},
		ConfigAccount: "playci",
	}

	r, err := listcmd.Run(rc, listcmd.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	p := r.(listcmd.Payload)
	if len(p.Apps) != 1 || p.Apps[0].Package != "com.altname.app" {
		t.Errorf("Apps = %+v; want exactly [com.altname.app] (the --account override target)", p.Apps)
	}
}

func TestRun_inlineCredential_returnsClearError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rc := kernel.NewForTest(context.Background(), kernel.Boot{
		Stdout: &stdout,
		Stderr: &stderr,
	}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = &serviceaccount.ServiceAccount{}
	rc.AccountName = ""
	rc.Resolved = &config.Resolved{}

	_, err := listcmd.Run(rc, listcmd.Input{})
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

func TestRun_accountNotInGlobal_returnsDistinctError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rc := kernel.NewForTest(context.Background(), kernel.Boot{
		Stdout: &stdout,
		Stderr: &stderr,
	}, kernel.Inputs{Format: output.FormatJSON})
	rc.AccountName = "ghost"
	rc.Resolved = &config.Resolved{
		Accounts:      []config.Account{{Name: "playci", Active: true}},
		ConfigAccount: "playci",
	}

	_, err := listcmd.Run(rc, listcmd.Input{})
	if err == nil {
		t.Fatal("Run: expected error for missing-Account, got nil")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the missing Account; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "gplay auth login") {
		t.Errorf("error should point at `gplay auth login`; got %q", err.Error())
	}
}

func TestRun_accountScope_ignoresPackagesUnderOtherAccounts(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rc := kernel.NewForTest(context.Background(), kernel.Boot{
		Stdout: &stdout,
		Stderr: &stderr,
	}, kernel.Inputs{Format: output.FormatJSON})
	rc.AccountName = "playci"
	rc.Resolved = &config.Resolved{
		Accounts: []config.Account{
			{Name: "playci", Active: true, Packages: []string{"com.playci.app"}},
			{Name: "altname", Packages: []string{"com.altname.app"}},
		},
		ConfigAccount: "playci",
	}

	r, err := listcmd.Run(rc, listcmd.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	p := r.(listcmd.Payload)
	if len(p.Apps) != 1 || p.Apps[0].Package != "com.playci.app" {
		t.Errorf("Apps = %+v; want exactly [com.playci.app]", p.Apps)
	}
}

func TestRun_noCredentialAtAll_returnsAuthErrorWithHint(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rc := kernel.NewForTest(context.Background(), kernel.Boot{
		Stdout: &stdout,
		Stderr: &stderr,
	}, kernel.Inputs{Format: output.FormatJSON})
	rc.Resolved = &config.Resolved{}

	_, err := listcmd.Run(rc, listcmd.Input{})
	if err == nil {
		t.Fatal("Run: expected error for missing active Account, got nil")
	}
	if got := exit.For(err); got != 10 {
		t.Errorf("exit.For = %d, want 10 (no credential); err = %v", got, err)
	}
	if !strings.Contains(err.Error(), "gplay auth login") {
		t.Errorf("error should point at `gplay auth login`; got %q", err.Error())
	}
}

func TestPayload_jsonShape(t *testing.T) {
	p := listcmd.Payload{Apps: []listcmd.AppRow{
		{Package: "com.example.alpha", Pinned: true},
		{Package: "com.example.beta"},
	}}
	var buf bytes.Buffer
	if err := p.Renderers().JSON(&buf); err != nil {
		t.Fatalf("Renderers().JSON: %v", err)
	}
	var parsed struct {
		Apps []struct {
			Package string `json:"package"`
			Pinned  bool   `json:"pinned"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("Unmarshal: %v (raw=%q)", err, buf.String())
	}
	if len(parsed.Apps) != 2 {
		t.Fatalf("len(Apps) = %d, want 2", len(parsed.Apps))
	}
	if parsed.Apps[0].Package != "com.example.alpha" || !parsed.Apps[0].Pinned {
		t.Errorf("Apps[0] = %+v, want alpha pinned=true", parsed.Apps[0])
	}
	if parsed.Apps[1].Package != "com.example.beta" || parsed.Apps[1].Pinned {
		t.Errorf("Apps[1] = %+v, want beta pinned=false", parsed.Apps[1])
	}
}

func TestPayload_table_empty_rendersPlaceholderOnStdout(t *testing.T) {
	p := listcmd.Payload{Apps: []listcmd.AppRow{}}
	var buf bytes.Buffer
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("Renderers().Table: %v", err)
	}
	if !strings.Contains(buf.String(), "no packages registered") {
		t.Errorf("empty table should emit a placeholder; got %q", buf.String())
	}
}

func TestRun_emptyActiveAccount_writesPlaceholderViaRenderer_notStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rc := newRC(t, &stdout, &stderr, nil)

	if _, err := listcmd.Run(rc, listcmd.Input{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr should be empty after the renderer refactor (empty-state lives in renderers); got %q", stderr.String())
	}
}

func TestPayload_tableHeadersAndPinMarker(t *testing.T) {
	p := listcmd.Payload{Apps: []listcmd.AppRow{
		{Package: "com.example.alpha", Pinned: false},
		{Package: "com.example.beta", Pinned: true},
	}}
	var buf bytes.Buffer
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("Renderers().Table: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"PACKAGE", "PINNED", "com.example.alpha", "com.example.beta"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q; got:\n%s", want, out)
		}
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("table should be header + 2 rows; got %d lines:\n%s", len(lines), out)
	}
	alphaLine, betaLine := lines[1], lines[2]
	pinMarker := "✓"
	if strings.Contains(alphaLine, pinMarker) {
		t.Errorf("unpinned alpha row carries the pin marker %q: %q", pinMarker, alphaLine)
	}
	if !strings.Contains(betaLine, pinMarker) {
		t.Errorf("pinned beta row missing the pin marker %q: %q", pinMarker, betaLine)
	}
}

func runCmd(t *testing.T, cfgPath string, stdout, stderr *bytes.Buffer, args ...string) error {
	t.Helper()
	boot := kernel.Boot{
		Stdout:     stdout,
		Stderr:     stderr,
		ConfigPath: cfgPath,
	}
	sub := listcmd.NewCommand(boot)
	root := &cobra.Command{Use: "gplay"}
	root.AddCommand(sub)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(append([]string{"list"}, args...))
	return root.Execute()
}

func seedGlobal(t *testing.T, packages []string) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	g := &config.Global{Accounts: []config.Account{{Name: "playci", Active: true, Packages: packages}}}
	if err := g.Save(context.Background(), config.OSFS{}, cfgPath); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	return cfgPath
}

func TestList_cobra_extraArg_rejectedByNoArgs(t *testing.T) {
	cfgPath := seedGlobal(t, nil)
	var stdout, stderr bytes.Buffer
	err := runCmd(t, cfgPath, &stdout, &stderr, "com.example.foo")
	if err == nil {
		t.Fatal("Execute: expected error for unexpected positional arg, got nil")
	}
	if !strings.Contains(err.Error(), "unknown command") && !strings.Contains(err.Error(), "unknown") && !strings.Contains(err.Error(), "arg") {
		t.Errorf("error should mention unknown command/argument; got %q", err.Error())
	}
}

func TestList_cobra_emptyRegistry_jsonStaysParseable(t *testing.T) {
	cfgPath := seedGlobal(t, nil)
	var stdout, stderr bytes.Buffer
	if err := runCmd(t, cfgPath, &stdout, &stderr, "--output", "json"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var parsed struct {
		Apps []struct{} `json:"apps"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("stdout should be valid JSON; got %q (err=%v)", stdout.String(), err)
	}
	if len(parsed.Apps) != 0 {
		t.Errorf("Apps should be empty; got %+v", parsed.Apps)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr should be empty in the JSON path; got %q", stderr.String())
	}
}

func TestList_cobra_jsonOutput_passThroughShape(t *testing.T) {
	cfgPath := seedGlobal(t, []string{"com.example.alpha", "com.example.beta"})
	var stdout, stderr bytes.Buffer
	if err := runCmd(t, cfgPath, &stdout, &stderr, "--output", "json"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var parsed struct {
		Apps []struct {
			Package string `json:"package"`
			Pinned  bool   `json:"pinned"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("Unmarshal: %v (raw=%q)", err, stdout.String())
	}
	if len(parsed.Apps) != 2 {
		t.Fatalf("len(Apps) = %d, want 2", len(parsed.Apps))
	}
	if parsed.Apps[0].Package != "com.example.alpha" || parsed.Apps[1].Package != "com.example.beta" {
		t.Errorf("unexpected packages: %+v", parsed.Apps)
	}
}

func TestPayload_markdownTable(t *testing.T) {
	p := listcmd.Payload{Apps: []listcmd.AppRow{
		{Package: "com.example.alpha", Pinned: false},
		{Package: "com.example.beta", Pinned: true},
	}}
	var buf bytes.Buffer
	if err := p.Renderers().Markdown(&buf); err != nil {
		t.Fatalf("Renderers().Markdown: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"| Package | Pinned |",
		"| --- | --- |",
		"| com.example.alpha |  |",
		"| com.example.beta | ✓ |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q; got:\n%s", want, out)
		}
	}
}

func TestList_cobra_malformedInlineCredential_exits10NotFallback(t *testing.T) {
	t.Setenv(resolver.EnvAccount, "")
	t.Setenv(resolver.EnvServiceAccount, "{ not valid json")
	cfgPath := seedGlobal(t, []string{"com.cascade.app"})
	var stdout, stderr bytes.Buffer

	err := runCmd(t, cfgPath, &stdout, &stderr, "--output", "json")
	if err == nil {
		t.Fatal("apps list with a malformed inline credential = nil, want exit 10")
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
	if strings.Contains(stdout.String(), "com.cascade.app") {
		t.Errorf("a malformed inline credential must not list the cascade Account's packages; got %q", stdout.String())
	}
	if out := stdout.String(); !strings.Contains(out, `"exitCode": 10`) {
		t.Errorf("json hard-error path should emit the exit-10 error envelope; got %q", out)
	}
}
