// Package validatecmd_test exercises `gplay metadata validate` end to end
// at the kernel level — but, crucially, OFFLINE. The RunContext is built
// with NO Account and NO injected transport: any attempt to authenticate
// or make an HTTP call would fail (kernel.AuthedClient errors without an
// Account, and there is no RoundTripper to serve a request). A passing
// suite is itself the proof that validate never touches the network.
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

// newRC builds an offline RunContext: no Account, no transport. If the
// command under test tried to authenticate or hit the network, it would
// fail here — which is exactly the offline guarantee we want to assert.
func newRC(t *testing.T) (*kernel.RunContext, *bytes.Buffer) {
	t.Helper()
	var stdout bytes.Buffer
	boot := kernel.Boot{Stdout: &stdout}
	rc := kernel.NewForTest(context.Background(), boot, kernel.Inputs{Format: output.FormatJSON})
	// rc.Account stays nil — no credentials of any kind.
	return rc, &stdout
}

// writeTree writes a metadata tree under a fresh temp dir and returns its
// path. Using tree.Write keeps the on-disk encoding (missing ≠ empty) in
// lockstep with what the codec reads back.
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

// TestRun_validTree_success is the tracer bullet: a known locale with a
// complete listing validates with no credentials and renders a Payload.
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

// TestRun_charLimit_exit20 asserts a title over 30 chars fails with exit
// 20 and an actionable message.
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

// TestRun_requiredEmpty_exit20 asserts an empty full_description file
// (managed but empty) fails — it is not a clear for a required field.
func TestRun_requiredEmpty_exit20(t *testing.T) {
	l := listing.NewListing("en-US")
	l.Set(listing.Title, "My App")
	l.Set(listing.FullDescription, "") // empty file on disk
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

// TestRun_requiredMissing_exit20 asserts a locale with no title.txt at all
// fails (title is required, present-and-non-empty, per locale).
func TestRun_requiredMissing_exit20(t *testing.T) {
	l := listing.NewListing("en-US")
	l.Set(listing.FullDescription, "fine") // no Title set -> file absent
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

// TestRun_unknownLocale_exit20_thenAllowLocaleWhitelists asserts an
// unknown locale fails by default, and that --allow-locale flips it to a
// pass without any other change.
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

	// Same tree, now whitelisted -> success.
	rc2, _ := newRC(t)
	r, err := validatecmd.Run(rc2, validatecmd.Input{Dir: dir, AllowLocale: []string{"pt-XX"}})
	if err != nil {
		t.Fatalf("Run with --allow-locale pt-XX: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable after whitelisting")
	}
}

// TestRun_allowLocaleRepeatable asserts two --allow-locale values both
// take effect (the StringArrayVar repeatable flag).
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

// TestRun_missingDir_exit20 asserts pointing --dir at a non-existent path
// is a client-side validation error (exit 20) with an actionable hint.
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

// TestRun_defaultDir asserts that an empty Input.Dir falls back to
// ./metadata (DefaultDir). We assert via the dirError message rather than
// creating a ./metadata dir in the test's working directory.
func TestRun_defaultDir(t *testing.T) {
	rc, _ := newRC(t)
	_, err := validatecmd.Run(rc, validatecmd.Input{}) // Dir unset
	if err == nil {
		t.Skip("a ./metadata directory exists in the test cwd; default-dir path not exercised")
	}
	if !strings.Contains(err.Error(), validatecmd.DefaultDir) {
		t.Errorf("error %q should reference the default dir %q", err.Error(), validatecmd.DefaultDir)
	}
}

// TestNewCommand_offlineFlags asserts the cobra command exposes --dir,
// --allow-locale (repeatable), and --output, and uses the validate verb.
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
	// --allow-locale must be a repeatable (stringArray) flag.
	if f := cmd.Flags().Lookup("allow-locale"); f != nil && f.Value.Type() != "stringArray" {
		t.Errorf("--allow-locale type = %q, want stringArray (repeatable)", f.Value.Type())
	}
}
