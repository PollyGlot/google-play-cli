package initcmd_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/apps/initcmd"
	"github.com/PollyGlot/google-play-cli/internal/config"
)

func runCmd(t *testing.T, opts initcmd.Options, args ...string) (string, string, error) {
	t.Helper()
	cmd := initcmd.NewCommand(opts)
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func TestInitCmd_writesPinAndGitignore(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()

	_, stderr, err := runCmd(t, initcmd.Options{RepoRoot: repo, HomeDir: home},
		"--package", "com.example.myapp")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, err := os.Stat(filepath.Join(repo, ".gplay", "config.json")); err != nil {
		t.Errorf("config.json not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".gplay", ".gitignore")); err != nil {
		t.Errorf(".gitignore not written: %v", err)
	}
	if !strings.Contains(stderr, "com.example.myapp") {
		t.Errorf("stderr should mention pinned package; got %q", stderr)
	}
}

func TestInitCmd_inHomeDir_refuses(t *testing.T) {
	home := t.TempDir()
	_, _, err := runCmd(t, initcmd.Options{RepoRoot: home, HomeDir: home},
		"--package", "com.example.myapp")
	if err == nil {
		t.Fatal("Execute in $HOME: expected error, got nil")
	}
	// No files should have been written.
	if _, err := os.Stat(filepath.Join(home, ".gplay", "config.json")); err == nil {
		t.Error("config.json should not exist after refusing $HOME init")
	}
}

func TestInitCmd_emitsRegistryHint(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()

	_, stderr, err := runCmd(t, initcmd.Options{RepoRoot: repo, HomeDir: home},
		"--package", "com.example.myapp")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(stderr, "gplay apps add com.example.myapp") {
		t.Errorf("stderr should hint at `gplay apps add`; got %q", stderr)
	}
}

// After init, Load() finds the pin via the cascading loader — end-to-end
// proof that the tracer bullet works.
func TestInitCmd_pinIsReadableByLoad(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()

	if _, _, err := runCmd(t, initcmd.Options{RepoRoot: repo, HomeDir: home},
		"--package", "com.example.myapp"); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Sub-sub-directory of repo: walk-up must still find the pin.
	subSub := filepath.Join(repo, "src", "feature", "deep")
	if err := os.MkdirAll(subSub, 0o755); err != nil {
		t.Fatal(err)
	}

	resolved, err := config.Load(config.LoadOptions{
		GlobalPath: filepath.Join(home, ".gplay-global-not-needed-for-this-test.json"),
		StartDir:   subSub,
		HomeDir:    home,
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if resolved.Pin != "com.example.myapp" {
		t.Errorf("Resolved.Pin = %q, want %q", resolved.Pin, "com.example.myapp")
	}
}

func TestInitCmd_missingPackageFlag_returnsErrorBeforeWrite(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()

	cmd := initcmd.NewCommand(initcmd.Options{RepoRoot: repo, HomeDir: home})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{})
	// Cobra surfaces MarkFlagRequired errors with usage; the parent root
	// suppresses usage on RunE errors but here we run the command directly,
	// so silencing usage to keep stderr predictable.
	cobraSilent(cmd)

	if err := cmd.Execute(); err == nil {
		t.Fatal("Execute without --package: expected error, got nil")
	}
	if _, err := os.Stat(filepath.Join(repo, ".gplay")); err == nil {
		t.Error(".gplay/ should not be created when --package is missing")
	}
}

func cobraSilent(c *cobra.Command) {
	c.SilenceUsage = true
	c.SilenceErrors = true
}
