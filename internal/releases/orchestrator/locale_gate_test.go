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
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func requests(f *testkit.Fake) int { return len(f.Calls()) + f.TokenExchanges() }

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

func TestUpload_invalidNotesLocale_failsBeforeAnyHTTP(t *testing.T) {
	rt := testkit.NewFake()
	dir := notesDir(t, "en_US.txt", "pt_BR.txt", "fr-FR.txt")

	_, err := orchestrator.Upload(context.Background(), &http.Client{Transport: rt}, orchestrator.Opts{
		Package:         "com.example.app",
		Track:           "internal",
		Artifacts:       []orchestrator.Artifact{{Path: writeFakeAAB(t)}},
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
	if n := requests(rt); n != 0 {
		t.Errorf("%d HTTP request(s) issued, want 0: %v (the locale gate must reject before any Edit is opened)", n, apiCalls(rt))
	}
}

func TestPromote_invalidNotesLocale_failsBeforeAnyHTTP(t *testing.T) {
	rt := testkit.NewFake()
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
	if n := requests(rt); n != 0 {
		t.Errorf("%d HTTP request(s) issued, want 0: %v (the locale gate must reject before any Edit is opened)", n, apiCalls(rt))
	}
}

func TestUpload_missingNotesDir_failsBeforeAnyHTTP(t *testing.T) {
	rt := testkit.NewFake()
	missing := filepath.Join(t.TempDir(), "relase-notes")

	_, err := orchestrator.Upload(context.Background(), &http.Client{Transport: rt}, orchestrator.Opts{
		Package:         "com.example.app",
		Track:           "internal",
		Artifacts:       []orchestrator.Artifact{{Path: writeFakeAAB(t)}},
		ReleaseNotesDir: missing,
	})
	if err == nil {
		t.Fatal("Upload: got nil, want a rejection of the unreadable --release-notes-dir")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) || coder.ExitCode() != 2 {
		t.Errorf("err = %v (%T), want ExitCode() 2 (CLI misuse)", err, err)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error = %q, want it to name the directory %q", err, missing)
	}
	if n := requests(rt); n != 0 {
		t.Errorf("%d HTTP request(s) issued, want 0: %v (the locale gate must reject before any Edit is opened)", n, apiCalls(rt))
	}
}

func TestUpload_validNotesLocales_passTheGate(t *testing.T) {
	dir := notesDir(t, "en-US.txt", "zh-Hant-TW.txt", "es-419.txt", "default.txt")
	_, transport := newPlay(playAPI{editID: "edit-loc", versionCode: 7})

	if _, err := orchestrator.Upload(context.Background(), &http.Client{Transport: transport}, orchestrator.Opts{
		Package:         "com.example.app",
		Track:           "internal",
		Artifacts:       []orchestrator.Artifact{{Path: writeFakeAAB(t)}},
		ReleaseNotesDir: dir,
	}); err != nil {
		t.Fatalf("Upload with valid locales: %v, want nil", err)
	}
}
