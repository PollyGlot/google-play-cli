package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/pathguard"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func writeLocale(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "title.txt"), []byte("My App"))
	writeFile(t, filepath.Join(dir, "full_description.txt"), []byte("A perfectly fine description."))
}

func metadataTreeWithLocaleLink(t *testing.T, target string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "m")
	writeLocale(t, filepath.Join(dir, "en-US"))
	symlink(t, target, filepath.Join(dir, "de-DE"))
	return dir
}

func escapingMetadataTree(t *testing.T) string {
	t.Helper()
	shared := t.TempDir()
	writeLocale(t, filepath.Join(shared, "de-DE"))
	return metadataTreeWithLocaleLink(t, filepath.Join(shared, "de-DE"))
}

type escapeCase struct {
	name      string
	args      func(t *testing.T) []string
	hatchExit int
}

func escapeCases() []escapeCase {
	return []escapeCase{
		{"metadata validate", func(t *testing.T) []string {
			t.Helper()
			return []string{"metadata", "validate", "--dir", escapingMetadataTree(t), "--output", "json"}
		}, 0},
		{"metadata apply --dry-run", func(t *testing.T) []string {
			t.Helper()
			return []string{"metadata", "apply", "--dir", escapingMetadataTree(t), "--dry-run", "--package", "com.example.app", "--output", "json"}
		}, 10},
		{"metadata images validate", func(t *testing.T) []string {
			t.Helper()
			shared := t.TempDir()
			writeFile(t, filepath.Join(shared, "icon.png"), testkit.PNG(512, 512))
			dir := filepath.Join(t.TempDir(), "img")
			symlink(t, filepath.Join(shared, "icon.png"), filepath.Join(dir, "en-US", "images", "icon.png"))
			return []string{"metadata", "images", "validate", "--dir", dir, "--output", "json"}
		}, 0},
		{"releases upload --release-notes-dir --dry-run", func(t *testing.T) []string {
			t.Helper()
			shared := t.TempDir()
			writeFile(t, filepath.Join(shared, "en-US.txt"), []byte("Shared notes"))
			work := t.TempDir()
			aab := filepath.Join(work, "a.aab")
			writeFile(t, aab, []byte("aab"))
			notesDir := filepath.Join(work, "notes")
			symlink(t, filepath.Join(shared, "en-US.txt"), filepath.Join(notesDir, "en-US.txt"))
			return []string{"releases", "upload", aab, "--track", "internal", "--release-notes-dir", notesDir, "--dry-run", "--skip-preflight", "--package", "com.example.app", "--output", "json"}
		}, 0},
	}
}

func isolateTreeRun(t *testing.T, hatch string) *bytes.Buffer {
	t.Helper()
	t.Setenv(output.EnvDefaultOutput, "")
	t.Setenv(resolver.EnvServiceAccount, "")
	t.Setenv(resolver.EnvAccount, "")
	t.Setenv(pathguard.EnvAllowExternalSymlinks, hatch)
	var notes bytes.Buffer
	t.Cleanup(pathguard.SetNoteWriter(&notes))
	return &notes
}

func TestContainmentRefusal_exitsUsageFromTreeCommands(t *testing.T) {
	for _, tc := range escapeCases() {
		t.Run(tc.name, func(t *testing.T) {
			notes := isolateTreeRun(t, "")
			stdout, code, err := runLikeMain(t, tc.args(t)...)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2 for an escaping symlink; err=%v", code, err)
			}
			d := envelopeOf(t, stdout)
			if d.Code != string(exit.CodeUsageError) || d.ExitCode != 2 {
				t.Errorf("envelope code/exitCode = %s/%d, want USAGE_ERROR/2", d.Code, d.ExitCode)
			}
			if !strings.Contains(d.Message, pathguard.EnvAllowExternalSymlinks) {
				t.Errorf("the refusal does not name %s: %s", pathguard.EnvAllowExternalSymlinks, d.Message)
			}
			if notes.Len() != 0 {
				t.Errorf("a refusal must not emit a NOTE, got %q", notes.String())
			}
		})
	}
}

func TestContainmentRefusal_hatchLetsTreeCommandsGoOn(t *testing.T) {
	for _, tc := range escapeCases() {
		t.Run(tc.name, func(t *testing.T) {
			notes := isolateTreeRun(t, "1")
			_, code, err := runLikeMain(t, tc.args(t)...)
			if code != tc.hatchExit {
				t.Fatalf("exit code = %d, want %d once the hatch lets the run go on; err=%v", code, tc.hatchExit, err)
			}
			if !strings.HasPrefix(notes.String(), "NOTE: ") {
				t.Errorf("want a NOTE line for the followed link, got %q", notes.String())
			}
		})
	}
}

func TestBrokenLocaleLink_staysValidationFailure(t *testing.T) {
	cases := []struct {
		name   string
		target string
	}{
		{"dangling", "missing"},
		{"link to a file", filepath.Join("en-US", "title.txt")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateTreeRun(t, "")
			dir := metadataTreeWithLocaleLink(t, tc.target)
			stdout, code, err := runLikeMain(t, "metadata", "validate", "--dir", dir, "--output", "json")
			if code != 20 {
				t.Fatalf("exit code = %d, want 20 for a broken locale link; err=%v", code, err)
			}
			if d := envelopeOf(t, stdout); d.Code != string(exit.CodeValidationFailed) {
				t.Errorf("envelope code = %s, want VALIDATION_FAILED", d.Code)
			}
		})
	}
}
