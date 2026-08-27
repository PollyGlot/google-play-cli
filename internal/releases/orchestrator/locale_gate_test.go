package orchestrator_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// countingRT fails the test on ANY request. It is the whole point of the
// pre-Edit locale gate (PRD #446 / #452): a malformed `<locale>.txt` name is
// knowable from the filesystem alone, so discovering it must not cost an
// edits.insert, an artifact upload, or a burnt Edit.
type countingRT struct {
	t     *testing.T
	calls int
}

func (r *countingRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.calls++
	r.t.Errorf("unexpected HTTP request %s %s: the locale gate must reject before any Edit is opened", req.Method, req.URL.Path)
	return nil, errors.New("no network in tests")
}

// notesDir writes a release-notes directory with the given `<name>.txt` files.
func notesDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("notes"), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", n, err)
		}
	}
	return dir
}

// TestUpload_invalidNotesLocale_failsBeforeAnyHTTP is the acceptance criterion
// of #452 on the upload path: an `en_US.txt` typo is rejected with exit 2,
// every offending file is named at once, and the RoundTripper never sees a
// request.
func TestUpload_invalidNotesLocale_failsBeforeAnyHTTP(t *testing.T) {
	rt := &countingRT{t: t}
	dir := notesDir(t, "en_US.txt", "pt_BR.txt", "fr-FR.txt")

	_, err := orchestrator.Upload(context.Background(), &http.Client{Transport: rt}, orchestrator.Opts{
		Package:         "com.example.app",
		Track:           "internal",
		AABPath:         writeFakeAAB(t),
		ReleaseNotesDir: dir,
	})
	if err == nil {
		t.Fatal("Upload: got nil, want a locale-validation error")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) || coder.ExitCode() != 2 {
		t.Errorf("err = %v (%T), want ExitCode() 2 (CLI misuse)", err, err)
	}
	for _, want := range []string{"en_US.txt", "pt_BR.txt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to name %q (all offenders in one pass)", err, want)
		}
	}
	if rt.calls != 0 {
		t.Errorf("%d HTTP request(s) issued, want 0", rt.calls)
	}
}

// TestPromote_invalidNotesLocale_failsBeforeAnyHTTP repeats it on the promote
// path, so the gate is not an upload-only accident: promote overrides notes
// from the same kind of directory.
func TestPromote_invalidNotesLocale_failsBeforeAnyHTTP(t *testing.T) {
	rt := &countingRT{t: t}
	dir := notesDir(t, "en_US.txt")

	_, err := orchestrator.Promote(context.Background(), &http.Client{Transport: rt}, orchestrator.PromoteOpts{
		Package:         "com.example.app",
		FromTrack:       "internal",
		ToTrack:         "alpha",
		ReleaseNotesDir: dir,
	})
	if err == nil {
		t.Fatal("Promote: got nil, want a locale-validation error")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) || coder.ExitCode() != 2 {
		t.Errorf("err = %v (%T), want ExitCode() 2 (CLI misuse)", err, err)
	}
	if rt.calls != 0 {
		t.Errorf("%d HTTP request(s) issued, want 0", rt.calls)
	}
}

// TestUpload_validNotesLocales_passTheGate is the negative control: script and
// region variants, the numeric UN M.49 region, and the `default.txt` marker all
// go through untouched, so the gate cannot be "green because it rejects
// everything".
func TestUpload_validNotesLocales_passTheGate(t *testing.T) {
	dir := notesDir(t, "en-US.txt", "zh-Hant-TW.txt", "es-419.txt", "default.txt")
	rt := &playRT{t: t, editID: "edit-loc", versionCode: 7}

	if _, err := orchestrator.Upload(context.Background(), &http.Client{Transport: rt}, orchestrator.Opts{
		Package:         "com.example.app",
		Track:           "internal",
		AABPath:         writeFakeAAB(t),
		ReleaseNotesDir: dir,
	}); err != nil {
		t.Fatalf("Upload with valid locales: %v, want nil", err)
	}
}
