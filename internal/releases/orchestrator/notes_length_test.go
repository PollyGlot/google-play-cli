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
