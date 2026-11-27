package upload_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/upload"
	"github.com/PollyGlot/google-play-cli/internal/exit"
)

func notesDirWith(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("notes"), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", name, err)
	}
	return dir
}

func exactly(msg string) string { return "^" + regexp.QuoteMeta(msg) + "$" }

func TestRun_invalidInput_exit2_namesTheFlag_noHTTP(t *testing.T) {
	exclusive := exactly("--release-notes and --release-notes-dir are mutually exclusive")
	blankName := exactly("--release-name must not be empty or blank")
	cases := []struct {
		name string
		in   func(*upload.Input)
		want string
	}{
		{"notes text and dir", func(in *upload.Input) {
			in.ReleaseNotes, in.ReleaseNotesSet, in.ReleaseNotesDir = "hello", true, notesDirWith(t, "en-US.txt")
		}, exclusive},
		{"notes explicit empty and dir", func(in *upload.Input) {
			in.ReleaseNotes, in.ReleaseNotesSet, in.ReleaseNotesDir = "", true, notesDirWith(t, "en-US.txt")
		}, exclusive},
		{"notes blank and dir", func(in *upload.Input) {
			in.ReleaseNotes, in.ReleaseNotesSet, in.ReleaseNotesDir = "   ", true, notesDirWith(t, "en-US.txt")
		}, exclusive},
		{"notes too long and dir", func(in *upload.Input) {
			in.ReleaseNotes, in.ReleaseNotesSet, in.ReleaseNotesDir = strings.Repeat("x", 600), true, notesDirWith(t, "en-US.txt")
		}, exclusive},
		{"update priority 9", func(in *upload.Input) { in.UpdatePriority, in.UpdatePrioritySet = 9, true },
			exactly("--update-priority must be in 0..5, got 9")},
		{"update priority -1", func(in *upload.Input) { in.UpdatePriority, in.UpdatePrioritySet = -1, true },
			exactly("--update-priority must be in 0..5, got -1")},
		{"release name explicit empty", func(in *upload.Input) { in.ReleaseName, in.ReleaseNameSet = "", true }, blankName},
		{"release name blank", func(in *upload.Input) { in.ReleaseName, in.ReleaseNameSet = " \t", true }, blankName},
		{"notes dir locale", func(in *upload.Input) { in.ReleaseNotesDir = notesDirWith(t, "en_US.txt") },
			"^release-notes file name\\(s\\) are not a valid BCP-47 locale tag: .*en_US\\.txt: rename them"},
	}
	modes := []struct {
		name string
		in   func(*upload.Input)
	}{
		{"internal", func(in *upload.Input) {}},
		{"production without confirm", func(in *upload.Input) { in.Track, in.Complete = "production", true }},
		{"dry-run", func(in *upload.Input) { in.DryRun = true }},
	}
	for _, tc := range cases {
		for _, m := range modes {
			t.Run(tc.name+"/"+m.name, func(t *testing.T) {
				rt, transport := newUploadTransport(uploadAPI{editID: "edit-invalid", versionCode: 142})
				rc, _ := newRC(t, transport)
				in := upload.Input{Package: "com.example.app", Track: "internal", Artifacts: []string{writeFakeAAB(t)}}
				m.in(&in)
				tc.in(&in)

				_, err := upload.Run(rc, in)
				if got := exit.For(err); got != 2 {
					t.Fatalf("exit.For(err) = %d, want 2; err=%v", got, err)
				}
				if !regexp.MustCompile(tc.want).MatchString(err.Error()) {
					t.Errorf("error = %q, want it to match %s", err, tc.want)
				}
				if touched(rt) {
					t.Errorf("RoundTripper saw calls on a usage error: %v", apiCalls(rt))
				}
			})
		}
	}
}
