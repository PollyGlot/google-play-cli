package config_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/config"
)

// loadFixture is a `t.TempDir`-rooted scratch space for cascading-loader
// tests. It builds the three on-disk layers as the loader would see them
// in a real repo: global ($XDG/gplay/config.json), project-shared
// (.gplay/config.json), project-local (.gplay/config.local.json).
type loadFixture struct {
	root       string // tempdir root
	homeDir    string // simulated $HOME (walk-up barrier)
	xdgDir     string // simulated $XDG_CONFIG_HOME/gplay
	globalPath string
	repoRoot   string // simulated repo root containing .gplay/
	cwd        string // simulated cwd (a sub-sub-dir of repoRoot)
	sharedPath string // <repoRoot>/.gplay/config.json
	localPath  string // <repoRoot>/.gplay/config.local.json
}

func newLoadFixture(t *testing.T) *loadFixture {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	xdg := filepath.Join(home, ".config", "gplay")
	// repo sits BELOW home so $HOME is genuinely an ancestor: the
	// barrier check has to be ineffective only when the file is found
	// below $HOME, never at/above.
	repo := filepath.Join(home, "work", "myrepo")
	cwd := filepath.Join(repo, "src", "feature")
	if err := os.MkdirAll(xdg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	return &loadFixture{
		root:       root,
		homeDir:    home,
		xdgDir:     xdg,
		globalPath: filepath.Join(xdg, "config.json"),
		repoRoot:   repo,
		cwd:        cwd,
		sharedPath: filepath.Join(repo, ".gplay", "config.json"),
		localPath:  filepath.Join(repo, ".gplay", "config.local.json"),
	}
}

func (f *loadFixture) writeGlobal(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(f.globalPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *loadFixture) writeShared(t *testing.T, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.sharedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.sharedPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *loadFixture) writeLocal(t *testing.T, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.localPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.localPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *loadFixture) loadOpts() config.LoadOptions {
	return config.LoadOptions{
		GlobalPath: f.globalPath,
		StartDir:   f.cwd,
		HomeDir:    f.homeDir,
	}
}

func TestLoad_noFiles_returnsEmptyResolved(t *testing.T) {
	f := newLoadFixture(t)

	r, err := config.Load(context.Background(), config.OSFS{}, f.loadOpts())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.Pin != "" {
		t.Errorf("Pin = %q, want empty", r.Pin)
	}
	if r.ConfigAccount != "" {
		t.Errorf("ConfigAccount = %q, want empty", r.ConfigAccount)
	}
	if len(r.Accounts) != 0 {
		t.Errorf("Accounts = %+v, want empty", r.Accounts)
	}
}

func TestLoad_projectSharedPin_foundViaWalkUp(t *testing.T) {
	f := newLoadFixture(t)
	f.writeShared(t, `{"package":"com.example.app"}`)

	r, err := config.Load(context.Background(), config.OSFS{}, f.loadOpts())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.Pin != "com.example.app" {
		t.Errorf("Pin = %q, want %q (project-shared via walk-up)", r.Pin, "com.example.app")
	}
}

func TestLoad_globalActiveAccount_isResolved(t *testing.T) {
	f := newLoadFixture(t)
	f.writeGlobal(t, `{"accounts":[{"name":"ci","active":true},{"name":"manual"}]}`)

	r, err := config.Load(context.Background(), config.OSFS{}, f.loadOpts())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.ConfigAccount != "ci" {
		t.Errorf("ConfigAccount = %q, want %q (from global active flag)", r.ConfigAccount, "ci")
	}
	if len(r.Accounts) != 2 {
		t.Errorf("Accounts len = %d, want 2", len(r.Accounts))
	}
}

func TestLoad_projectLocal_accountOverridesGlobal(t *testing.T) {
	f := newLoadFixture(t)
	f.writeGlobal(t, `{"accounts":[{"name":"ci","active":true},{"name":"manual"}]}`)
	f.writeLocal(t, `{"account":"manual"}`)

	r, err := config.Load(context.Background(), config.OSFS{}, f.loadOpts())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.ConfigAccount != "manual" {
		t.Errorf("ConfigAccount = %q, want %q (project-local override)", r.ConfigAccount, "manual")
	}
}

func TestLoad_projectShared_accountField_isRejected(t *testing.T) {
	f := newLoadFixture(t)
	f.writeShared(t, `{"package":"com.example.app","account":"ci"}`)

	_, err := config.Load(context.Background(), config.OSFS{}, f.loadOpts())
	if err == nil {
		t.Fatal("Load: expected rejection of account field in project-shared config")
	}
	// The error must point at the offending file (per PRD).
	if !strings.Contains(err.Error(), f.sharedPath) {
		t.Errorf("error %q must mention %q", err.Error(), f.sharedPath)
	}
	if !strings.Contains(err.Error(), "account") {
		t.Errorf("error %q must mention the offending field name", err.Error())
	}
}

// `"account": null` is still presence of the forbidden field: must be
// rejected the same as a non-null value.
func TestLoad_projectShared_accountFieldExplicitlyNull_isRejected(t *testing.T) {
	f := newLoadFixture(t)
	f.writeShared(t, `{"package":"com.example.app","account":null}`)

	_, err := config.Load(context.Background(), config.OSFS{}, f.loadOpts())
	if err == nil {
		t.Fatal("Load: expected rejection of account:null in project-shared config")
	}
	if !strings.Contains(err.Error(), "account") {
		t.Errorf("error %q must mention the offending field name", err.Error())
	}
}

func TestLoad_projectLocal_accountField_isAccepted(t *testing.T) {
	f := newLoadFixture(t)
	f.writeLocal(t, `{"account":"manual"}`)

	r, err := config.Load(context.Background(), config.OSFS{}, f.loadOpts())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.ConfigAccount != "manual" {
		t.Errorf("ConfigAccount = %q, want %q", r.ConfigAccount, "manual")
	}
}

func TestLoad_projectShared_atHomeDir_isIgnored(t *testing.T) {
	f := newLoadFixture(t)
	// Place a .gplay/config.json directly in $HOME: must NOT be picked
	// up via walk-up, even though it sits between cwd and the root.
	if err := os.MkdirAll(filepath.Join(f.homeDir, ".gplay"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.homeDir, ".gplay", "config.json"),
		[]byte(`{"package":"rogue"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	r, err := config.Load(context.Background(), config.OSFS{}, f.loadOpts())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.Pin != "" {
		t.Errorf("Pin = %q, want empty (rogue $HOME .gplay must be ignored)", r.Pin)
	}
}

func TestLoad_malformedJSON_returnsError(t *testing.T) {
	f := newLoadFixture(t)
	f.writeShared(t, `{not-json`)

	_, err := config.Load(context.Background(), config.OSFS{}, f.loadOpts())
	if err == nil {
		t.Fatal("Load: expected error for malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), f.sharedPath) {
		t.Errorf("error %q must mention offending file", err.Error())
	}
}

func TestLoad_paths_arePopulatedWhenFilesExist(t *testing.T) {
	f := newLoadFixture(t)
	f.writeGlobal(t, `{"accounts":[]}`)
	f.writeShared(t, `{"package":"com.example.app"}`)
	f.writeLocal(t, `{"account":"manual"}`)

	r, err := config.Load(context.Background(), config.OSFS{}, f.loadOpts())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.GlobalPath != f.globalPath {
		t.Errorf("GlobalPath = %q, want %q", r.GlobalPath, f.globalPath)
	}
	if r.ProjectSharedPath != f.sharedPath {
		t.Errorf("ProjectSharedPath = %q, want %q", r.ProjectSharedPath, f.sharedPath)
	}
	if r.ProjectLocalPath != f.localPath {
		t.Errorf("ProjectLocalPath = %q, want %q", r.ProjectLocalPath, f.localPath)
	}
}

// Sanity: the loader must not error on a nonexistent global path: the
// global is created lazily by `auth login`.
func TestLoad_missingGlobal_isOK(t *testing.T) {
	f := newLoadFixture(t)
	if _, err := os.Stat(f.globalPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("precondition: globalPath should not exist, stat err = %v", err)
	}
	if _, err := config.Load(context.Background(), config.OSFS{}, f.loadOpts()); err != nil {
		t.Errorf("Load (missing global): %v", err)
	}
}
