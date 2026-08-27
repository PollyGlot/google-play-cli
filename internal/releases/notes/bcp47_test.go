package notes_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/releases/notes"
)

// TestValidBCP47 is the grammar table for PRD #446 / #452: well-formedness,
// not registration. `xy-ZZ` is shaped like a tag and is Play's to refuse; the
// rejected column is what a human or an agent actually typos.
func TestValidBCP47(t *testing.T) {
	valid := []string{
		"en", "fr", "ar", "fil", // bare language, 2 and 3 letters
		"en-US", "pt-BR", "fr-CA", "zh-CN", // language-REGION
		"es-419",         // UN M.49 numeric region
		"zh-Hant-TW",     // script + region
		"zh-Hant",        // script only
		"sr-Latn-RS",     // the other classic script case
		"de-CH-1901",     // digit-led variant
		"sl-rozaj",       // alphabetic variant
		"xy-ZZ",          // well-formed but unregistered: Play's call, not ours
		"EN-us",          // tags are case-insensitive per RFC 5646 §2.1
		"zh-yue-Hant-CN", // extlang
	}
	for _, s := range valid {
		if !notes.ValidBCP47(s) {
			t.Errorf("ValidBCP47(%q) = false, want true", s)
		}
	}

	invalid := []string{
		"",         // no tag at all
		"en_US",    // THE typo this gate exists for: underscore, not hyphen
		"en-",      // trailing separator
		"-US",      // missing primary subtag
		"e",        // 1-letter primary
		"klingon",  // 7-letter primary: not a language subtag in practice
		"en--US",   // empty subtag
		"en US",    // space
		"en.US",    // dot
		"123",      // digits where a language belongs
		"en-US-",   // trailing separator after a full tag
		"français", // non-ASCII
		// Singleton extension / private-use subtags (RFC 5646 §2.2.6-7). No
		// Play store locale carries one, and accepting them would open the
		// grammar to arbitrary 1-letter subtags for nothing.
		"en-US-u-co-phonebk",
		"en-x-private",
	}
	for _, s := range invalid {
		if notes.ValidBCP47(s) {
			t.Errorf("ValidBCP47(%q) = true, want false", s)
		}
	}
}

// TestValidateDirLocales_listsEveryOffendingFile is the acceptance criterion
// that saves a round-trip per typo: a directory with three malformed names
// fails ONCE, naming all three, instead of failing on the first and making the
// user re-run to discover the next.
func TestValidateDirLocales_listsEveryOffendingFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "en_US.txt", "underscore typo")
	writeFile(t, dir, "pt_BR.txt", "another one")
	writeFile(t, dir, "klingon.txt", "not a language subtag")
	writeFile(t, dir, "fr-FR.txt", "this one is fine")

	err := notes.ValidateDirLocales(dir)
	if err == nil {
		t.Fatal("ValidateDirLocales: got nil, want a validation error")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) || coder.ExitCode() != 2 {
		t.Errorf("err = %v (%T), want ExitCode() 2 (CLI misuse)", err, err)
	}
	for _, want := range []string{"en_US.txt", "pt_BR.txt", "klingon.txt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to name %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "fr-FR.txt") {
		t.Errorf("error = %q, want it to leave the valid locale out", err)
	}
}

// TestValidateDirLocales_acceptsValidTree asserts the gate stays out of the way
// of the shapes people really ship, including a script/region variant and the
// `default.txt` marker (which is a fallback flag, not a locale).
func TestValidateDirLocales_acceptsValidTree(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "en-US.txt", "a")
	writeFile(t, dir, "zh-Hant-TW.txt", "b")
	writeFile(t, dir, "es-419.txt", "c")
	writeFile(t, dir, "default.txt", "d")
	writeFile(t, dir, "README.md", "not a notes file")
	if err := os.Mkdir(filepath.Join(dir, "archive"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	if err := notes.ValidateDirLocales(dir); err != nil {
		t.Errorf("ValidateDirLocales: %v, want nil", err)
	}
}

// TestValidateDirLocales_emptyDirIsNoop pins the "no --release-notes-dir" case:
// the gate is called unconditionally from the orchestrator's validateOpts, so
// it must be silent when there is nothing to validate.
func TestValidateDirLocales_emptyDirIsNoop(t *testing.T) {
	if err := notes.ValidateDirLocales(""); err != nil {
		t.Errorf("ValidateDirLocales(\"\") = %v, want nil", err)
	}
	if err := notes.ValidateDirLocales(filepath.Join(t.TempDir(), "does-not-exist")); err != nil {
		t.Errorf("ValidateDirLocales on a missing dir = %v, want nil (Load owns that error)", err)
	}
}

// TestLoad_dirMode_invalidLocale_returnsExit2 pins the defence in depth: even a
// caller that skips the orchestrator's pre-Edit gate cannot get a malformed
// locale past Load and onto the wire.
func TestLoad_dirMode_invalidLocale_returnsExit2(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "en_US.txt", "underscore typo")

	_, err := notes.Load(notes.Opts{Dir: dir, DefaultLanguage: "en-US"})
	if err == nil {
		t.Fatal("Load: got nil, want a validation error on en_US.txt")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) || coder.ExitCode() != 2 {
		t.Errorf("err = %v (%T), want ExitCode() 2", err, err)
	}
}
