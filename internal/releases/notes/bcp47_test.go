package notes_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/releases/notes"
)

func TestValidBCP47(t *testing.T) {
	valid := []string{
		"en", "fr", "ar", "fil",
		"en-US", "pt-BR", "fr-CA", "zh-CN",
		"es-419",
		"zh-Hant-TW",
		"zh-Hant",
		"sr-Latn-RS",
		"de-CH-1901",
		"sl-rozaj",
		"xy-ZZ",
		"EN-us",
		"zh-yue-Hant-CN",
	}
	for _, s := range valid {
		if !notes.ValidBCP47(s) {
			t.Errorf("ValidBCP47(%q) = false, want true", s)
		}
	}

	invalid := []string{
		"",
		"en_US",
		"en-",
		"-US",
		"e",
		"klingon",
		"en--US",
		"en US",
		"en.US",
		"123",
		"en-US-",
		"français",
		"en-US-u-co-phonebk",
		"en-x-private",
	}
	for _, s := range invalid {
		if notes.ValidBCP47(s) {
			t.Errorf("ValidBCP47(%q) = true, want false", s)
		}
	}
}

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

func TestValidateDirLocales_emptyDirIsNoop(t *testing.T) {
	if err := notes.ValidateDirLocales(""); err != nil {
		t.Errorf("ValidateDirLocales(\"\") = %v, want nil", err)
	}
}

func TestValidateDirLocales_missingDirIsRejectedHere(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	err := notes.ValidateDirLocales(missing)
	if err == nil {
		t.Fatal("ValidateDirLocales on a missing dir = nil, want a rejection before the Edit")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) || coder.ExitCode() != 2 {
		t.Errorf("err = %v (%T), want ExitCode() 2 (CLI misuse)", err, err)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("err = %q, want it to name the directory %q", err, missing)
	}
}

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
