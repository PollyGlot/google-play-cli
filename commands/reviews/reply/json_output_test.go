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

type capturedJSON []byte

func (c capturedJSON) Renderers() output.Renderers {
	return output.Renderers{JSON: func(w io.Writer) error { _, err := w.Write(c); return err }}
}

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

func TestRenderJSON_batch_golden(t *testing.T) {
	stdin := strings.NewReader("gp:ok-1\tThanks & see you <soon>!\ngp:expired\tSorry about that\nno-tab-here\n")
	rc, stdout, _ := newRC(t, replyFake(), output.FormatJSON, stdin)

	_ = Run(rc, Input{Package: "com.example.app", BatchSet: true, Batch: "-"})
	outputtest.GoldenJSON(t, "batch.json.golden", capturedJSON(stdout.Bytes()))
}

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
