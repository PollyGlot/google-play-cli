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

const emptyTextNote = "release notes for the default language (--release-notes) are empty (0 characters): write them, or drop this locale"

func TestUpload_explicitEmptyNotes_exit20_beforeConfirmAndHTTP(t *testing.T) {
	modes := []struct {
		name string
		opts func(*orchestrator.Opts)
	}{
		{"internal", func(o *orchestrator.Opts) {}},
		{"production without confirm", func(o *orchestrator.Opts) { o.Track, o.State = "production", orchestrator.Completed() }},
		{"dry-run", func(o *orchestrator.Opts) { o.DryRun = true }},
	}
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			rt := testkit.NewFake()
			opts := orchestrator.Opts{
				Package: "com.example.app", Track: "internal",
				Artifacts:       []orchestrator.Artifact{{Path: writeFakeAAB(t)}},
				ReleaseNotesSet: true,
			}
			m.opts(&opts)

			_, err := orchestrator.Upload(context.Background(), &http.Client{Transport: rt}, opts)
			if got := exit.For(err); got != 20 || err.Error() != emptyTextNote {
				t.Errorf("Upload = %v (exit %d), want %q exit 20", err, got, emptyTextNote)
			}
			if n := requests(rt); n != 0 {
				t.Errorf("%d HTTP request(s) issued, want 0: %v", n, apiCalls(rt))
			}
		})
	}
}

func TestPromote_explicitEmptyNotes_exit20_beforeConfirmAndHTTP(t *testing.T) {
	modes := []struct {
		name string
		opts func(*orchestrator.PromoteOpts)
	}{
		{"to beta", func(o *orchestrator.PromoteOpts) {}},
		{"production without confirm", func(o *orchestrator.PromoteOpts) { o.ToTrack, o.State = "production", orchestrator.Completed() }},
		{"dry-run", func(o *orchestrator.PromoteOpts) { o.DryRun = true }},
		{"plan", func(o *orchestrator.PromoteOpts) { o.Plan = true }},
	}
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			rt := testkit.NewFake()
			opts := orchestrator.PromoteOpts{Package: "com.example.app", FromTrack: "internal", ToTrack: "beta", ReleaseNotesSet: true}
			m.opts(&opts)

			_, err := orchestrator.Promote(context.Background(), &http.Client{Transport: rt}, opts)
			if got := exit.For(err); got != 20 || err.Error() != emptyTextNote {
				t.Errorf("Promote = %v (exit %d), want %q exit 20", err, got, emptyTextNote)
			}
			if n := requests(rt); n != 0 {
				t.Errorf("%d HTTP request(s) issued, want 0: %v", n, apiCalls(rt))
			}
		})
	}
}

func TestUpload_dryRun_onlyDefaultTxt_previewsDefaultLanguage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "default.txt"), []byte("Bug fixes\n"), 0o644); err != nil {
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
	if strings.Join(res.Locales, ",") != "(default-language)" {
		t.Errorf("previewed locales = %v, want [(default-language)]", res.Locales)
	}
	if n := requests(rt); n != 0 {
		t.Errorf("%d HTTP request(s) issued, want 0: %v", n, apiCalls(rt))
	}
}

func TestUpload_unreadableDefaultTxt_readsTheDefaultLanguage(t *testing.T) {
	for _, tc := range []struct {
		lang    string
		refused bool
	}{{"fr-FR", true}, {"en-US", false}} {
		t.Run(tc.lang, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "en-US.txt"), []byte("Bug fixes\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, "default.txt")); err != nil {
				t.Fatal(err)
			}
			rt, transport := newPlay(playAPI{editID: "e1", versionCode: 7, defaultLanguage: tc.lang})
			_, err := orchestrator.Upload(context.Background(), &http.Client{Transport: transport}, orchestrator.Opts{
				Package: "com.example.app", Track: "internal",
				Artifacts:       []orchestrator.Artifact{{Path: writeFakeAAB(t)}},
				ReleaseNotesDir: dir,
			})
			calls := strings.Join(apiCalls(rt), "\n")
			if !strings.Contains(calls, "/details") {
				t.Errorf("no details.get issued, want the default language read before deciding on default.txt: %s", calls)
			}
			if !tc.refused {
				if err != nil {
					t.Errorf("Upload = %v, want accepted: en-US.txt covers the default language, default.txt is unused", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "default.txt") {
				t.Fatalf("Upload = %v, want default.txt refused: it would ship as fr-FR and cannot be read", err)

			}
			if strings.Contains(calls, "/bundles") || strings.Contains(calls, "/tracks/") {
				t.Errorf("upload or track update issued after a refused note: %s", calls)
			}
		})
	}
}
