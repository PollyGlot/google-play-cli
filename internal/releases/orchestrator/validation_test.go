package orchestrator_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func priority(p int) *int { return &p }

func TestUpload_invalidInput_exit2_beforeAnyHTTP(t *testing.T) {
	cases := []struct {
		name string
		opts func(*orchestrator.Opts)
	}{
		{"notes text and dir", func(o *orchestrator.Opts) { o.ReleaseNotes, o.ReleaseNotesDir = "hello", notesDir(t, "en-US.txt") }},
		{"update priority above range", func(o *orchestrator.Opts) { o.UpdatePriority = priority(6) }},
		{"update priority below range", func(o *orchestrator.Opts) { o.UpdatePriority = priority(-1) }},
		{"blank release name", func(o *orchestrator.Opts) { o.ReleaseName = "   " }},
		{"notes dir locale", func(o *orchestrator.Opts) { o.ReleaseNotesDir = notesDir(t, "en_US.txt") }},
	}
	modes := []struct {
		name string
		opts func(*orchestrator.Opts)
	}{
		{"internal", func(o *orchestrator.Opts) {}},
		{"production without confirm", func(o *orchestrator.Opts) { o.Track, o.State = "production", orchestrator.Completed() }},
		{"dry-run", func(o *orchestrator.Opts) { o.DryRun = true }},
	}
	for _, tc := range cases {
		for _, m := range modes {
			t.Run(tc.name+"/"+m.name, func(t *testing.T) {
				rt := testkit.NewFake()
				opts := orchestrator.Opts{
					Package:   "com.example.app",
					Track:     "internal",
					Artifacts: []orchestrator.Artifact{{Path: writeFakeAAB(t)}},
				}
				m.opts(&opts)
				tc.opts(&opts)

				_, err := orchestrator.Upload(context.Background(), &http.Client{Transport: rt}, opts)
				if got := exit.For(err); got != 2 {
					t.Errorf("exit.For(err) = %d, want 2; err=%v", got, err)
				}
				if n := requests(rt); n != 0 {
					t.Errorf("%d HTTP request(s) issued, want 0: %v", n, apiCalls(rt))
				}
			})
		}
	}
}

func TestPromote_invalidInput_exit2_beforeAnyHTTP(t *testing.T) {
	cases := []struct {
		name string
		opts func(*orchestrator.PromoteOpts)
	}{
		{"notes text and dir", func(o *orchestrator.PromoteOpts) {
			o.ReleaseNotes, o.ReleaseNotesDir = "hello", notesDir(t, "en-US.txt")
		}},
		{"update priority above range", func(o *orchestrator.PromoteOpts) { o.UpdatePriority = priority(6) }},
		{"update priority below range", func(o *orchestrator.PromoteOpts) { o.UpdatePriority = priority(-1) }},
		{"notes dir locale", func(o *orchestrator.PromoteOpts) { o.ReleaseNotesDir = notesDir(t, "en_US.txt") }},
	}
	modes := []struct {
		name string
		opts func(*orchestrator.PromoteOpts)
	}{
		{"to alpha", func(o *orchestrator.PromoteOpts) {}},
		{"production without confirm", func(o *orchestrator.PromoteOpts) { o.ToTrack, o.State = "production", orchestrator.Completed() }},
		{"dry-run", func(o *orchestrator.PromoteOpts) { o.DryRun = true }},
		{"plan", func(o *orchestrator.PromoteOpts) { o.Plan = true }},
	}
	for _, tc := range cases {
		for _, m := range modes {
			t.Run(tc.name+"/"+m.name, func(t *testing.T) {
				rt := testkit.NewFake()
				opts := orchestrator.PromoteOpts{Package: "com.example.app", FromTrack: "beta", ToTrack: "alpha"}
				m.opts(&opts)
				tc.opts(&opts)

				_, err := orchestrator.Promote(context.Background(), &http.Client{Transport: rt}, opts)
				if got := exit.For(err); got != 2 {
					t.Errorf("exit.For(err) = %d, want 2; err=%v", got, err)
				}
				if n := requests(rt); n != 0 {
					t.Errorf("%d HTTP request(s) issued, want 0: %v", n, apiCalls(rt))
				}
			})
		}
	}
}
