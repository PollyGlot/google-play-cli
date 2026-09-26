package reply

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// capturedJSON adapts the bytes runBatch wrote to stdout into a Renderable:
// the batch envelope is written inside Run, not returned, so driving Run is
// the only way to reach it.
type capturedJSON []byte

func (c capturedJSON) Renderers() output.Renderers {
	return output.Renderers{JSON: func(w io.Writer) error { _, err := w.Write(c); return err }}
}

// replyFake answers reviews.reply: 404 for gp:expired (a review past the
// 7-day window), 200 for any other id.
func replyFake() *testkit.Fake {
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		if c.Method != http.MethodPost || !strings.HasSuffix(c.Path, ":reply") {
			return 0, "", false
		}
		if strings.HasSuffix(c.Path, "/gp:expired:reply") {
			return http.StatusNotFound, `{"error":{"code":404,"message":"Review not found.","status":"NOT_FOUND"}}`, true
		}
		return http.StatusOK, `{"result":{"replyText":"Thanks!","lastEdited":{"seconds":"1785837600"}}}`, true
	})
}

// TestRenderJSON_batch_golden freezes the {"results":[...]} envelope over the
// three row outcomes: ok, an API error classified with its hint, and a
// malformed TSV line (no review id to report).
func TestRenderJSON_batch_golden(t *testing.T) {
	stdin := strings.NewReader("gp:ok-1\tThanks & see you <soon>!\ngp:expired\tSorry about that\nno-tab-here\n")
	rc, stdout, _ := newRC(t, replyFake(), output.FormatJSON, stdin)

	_ = Run(rc, Input{Package: "com.example.app", BatchSet: true, Batch: "-"}) // rows failed: exit code asserted elsewhere
	outputtest.GoldenJSON(t, "batch.json.golden", capturedJSON(stdout.Bytes()))
}

// TestRenderJSON_batchDryRun_golden freezes the rehearsal: every valid row
// "planned", no network, so an agent can preview a batch before posting it.
func TestRenderJSON_batchDryRun_golden(t *testing.T) {
	fake := replyFake()
	stdin := strings.NewReader("gp:ok-1\tThanks!\ngp:ok-2\tWe fixed it\n")
	rc, stdout, _ := newRC(t, fake, output.FormatJSON, stdin)

	if err := Run(rc, Input{Package: "com.example.app", BatchSet: true, Batch: "-", DryRun: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := len(fake.Calls()); n != 0 {
		t.Fatalf("--dry-run made %d API calls, want 0", n)
	}
	outputtest.GoldenJSON(t, "batch_dry_run.json.golden", capturedJSON(stdout.Bytes()))
}
