package history

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const slowBound = 100 * time.Millisecond

func pacedFetch(rt *historyRT, body func(*http.Request, []byte) io.ReadCloser) http.RoundTripper {
	return testkit.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		resp, err := rt.serve(req)
		if err != nil || req.URL.Query().Get("alt") != "media" {
			return resp, err
		}
		_ = resp.Body.Close()
		resp.Body = body(req, rt.getBody)
		return resp, nil
	})
}

func runPacedMonth(t *testing.T, body func(*http.Request, []byte) io.ReadCloser) (Payload, time.Duration, error) {
	t.Helper()
	rc, _, _, _ := newRC(t, pacedFetch(&historyRT{getBody: utf16LE(reportCSV)}, body))
	rc.SetControlPlaneDefaultForTest(slowBound)
	start := time.Now()
	r, err := Run(rc, Input{Package: "com.example.app", Month: "2026-06"})
	p, _ := r.(Payload)
	return p, time.Since(start), err
}

func TestRun_slowMonthOutlivesTheControlPlaneBound(t *testing.T) {
	p, took, err := runPacedMonth(t, func(req *http.Request, raw []byte) io.ReadCloser {
		return testkit.TrickleBody(req.Context(), raw, len(raw)/15+1, 20*time.Millisecond)
	})
	if err != nil {
		t.Fatalf("slow but progressing month failed after %v: %v", took, err)
	}
	if len(p.Rows) != 2 {
		t.Fatalf("want the 2 rows of the month, got %d", len(p.Rows))
	}
	if took < 2*slowBound {
		t.Fatalf("run took %v, under 2x the bound: the test proves nothing", took)
	}
}

func TestRun_stalledMonthFailsAsNetwork(t *testing.T) {
	_, took, err := runPacedMonth(t, func(req *http.Request, raw []byte) io.ReadCloser {
		return testkit.StalledBody(req.Context(), raw[:len(raw)/2])
	})
	if code := exitCodeOf(t, err); code != 50 {
		t.Errorf("exit = %d, want 50 (network): %v", code, err)
	}
	if !strings.Contains(err.Error(), "stalled") {
		t.Errorf("err = %v, want it to name the stall", err)
	}
	if took > 5*time.Second {
		t.Errorf("stalled month returned after %v", took)
	}
}
