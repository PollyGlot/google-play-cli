package list_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/auth/list"
	"github.com/PollyGlot/google-play-cli/internal/config"
)

func newOpts(t *testing.T) list.Options {
	t.Helper()
	return list.Options{
		ConfigPath: filepath.Join(t.TempDir(), "config.json"),
	}
}

func seed(t *testing.T, opts list.Options, names ...string) {
	t.Helper()
	cfg := &config.Config{}
	for _, n := range names {
		cfg.AddAccount(n)
	}
	if len(names) > 0 {
		if err := cfg.SetActive(names[0]); err != nil {
			t.Fatalf("SetActive: %v", err)
		}
	}
	if err := cfg.Save(opts.ConfigPath); err != nil {
		t.Fatalf("cfg.Save: %v", err)
	}
}

func runCmd(t *testing.T, opts list.Options, stdout, stderr *bytes.Buffer, args ...string) error {
	t.Helper()
	sub := list.NewCommand(opts)
	root := &cobra.Command{Use: "gplay"}
	root.AddCommand(sub)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(append([]string{"list"}, args...))
	return root.Execute()
}

func TestList_emptyRegistry_prints_noAccountsLine(t *testing.T) {
	opts := newOpts(t)
	var stdout, stderr bytes.Buffer
	if err := runCmd(t, opts, &stdout, &stderr); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(stdout.String(), "no accounts registered") {
		t.Errorf("expected empty-registry line; got %q", stdout.String())
	}
}

func TestList_table_marksActive(t *testing.T) {
	opts := newOpts(t)
	seed(t, opts, "alpha", "beta", "gamma") // alpha is active

	var stdout, stderr bytes.Buffer
	if err := runCmd(t, opts, &stdout, &stderr); err != nil {
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

func TestList_json_passThroughShape(t *testing.T) {
	opts := newOpts(t)
	seed(t, opts, "alpha", "beta")

	var stdout, stderr bytes.Buffer
	if err := runCmd(t, opts, &stdout, &stderr, "--output", "json"); err != nil {
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
