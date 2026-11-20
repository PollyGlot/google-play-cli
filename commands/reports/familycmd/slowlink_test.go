package familycmd

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/reports"
	"github.com/PollyGlot/google-play-cli/internal/reports/reportstest"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const slowBound = 100 * time.Millisecond

func paced(t *testing.T, b reportstest.Bucket, pick func(*http.Request) bool, body func(*http.Request, []byte) io.ReadCloser) http.RoundTripper {
	t.Helper()
	fake := testkit.NewFake(b.Responder())
	return testkit.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		resp, err := fake.RoundTrip(req)
		if err != nil || resp.StatusCode != http.StatusOK || !pick(req) {
			return resp, err
		}
		raw, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("read the fake's body: %v", err)
		}
		resp.Body = body(req, raw)
		return resp, nil
	})
}

func isFetch(req *http.Request) bool { return req.URL.Query().Get("alt") == "media" }

func isListing(req *http.Request) bool {
	return req.URL.Host == "storage.googleapis.com" && !isFetch(req)
}

func trickle(req *http.Request, raw []byte) io.ReadCloser {
	return testkit.TrickleBody(req.Context(), raw, len(raw)/15+1, 20*time.Millisecond)
}

func runPaced(t *testing.T, rt http.RoundTripper) (time.Duration, error) {
	t.Helper()
	rc, _, _ := reportstest.NewRC(t, rt)
	rc.SetControlPlaneDefaultForTest(slowBound)
	start := time.Now()
	_, err := Run(rc, reports.Installs, Input{Package: pkg, Month: "2026-09", Dimension: "country"})
	return time.Since(start), err
}

func TestRun_slowExportOutlivesTheControlPlaneBound(t *testing.T) {
	took, err := runPaced(t, paced(t, installsBucket(), isFetch, trickle))
	if err != nil {
		t.Fatalf("slow but progressing export failed after %v: %v", took, err)
	}
	if took < 2*slowBound {
		t.Fatalf("run took %v, under 2x the bound: the test proves nothing", took)
	}
}

func TestRun_stalledExportFailsAsNetwork(t *testing.T) {
	stall := func(req *http.Request, raw []byte) io.ReadCloser {
		return testkit.StalledBody(req.Context(), raw[:len(raw)/2])
	}
	took, err := runPaced(t, paced(t, installsBucket(), isFetch, stall))
	if err == nil {
		t.Fatal("a stalled export succeeded")
	}
	if code := exitCodeOf(t, err); code != 50 {
		t.Errorf("exit = %d, want 50 (network): %v", code, err)
	}
	if !strings.Contains(err.Error(), "stalled") {
		t.Errorf("err = %v, want it to name the stall", err)
	}
	if took > 5*time.Second {
		t.Errorf("stalled export returned after %v", took)
	}
}

func TestRun_slowListingKeepsTheControlPlaneBound(t *testing.T) {
	_, err := runPaced(t, paced(t, installsBucket(), isListing, trickle))
	if err == nil {
		t.Fatal("a listing slower than the control-plane bound succeeded")
	}
	if code := exitCodeOf(t, err); code != 50 {
		t.Errorf("exit = %d, want 50 (network): %v", code, err)
	}
}
