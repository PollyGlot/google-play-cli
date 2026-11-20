package validatecmd_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	validatecmd "github.com/PollyGlot/google-play-cli/commands/metadata/validate"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/metadata/tree"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

func newRC(t *testing.T) (*kernel.RunContext, *bytes.Buffer) {
	t.Helper()
	var stdout bytes.Buffer
	boot := kernel.Boot{Stdout: &stdout}
	rc := kernel.NewForTest(context.Background(), boot, kernel.Inputs{Format: output.FormatJSON})
	return rc, &stdout
}

func writeTree(t *testing.T, tr listing.Tree) string {
	t.Helper()
	dir := t.TempDir()
	if err := tree.Write(dir, tr); err != nil {
		t.Fatalf("tree.Write: %v", err)
	}
	return dir
}

func completeListing(locale string) listing.Listing {
	l := listing.NewListing(locale)
	l.Set(listing.Title, "My App")
	l.Set(listing.FullDescription, "A perfectly fine description.")
	return l
}

func exitCodeOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	return coder.ExitCode()
}

func TestRun_validTree_success(t *testing.T) {
	dir := writeTree(t, listing.Tree{"en-US": completeListing("en-US")})
	rc, _ := newRC(t)

	r, err := validatecmd.Run(rc, validatecmd.Input{Dir: dir})
	if err != nil {
		t.Fatalf("Run on valid tree: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable on success")
	}

	var jsonOut bytes.Buffer
	if err := r.Renderers().JSON(&jsonOut); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if !strings.Contains(jsonOut.String(), "en-US") {
		t.Errorf("success JSON %q should mention the validated locale", jsonOut.String())
	}
}

func TestRun_charLimit_exit20(t *testing.T) {
	l := listing.NewListing("en-US")
	l.Set(listing.Title, strings.Repeat("a", 31))
	l.Set(listing.FullDescription, "fine")
	dir := writeTree(t, listing.Tree{"en-US": l})

	rc, _ := newRC(t)
	_, err := validatecmd.Run(rc, validatecmd.Input{Dir: dir})
	if code := exitCodeOf(t, err); code != 20 {
		t.Errorf("ExitCode() = %d, want 20", code)
	}
	if !strings.Contains(err.Error(), "exceeds") || !strings.Contains(err.Error(), "title") {
		t.Errorf("error %q should actionably name the title char limit", err.Error())
	}
}

func TestRun_requiredEmpty_exit20(t *testing.T) {
	l := listing.NewListing("en-US")
	l.Set(listing.Title, "My App")
	l.Set(listing.FullDescription, "")
	dir := writeTree(t, listing.Tree{"en-US": l})

	rc, _ := newRC(t)
	_, err := validatecmd.Run(rc, validatecmd.Input{Dir: dir})
	if code := exitCodeOf(t, err); code != 20 {
		t.Errorf("ExitCode() = %d, want 20", code)
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error %q should explain the empty-required-field rule", err.Error())
	}
}

func TestRun_requiredMissing_exit20(t *testing.T) {
	l := listing.NewListing("en-US")
	l.Set(listing.FullDescription, "fine")
	dir := writeTree(t, listing.Tree{"en-US": l})

	rc, _ := newRC(t)
	_, err := validatecmd.Run(rc, validatecmd.Input{Dir: dir})
	if code := exitCodeOf(t, err); code != 20 {
		t.Errorf("ExitCode() = %d, want 20", code)
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("error %q should say the field is required", err.Error())
	}
}

func TestRun_unknownLocale_exit20_thenAllowLocaleWhitelists(t *testing.T) {
	dir := writeTree(t, listing.Tree{"pt-XX": completeListing("pt-XX")})

	rc, _ := newRC(t)
	_, err := validatecmd.Run(rc, validatecmd.Input{Dir: dir})
	if code := exitCodeOf(t, err); code != 20 {
		t.Errorf("unknown locale ExitCode() = %d, want 20", code)
	}
	if !strings.Contains(err.Error(), "--allow-locale") {
		t.Errorf("error %q should point at --allow-locale", err.Error())
	}

	rc2, _ := newRC(t)
	r, err := validatecmd.Run(rc2, validatecmd.Input{Dir: dir, AllowLocale: []string{"pt-XX"}})
	if err != nil {
		t.Fatalf("Run with --allow-locale pt-XX: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable after whitelisting")
	}
}

func TestRun_allowLocaleRepeatable(t *testing.T) {
	dir := writeTree(t, listing.Tree{
		"pt-XX": completeListing("pt-XX"),
		"qq-ZZ": completeListing("qq-ZZ"),
	})

	rc, _ := newRC(t)
	r, err := validatecmd.Run(rc, validatecmd.Input{
		Dir:         dir,
		AllowLocale: []string{"pt-XX", "qq-ZZ"},
	})
	if err != nil {
		t.Fatalf("Run with two --allow-locale values: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable with two whitelisted locales")
	}
}

func TestRun_missingDir_exit20(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	rc, _ := newRC(t)
	_, err := validatecmd.Run(rc, validatecmd.Input{Dir: missing})
	if code := exitCodeOf(t, err); code != 20 {
		t.Errorf("ExitCode() = %d, want 20", code)
	}
	if !strings.Contains(err.Error(), "--dir") {
		t.Errorf("error %q should hint at --dir", err.Error())
	}
}

func TestRun_defaultDir(t *testing.T) {
	rc, _ := newRC(t)
	_, err := validatecmd.Run(rc, validatecmd.Input{})
	if err == nil {
		t.Skip("a ./metadata directory exists in the test cwd; default-dir path not exercised")
	}
	if !strings.Contains(err.Error(), validatecmd.DefaultDir) {
		t.Errorf("error %q should reference the default dir %q", err.Error(), validatecmd.DefaultDir)
	}
}

func TestNewCommand_offlineFlags(t *testing.T) {
	cmd := validatecmd.NewCommand(kernel.Boot{})
	if cmd.Use != "validate" {
		t.Errorf("cmd.Use = %q, want %q", cmd.Use, "validate")
	}
	for _, name := range []string{"dir", "allow-locale", "output"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("command missing --%s flag", name)
		}
	}
	if f := cmd.Flags().Lookup("allow-locale"); f != nil && f.Value.Type() != "stringArray" {
		t.Errorf("--allow-locale type = %q, want stringArray (repeatable)", f.Value.Type())
	}
}
