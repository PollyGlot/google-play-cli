// Package notes_test exercises the release-notes loader against
// hand-built Opts and t.TempDir() directory layouts.
package notes_test

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/releases/notes"
)

// TestLoad_textMode_singleEntryForDefaultLanguage asserts that --release-notes
// "<text>" produces exactly one entry, assigned to the app's default
// language.
func TestLoad_textMode_singleEntryForDefaultLanguage(t *testing.T) {
	got, err := notes.Load(notes.Opts{
		Text:            "Bug fixes and performance improvements.",
		DefaultLanguage: "en-US",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []notes.LocaleNote{
		{Locale: "en-US", Text: "Bug fixes and performance improvements."},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries (%v), want %d (%v)", len(got), got, len(want), want)
	}
	if got[0] != want[0] {
		t.Errorf("got %v, want %v", got[0], want[0])
	}
}

// writeFile is a tiny helper that creates a file in dir with given
// content, t.Fatal-ing on error so tests stay focused on assertions.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile %s: %v", name, err)
	}
}

// sortNotes returns a fresh slice sorted by Locale so map-iteration
// order doesn't make assertions flaky.
func sortNotes(in []notes.LocaleNote) []notes.LocaleNote {
	out := make([]notes.LocaleNote, len(in))
	copy(out, in)
	sort.Slice(out, func(i, j int) bool { return out[i].Locale < out[j].Locale })
	return out
}

// TestLoad_dirMode_perLocaleFiles_emitsAllLocales asserts that
// --release-notes-dir walks the directory and emits one entry per
// `<locale>.txt` file. The DefaultLanguage is not relevant when every
// locale has its own file.
func TestLoad_dirMode_perLocaleFiles_emitsAllLocales(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "en-US.txt", "English notes")
	writeFile(t, dir, "fr-FR.txt", "Notes en français")

	got, err := notes.Load(notes.Opts{
		Dir:             dir,
		DefaultLanguage: "en-US",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got = sortNotes(got)
	want := []notes.LocaleNote{
		{Locale: "en-US", Text: "English notes"},
		{Locale: "fr-FR", Text: "Notes en français"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("entry %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

// TestLoad_dirMode_defaultTxt_emittedAsDefaultLanguage asserts that a
// lone `default.txt` in the directory is shipped as the DefaultLanguage
// entry — the simplest "all locales fall back to the same text" case.
func TestLoad_dirMode_defaultTxt_emittedAsDefaultLanguage(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "default.txt", "Fallback notes")

	got, err := notes.Load(notes.Opts{
		Dir:             dir,
		DefaultLanguage: "en-US",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []notes.LocaleNote{
		{Locale: "en-US", Text: "Fallback notes"},
	}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestLoad_dirMode_explicitLocaleWinsOverDefaultTxt asserts the
// precedence rule: when both `<DefaultLanguage>.txt` and `default.txt`
// exist, the explicit per-locale file wins and default.txt does NOT
// produce a duplicate entry.
func TestLoad_dirMode_explicitLocaleWinsOverDefaultTxt(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "default.txt", "Fallback content")
	writeFile(t, dir, "en-US.txt", "Specific English content")

	got, err := notes.Load(notes.Opts{
		Dir:             dir,
		DefaultLanguage: "en-US",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries (%v), want 1 (en-US.txt should beat default.txt)", len(got), got)
	}
	if got[0].Locale != "en-US" || got[0].Text != "Specific English content" {
		t.Errorf("got %v, want en-US/'Specific English content'", got[0])
	}
}

// TestLoad_bothTextAndDir_returnsExit2Error asserts the CLI-misuse
// guardrail: --release-notes and --release-notes-dir are mutually
// exclusive, so passing both must return an error carrying
// ExitCode()=2 (CLI misuse per docs/DESIGN.md §9).
func TestLoad_bothTextAndDir_returnsExit2Error(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "en-US.txt", "irrelevant")

	_, err := notes.Load(notes.Opts{
		Text:            "Some text",
		Dir:             dir,
		DefaultLanguage: "en-US",
	})
	if err == nil {
		t.Fatal("Load(Text+Dir): want error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != 2 {
		t.Errorf("ExitCode() = %d, want 2", coder.ExitCode())
	}
}

// TestLoad_neitherTextNorDir_returnsEmpty asserts uploading without
// release notes is valid: Load returns an empty slice and no error.
func TestLoad_neitherTextNorDir_returnsEmpty(t *testing.T) {
	got, err := notes.Load(notes.Opts{DefaultLanguage: "en-US"})
	if err != nil {
		t.Fatalf("Load(empty opts): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty slice", got)
	}
}

// TestLoad_textMode_missingDefaultLanguage_returnsExit2 asserts the
// fail-fast: --release-notes without a DefaultLanguage has no locale
// to assign to and must reject (exit 2) rather than silently emitting
// an empty-locale entry.
func TestLoad_textMode_missingDefaultLanguage_returnsExit2(t *testing.T) {
	_, err := notes.Load(notes.Opts{Text: "Bug fixes"})
	if err == nil {
		t.Fatal("Load(Text without DefaultLanguage): want error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != 2 {
		t.Errorf("ExitCode() = %d, want 2", coder.ExitCode())
	}
}

// TestLoad_defaultTxt_missingDefaultLanguage_returnsExit2 asserts the
// same fail-fast for the dir-mode fallback: default.txt present + no
// DefaultLanguage = no locale to assign the fallback to.
func TestLoad_defaultTxt_missingDefaultLanguage_returnsExit2(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "default.txt", "Fallback")

	_, err := notes.Load(notes.Opts{Dir: dir})
	if err == nil {
		t.Fatal("Load(default.txt without DefaultLanguage): want error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != 2 {
		t.Errorf("ExitCode() = %d, want 2", coder.ExitCode())
	}
}
