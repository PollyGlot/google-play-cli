package list_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/auth/list"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func newBoot(t *testing.T) kernel.Boot {
	t.Helper()
	return kernel.Boot{
		ConfigPath: filepath.Join(t.TempDir(), "config.json"),
	}
}

func seed(t *testing.T, boot kernel.Boot, names ...string) {
	t.Helper()
	cfg := &config.Global{}
	for _, n := range names {
		cfg.AddAccount(n)
	}
	if len(names) > 0 {
		if err := cfg.SetActive(names[0]); err != nil {
			t.Fatalf("SetActive: %v", err)
		}
	}
	if err := cfg.Save(context.Background(), config.OSFS{}, boot.ConfigPath); err != nil {
		t.Fatalf("cfg.Save: %v", err)
	}
}

func runCmd(t *testing.T, boot kernel.Boot, stdout, stderr *bytes.Buffer, args ...string) error {
	t.Helper()
	boot.Stdout = stdout
	boot.Stderr = stderr
	sub := list.NewCommand(boot)
	root := &cobra.Command{Use: "gplay"}
	root.AddCommand(sub)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(append([]string{"list"}, args...))
	return root.Execute()
}

// TestRun_pureBusiness exercises the business function without cobra,
// using a hand-built kernel.RunContext. Run reads rc.Resolved.Accounts
// directly; no kernel.Run plumbing needed.
func TestRun_pureBusiness(t *testing.T) {
	rc := kernel.NewForTest(context.Background(), kernel.Boot{}, kernel.Inputs{Format: output.FormatJSON})
	rc.Resolved = &config.Resolved{
		Accounts: []config.Account{
			{Name: "alpha", Active: true},
			{Name: "beta"},
		},
	}

	r, err := list.Run(rc, list.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	p, ok := r.(list.Payload)
	if !ok {
		t.Fatalf("Run returned %T, want list.Payload", r)
	}
	if len(p.Accounts) != 2 {
		t.Fatalf("len(Accounts) = %d, want 2", len(p.Accounts))
	}
	if p.Accounts[0].Name != "alpha" || !p.Accounts[0].Active {
		t.Errorf("Accounts[0] = %+v, want alpha active=true", p.Accounts[0])
	}
}

func TestList_emptyRegistry_prints_noAccountsLine(t *testing.T) {
	boot := newBoot(t)
	var stdout, stderr bytes.Buffer
	if err := runCmd(t, boot, &stdout, &stderr, "--output", "table"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(stdout.String(), "no accounts registered") {
		t.Errorf("expected empty-registry line; got %q", stdout.String())
	}
}

func TestList_table_marksActive(t *testing.T) {
	boot := newBoot(t)
	seed(t, boot, "alpha", "beta", "gamma") // alpha is active

	var stdout, stderr bytes.Buffer
	if err := runCmd(t, boot, &stdout, &stderr, "--output", "table"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := stdout.String()
	// Active account line starts with "*"
	if !strings.Contains(out, "* alpha") {
		t.Errorf("table should mark alpha as active with '* '; got %q", out)
	}
	// Inactive accounts are present with leading spaces.
	for _, n := range []string{"beta", "gamma"} {
		if !strings.Contains(out, "  "+n) {
			t.Errorf("table missing inactive %q; got %q", n, out)
		}
	}
}

func TestList_markdownOutput_emitsMarkdownTable(t *testing.T) {
	boot := newBoot(t)
	seed(t, boot, "alpha", "beta")

	var stdout, stderr bytes.Buffer
	if err := runCmd(t, boot, &stdout, &stderr, "--output", "markdown"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"| Account | Active |",
		"| --- | --- |",
		"| alpha | * |",
		"| beta |  |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q; got:\n%s", want, out)
		}
	}
}

func TestList_markdownOutput_emptyRegistry(t *testing.T) {
	boot := newBoot(t)
	var stdout, stderr bytes.Buffer
	if err := runCmd(t, boot, &stdout, &stderr, "--output", "markdown"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(stdout.String(), "_No accounts registered._") {
		t.Errorf("empty markdown should be a single paragraph; got %q", stdout.String())
	}
}

func TestList_defaultNonTTY_emitsJSON(t *testing.T) {
	t.Setenv("CI", "")
	outputtest.ForceTerminal(t, false)
	boot := newBoot(t)
	seed(t, boot, "alpha")
	var stdout, stderr bytes.Buffer

	if err := runCmd(t, boot, &stdout, &stderr); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var parsed struct {
		Accounts []struct{ Name string } `json:"accounts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("non-TTY default should be JSON; got %q (err=%v)", stdout.String(), err)
	}
	if len(parsed.Accounts) != 1 || parsed.Accounts[0].Name != "alpha" {
		t.Errorf("unexpected payload: %+v", parsed)
	}
}

func TestList_defaultCIEnv_emitsJSON_evenOnTTY(t *testing.T) {
	t.Setenv("CI", "true")
	outputtest.ForceTerminal(t, true)
	boot := newBoot(t)
	seed(t, boot, "alpha")
	var stdout, stderr bytes.Buffer

	if err := runCmd(t, boot, &stdout, &stderr); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := json.Unmarshal(stdout.Bytes(), &struct{}{}); err != nil {
		t.Errorf("CI=true should force JSON on TTY; got %q", stdout.String())
	}
}

func TestList_explicitTableInPipe_overridesAutoJSON(t *testing.T) {
	t.Setenv("CI", "")
	outputtest.ForceTerminal(t, false)
	boot := newBoot(t)
	seed(t, boot, "alpha")
	var stdout, stderr bytes.Buffer

	if err := runCmd(t, boot, &stdout, &stderr, "--output", "table"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(stdout.String(), "* alpha") {
		t.Errorf("explicit --output table must win even in pipe; got %q", stdout.String())
	}
}

func TestList_unknownOutput_returnsErrorMentioningValidSet(t *testing.T) {
	boot := newBoot(t)
	seed(t, boot, "alpha")
	var stdout, stderr bytes.Buffer

	err := runCmd(t, boot, &stdout, &stderr, "--output", "xml")
	if err == nil {
		t.Fatal("expected error on --output xml")
	}
	for _, want := range []string{"unsupported", "table", "json", "markdown"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestList_json_passThroughShape(t *testing.T) {
	boot := newBoot(t)
	seed(t, boot, "alpha", "beta")

	var stdout, stderr bytes.Buffer
	if err := runCmd(t, boot, &stdout, &stderr, "--output", "json"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var parsed struct {
		Accounts []struct {
			Name   string `json:"name"`
			Active bool   `json:"active"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("Unmarshal: %v (raw=%q)", err, stdout.String())
	}
	if len(parsed.Accounts) != 2 {
		t.Fatalf("len(Accounts) = %d, want 2", len(parsed.Accounts))
	}
	if parsed.Accounts[0].Name != "alpha" || !parsed.Accounts[0].Active {
		t.Errorf("Accounts[0] = %+v, want alpha active=true", parsed.Accounts[0])
	}
	if parsed.Accounts[1].Name != "beta" || parsed.Accounts[1].Active {
		t.Errorf("Accounts[1] = %+v, want beta active=false", parsed.Accounts[1])
	}
}
