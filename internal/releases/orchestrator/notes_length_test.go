package orchestrator_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestUpload_invalidNoteContent_failsBeforeAnyHTTP is #786 on the upload path.
// Play refuses an empty note and one over 500 characters, but used to say so
// at tracks.update, after the bundle was uploaded. The refusal now comes before
// any request, in --dry-run too, with exit 20 and the locale and length named.
func TestUpload_invalidNoteContent_failsBeforeAnyHTTP(t *testing.T) {
	longDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(longDir, "fr-FR.txt"), []byte(strings.Repeat("é", 501)), 0o644); err != nil {
		t.Fatal(err)
	}
	emptyDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(emptyDir, "en-US.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		opts orchestrator.Opts
		want []string
	}{
		{"text over-long", orchestrator.Opts{ReleaseNotes: strings.Repeat("x", 600)}, []string{"the default language", "--release-notes", "600 characters"}},
		{"text blank", orchestrator.Opts{ReleaseNotes: "   "}, []string{"the default language", "empty (0 characters)"}},
		{"dir over-long in runes", orchestrator.Opts{ReleaseNotesDir: longDir}, []string{"fr-FR", "fr-FR.txt", "501 characters"}},
		{"dir empty file", orchestrator.Opts{ReleaseNotesDir: emptyDir}, []string{"en-US", "en-US.txt", "empty (0 characters)"}},
	}
	for _, tc := range cases {
		for _, dryRun := range []bool{false, true} {
			name := tc.name
			if dryRun {
				name += " dry-run"
			}
			t.Run(name, func(t *testing.T) {
				rt := testkit.NewFake()
				opts := tc.opts
				opts.Package = "com.example.app"
				opts.Track = "internal"
				opts.Artifacts = []orchestrator.Artifact{{Path: writeFakeAAB(t)}}
				opts.DryRun = dryRun

				_, err := orchestrator.Upload(context.Background(), &http.Client{Transport: rt}, opts)
				if err == nil {
					t.Fatal("Upload: got nil, want the release-notes refusal")
				}
				if got := exit.For(err); got != 20 {
					t.Errorf("exit = %d, want 20 (client-side validation): %v", got, err)
				}
				for _, want := range tc.want {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not contain %q", err, want)
					}
				}
				if n := requests(rt); n != 0 {
					t.Errorf("%d HTTP request(s) issued, want 0: %v", n, apiCalls(rt))
				}
			})
		}
	}
}

// TestPromote_invalidNoteContent_failsBeforeAnyHTTP is #786 on the promote
// path: a --release-notes override Play would refuse is refused before
// edits.insert, and --dry-run says so instead of exiting 0.
func TestPromote_invalidNoteContent_failsBeforeAnyHTTP(t *testing.T) {
	longDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(longDir, "fr-FR.txt"), []byte(strings.Repeat("é", 501)), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		opts orchestrator.PromoteOpts
		want []string
	}{
		{"text over-long in runes", orchestrator.PromoteOpts{ReleaseNotes: strings.Repeat("é", 501)}, []string{"the default language", "--release-notes", "501 characters"}},
		{"text blank", orchestrator.PromoteOpts{ReleaseNotes: " \n"}, []string{"empty (0 characters)"}},
		{"dir over-long in runes", orchestrator.PromoteOpts{ReleaseNotesDir: longDir}, []string{"fr-FR", "fr-FR.txt", "501 characters"}},
	}
	for _, tc := range cases {
		for _, dryRun := range []bool{false, true} {
			name := tc.name
			if dryRun {
				name += " dry-run"
			}
			t.Run(name, func(t *testing.T) {
				rt := testkit.NewFake()
				opts := tc.opts
				opts.Package = "com.example.app"
				opts.FromTrack = "internal"
				opts.ToTrack = "beta"
				opts.DryRun = dryRun

				_, err := orchestrator.Promote(context.Background(), &http.Client{Transport: rt}, opts)
				if got := exit.For(err); got != 20 {
					t.Fatalf("exit = %d, want 20 (client-side validation): %v", got, err)
				}
				for _, want := range tc.want {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not contain %q", err, want)
					}
				}
				if n := requests(rt); n != 0 {
					t.Errorf("%d HTTP request(s) issued, want 0: %v", n, apiCalls(rt))
				}
			})
		}
	}
}

// TestUpload_dryRun_unusedEmptyDefaultTxt_accepted: a default.txt that a
// `<locale>.txt` may cover is not refused offline, where the default language
// is unknown. With en-US.txt present and en-US the real default, the live load
// never reads default.txt, so refusing it would block a legitimate upload.
func TestUpload_dryRun_unusedEmptyDefaultTxt_accepted(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "en-US.txt"), []byte("Bug fixes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "default.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	rt := testkit.NewFake()
	res, err := orchestrator.Upload(context.Background(), &http.Client{Transport: rt}, orchestrator.Opts{
		Package: "com.example.app", Track: "internal", DryRun: true,
		Artifacts:       []orchestrator.Artifact{{Path: writeFakeAAB(t)}},
		ReleaseNotesDir: dir,
	})
	if err != nil {
		t.Fatalf("Upload dry-run: %v (exit %d), want accepted", err, exit.For(err))
	}
	if strings.Join(res.Locales, ",") != "en-US" {
		t.Errorf("previewed locales = %v, want [en-US]", res.Locales)
	}
	if n := requests(rt); n != 0 {
		t.Errorf("%d HTTP request(s) issued, want 0: %v", n, apiCalls(rt))
	}
}

// TestUpload_usedEmptyDefaultTxt_refusedBeforeBundleUpload is the other half
// of the offline rule: once details.get names a default language no
// `<locale>.txt` covers, the empty default.txt ships, so the load inside the
// Edit refuses it with exit 20 before any bundle byte is sent, and the
// implicit Edit is discarded.
func TestUpload_usedEmptyDefaultTxt_refusedBeforeBundleUpload(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "en-US.txt"), []byte("Bug fixes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "default.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	rt, transport := newPlay(playAPI{editID: "e1", versionCode: 7, defaultLanguage: "fr-FR"})
	_, err := orchestrator.Upload(context.Background(), &http.Client{Transport: transport}, orchestrator.Opts{
		Package: "com.example.app", Track: "internal",
		Artifacts:       []orchestrator.Artifact{{Path: writeFakeAAB(t)}},
		ReleaseNotesDir: dir,
	})
	if got := exit.For(err); got != 20 {
		t.Fatalf("exit = %d, want 20: %v", got, err)
	}
	if !strings.Contains(err.Error(), "fr-FR") || !strings.Contains(err.Error(), "default.txt") {
		t.Errorf("error %q does not name fr-FR and default.txt", err)
	}
	for _, c := range apiCalls(rt) {
		if strings.Contains(c, "/bundles") || strings.Contains(c, "/tracks/") {
			t.Errorf("call %s issued after a refused note, want none", c)
		}
	}
}
