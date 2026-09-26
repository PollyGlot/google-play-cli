package kernel_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// deadlineRecorder answers every request (the token exchange through
// testkit) and records, per "METHOD URL", the deadline the request carried.
type deadlineRecorder struct {
	mu     sync.Mutex
	budget map[string]time.Duration // 0 = no deadline
}

func (d *deadlineRecorder) serve(req *http.Request) (*http.Response, error) {
	var left time.Duration
	if dl, ok := req.Context().Deadline(); ok {
		left = time.Until(dl)
	}
	d.mu.Lock()
	d.budget[req.Method+" "+req.URL.String()] = left
	d.mu.Unlock()
	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
	}
	if resp, ok := testkit.TokenResponse(req); ok {
		return resp, nil
	}
	return testkit.Response(http.StatusOK, `{}`), nil
}

// TestUploadClient_boundsControlPlaneButNotMedia: an upload command drives one
// client through its Edit calls and its artifact transfer. The Edit calls,
// the resumable initiate and probe, and the token exchange keep the 60s
// control-plane bound (DESIGN §8); only the bytes of a media transfer run
// unbounded. With --retry the bound holds per attempt the same way.
func TestUploadClient_boundsControlPlaneButNotMedia(t *testing.T) {
	const (
		base   = "https://androidpublisher.googleapis.com"
		insert = base + "/androidpublisher/v3/applications/com.example.app/edits"
		commit = insert + "/e1:commit"
		upload = base + "/upload/androidpublisher/v3/applications/com.example.app/edits/e1/bundles?uploadType=resumable"
		chunk  = base + "/upload/androidpublisher/v3/applications/com.example.app/edits/e1/bundles?uploadType=resumable&upload_id=x"
		dl     = base + "/androidpublisher/v3/applications/com.example.app/generatedApks/1/downloads/d:download?alt=media"
	)
	requests := []struct {
		method, url string
		body        []byte
		bounded     bool
	}{
		{http.MethodPost, insert, nil, true},
		{http.MethodPost, upload, nil, true},                          // resumable initiate
		{http.MethodPut, chunk, bytes.Repeat([]byte("x"), 64), false}, // a chunk of media
		{http.MethodPut, chunk, nil, true},                            // offset probe
		{http.MethodGet, dl, nil, false},                              // generated APK bytes
		{http.MethodPost, commit, nil, true},
	}
	for _, retry := range []int{0, 2} {
		rec := &deadlineRecorder{budget: map[string]time.Duration{}}
		ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: testkit.RoundTripFunc(rec.serve)})
		rc := kernel.NewForTest(ctx, newBoot(t), kernel.Inputs{Retry: retry})
		rc.Account = signedAccount(t)
		hc, err := rc.UploadClient()
		if err != nil {
			t.Fatalf("UploadClient: %v", err)
		}
		for _, r := range requests {
			var body io.Reader
			if r.body != nil {
				body = bytes.NewReader(r.body)
			}
			req, err := http.NewRequestWithContext(context.Background(), r.method, r.url, body)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := hc.Do(req)
			if err != nil {
				t.Fatalf("retry=%d %s %s: %v", retry, r.method, r.url, err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()

			left := rec.budget[r.method+" "+r.url]
			switch {
			case r.bounded && (left <= 50*time.Second || left > 60*time.Second):
				t.Errorf("retry=%d %s %s: deadline %v, want the 60s control-plane bound", retry, r.method, r.url, left)
			case !r.bounded && left != 0:
				t.Errorf("retry=%d %s %s: deadline %v, want none (media transfer)", retry, r.method, r.url, left)
			}
		}
		if left := rec.budget[http.MethodPost+" "+testkit.TokenURL]; left <= 50*time.Second || left > 60*time.Second {
			t.Errorf("retry=%d token exchange deadline %v, want the 60s bound", retry, left)
		}
	}
}

// TestUploadClient_explicitTimeoutBoundsMedia: --timeout bounds every request,
// the media transfer included.
func TestUploadClient_explicitTimeoutBoundsMedia(t *testing.T) {
	rec := &deadlineRecorder{budget: map[string]time.Duration{}}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: testkit.RoundTripFunc(rec.serve)})
	rc := kernel.NewForTest(ctx, newBoot(t), kernel.Inputs{Timeout: 7 * time.Second})
	rc.Account = signedAccount(t)
	hc, err := rc.UploadClient()
	if err != nil {
		t.Fatalf("UploadClient: %v", err)
	}
	const chunk = "https://androidpublisher.googleapis.com/upload/androidpublisher/v3/x?upload_id=y"
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPut, chunk, bytes.NewReader([]byte("media")))
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if left := rec.budget[http.MethodPut+" "+chunk]; left <= 0 || left > 7*time.Second {
		t.Errorf("media deadline under --timeout 7s = %v, want within (0, 7s]", left)
	}
}
