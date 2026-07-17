package api_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	initiateURL = "https://androidpublisher.googleapis.com/upload/androidpublisher/v3/applications/com.example.app/edits/e1/bundles?uploadType=resumable"
	sessionURI  = "https://androidpublisher.googleapis.com/upload/session/abc123"
)

// putRecord captures what a chunk/probe PUT carried, so a test can assert the
// exact Content-Range sequence the helper produced.
type putRecord struct {
	contentRange string
	bodyLen      int
}

// resumeRT is a scripted RoundTripper: it answers the initiate POST from
// initResp and serves putSteps in order for each PUT to the session URI. A
// step with status 0 simulates a transport error.
type resumeRT struct {
	t *testing.T

	initStatus int
	initBody   string
	initNoLoc  bool // omit the Location header on the initiate response

	putSteps []step
	putIdx   int
	puts     []putRecord
}

// step is one scripted PUT outcome.
type step struct {
	status int    // 0 => transport error
	rng    string // Range header value for a 308
	body   string // response body for a 2xx (or error envelope)
}

func (r *resumeRT) RoundTrip(req *http.Request) (*http.Response, error) {
	switch req.Method {
	case http.MethodPost:
		if req.URL.String() != initiateURL {
			r.t.Fatalf("initiate POST to %s, want %s", req.URL, initiateURL)
		}
		if got := req.Header.Get("X-Upload-Content-Length"); got == "" {
			r.t.Errorf("initiate missing X-Upload-Content-Length header")
		}
		status := r.initStatus
		if status == 0 {
			status = http.StatusOK
		}
		h := http.Header{}
		if status >= 200 && status < 300 && !r.initNoLoc {
			h.Set("Location", sessionURI)
		}
		return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(r.initBody))}, nil

	case http.MethodPut:
		body, _ := io.ReadAll(req.Body)
		_ = req.Body.Close()
		r.puts = append(r.puts, putRecord{contentRange: req.Header.Get("Content-Range"), bodyLen: len(body)})
		if r.putIdx >= len(r.putSteps) {
			r.t.Fatalf("unexpected PUT #%d: only %d scripted", r.putIdx+1, len(r.putSteps))
		}
		st := r.putSteps[r.putIdx]
		r.putIdx++
		if st.status == 0 {
			return nil, &net0Error{}
		}
		h := http.Header{}
		if st.rng != "" {
			h.Set("Range", st.rng)
		}
		return &http.Response{StatusCode: st.status, Header: h, Body: io.NopCloser(strings.NewReader(st.body))}, nil

	default:
		r.t.Fatalf("unexpected method %s", req.Method)
		return nil, nil
	}
}

type net0Error struct{}

func (*net0Error) Error() string { return "simulated transport failure" }

func reader(n int) io.ReaderAt { return bytes.NewReader(make([]byte, n)) }

func run(t *testing.T, rt *resumeRT, size int) ([]byte, int, error) {
	t.Helper()
	hc := &http.Client{Transport: rt}
	return api.ResumableUpload(context.Background(), hc, "bundles.upload", "com.example.app", initiateURL, "application/octet-stream", reader(size), int64(size))
}

// TestResumable_happyPath_singleChunk: a sub-chunk-size artifact is one
// initiate + one PUT whose 2xx body is returned verbatim (ADR-0003).
func TestResumable_happyPath_singleChunk(t *testing.T) {
	rt := &resumeRT{t: t, putSteps: []step{{status: 200, body: `{"versionCode":42}`}}}
	body, status, err := run(t, rt, 1024)
	if err != nil {
		t.Fatalf("ResumableUpload: %v", err)
	}
	if status != 200 || string(body) != `{"versionCode":42}` {
		t.Fatalf("got status=%d body=%q", status, body)
	}
	if len(rt.puts) != 1 {
		t.Fatalf("PUT count = %d, want 1", len(rt.puts))
	}
	if rt.puts[0].contentRange != "bytes 0-1023/1024" {
		t.Errorf("Content-Range = %q, want bytes 0-1023/1024", rt.puts[0].contentRange)
	}
	if rt.puts[0].bodyLen != 1024 {
		t.Errorf("chunk body len = %d, want 1024", rt.puts[0].bodyLen)
	}
}

// TestResumable_multiChunk: an artifact spanning >2 chunks PUTs each 8 MiB
// slice in order, advancing on 308, finishing on the final 2xx.
func TestResumable_multiChunk(t *testing.T) {
	size := api.ResumableChunkSize*2 + 500
	rt := &resumeRT{t: t, putSteps: []step{
		{status: 308, rng: "bytes=0-8388607"},
		{status: 308, rng: "bytes=0-16777215"},
		{status: 201, body: `{"versionCode":7}`},
	}}
	body, status, err := run(t, rt, size)
	if err != nil {
		t.Fatalf("ResumableUpload: %v", err)
	}
	if status != 201 || string(body) != `{"versionCode":7}` {
		t.Fatalf("got status=%d body=%q", status, body)
	}
	wantRanges := []string{
		"bytes 0-8388607/16777716",
		"bytes 8388608-16777215/16777716",
		"bytes 16777216-16777715/16777716",
	}
	if len(rt.puts) != len(wantRanges) {
		t.Fatalf("PUT count = %d, want %d", len(rt.puts), len(wantRanges))
	}
	for i, w := range wantRanges {
		if rt.puts[i].contentRange != w {
			t.Errorf("PUT #%d Content-Range = %q, want %q", i+1, rt.puts[i].contentRange, w)
		}
	}
}

// TestResumable_5xxMidUpload_resumesFromAckedOffset: a 5xx on the first chunk
// triggers an offset probe; the server reports it committed chunk 1 anyway, so
// the helper resumes from that acknowledged offset (no re-send) and finishes.
func TestResumable_5xxMidUpload_resumesFromAckedOffset(t *testing.T) {
	size := api.ResumableChunkSize + 300
	rt := &resumeRT{t: t, putSteps: []step{
		{status: 500},                            // chunk 1 PUT: upstream hiccup
		{status: 308, rng: "bytes=0-8388607"},    // probe (bytes */size): server DID commit chunk 1
		{status: 200, body: `{"versionCode":9}`}, // chunk 2 (final), resumed from offset 8388608
	}}
	body, status, err := run(t, rt, size)
	if err != nil {
		t.Fatalf("ResumableUpload: %v", err)
	}
	if status != 200 || string(body) != `{"versionCode":9}` {
		t.Fatalf("got status=%d body=%q", status, body)
	}
	if len(rt.puts) != 3 {
		t.Fatalf("PUT count = %d, want 3 (chunk1, probe, chunk2)", len(rt.puts))
	}
	// The probe carries the "bytes */size" query form and no body.
	if rt.puts[1].contentRange != "bytes */8388908" || rt.puts[1].bodyLen != 0 {
		t.Errorf("probe = {range=%q len=%d}, want {bytes */8388908, 0}", rt.puts[1].contentRange, rt.puts[1].bodyLen)
	}
	// The resumed final chunk starts at the acknowledged offset, not 0.
	if rt.puts[2].contentRange != "bytes 8388608-8388907/8388908" {
		t.Errorf("resumed chunk Content-Range = %q, want bytes 8388608-8388907/8388908", rt.puts[2].contentRange)
	}
}

// TestResumable_transportErrorMidUpload_probeAndResume: a transport error (not
// an HTTP status) on a chunk PUT is recovered the same way — probe, then
// resume from the acknowledged offset.
func TestResumable_transportErrorMidUpload_probeAndResume(t *testing.T) {
	size := api.ResumableChunkSize + 100
	rt := &resumeRT{t: t, putSteps: []step{
		{status: 0},                           // chunk 1 PUT: connection dropped
		{status: 308, rng: "bytes=0-8388607"}, // probe: server has chunk 1
		{status: 200, body: `{"versionCode":3}`},
	}}
	_, _, err := run(t, rt, size)
	if err != nil {
		t.Fatalf("ResumableUpload: %v", err)
	}
	if len(rt.puts) != 3 {
		t.Fatalf("PUT count = %d, want 3", len(rt.puts))
	}
}

// TestResumable_initiateFailure: a non-2xx initiate is an *api.Error carrying
// the upstream status (403 → exit 11), before any PUT.
func TestResumable_initiateFailure(t *testing.T) {
	rt := &resumeRT{t: t, initStatus: 403, initBody: `{"error":{"message":"denied","errors":[{"reason":"forbidden"}]}}`}
	_, _, err := run(t, rt, 1024)
	if err == nil {
		t.Fatal("expected an error on a 403 initiate")
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %v is not *api.Error", err)
	}
	if apiErr.StatusCode != 403 {
		t.Errorf("StatusCode = %d, want 403", apiErr.StatusCode)
	}
	if got := exit.For(err); got != 11 {
		t.Errorf("exit.For = %d, want 11", got)
	}
	if len(rt.puts) != 0 {
		t.Errorf("PUTs issued = %d, want 0 (initiate failed first)", len(rt.puts))
	}
}

// TestResumable_initiateNoLocation: a 2xx initiate without a Location header is
// a protocol violation surfaced as an *api.Error.
func TestResumable_initiateNoLocation(t *testing.T) {
	rt := &resumeRT{t: t, initStatus: 200, initNoLoc: true}
	_, _, err := run(t, rt, 1024)
	if err == nil {
		t.Fatal("expected an error when initiate omits the session URI")
	}
	if len(rt.puts) != 0 {
		t.Errorf("PUTs issued = %d, want 0", len(rt.puts))
	}
}

// TestResumable_terminal4xxDuringChunks: a 4xx on a chunk PUT is terminal (no
// probe/resume). For bundles.upload, a 400 maps to exit 20 (malformed AAB).
func TestResumable_terminal4xxDuringChunks(t *testing.T) {
	rt := &resumeRT{t: t, putSteps: []step{
		{status: 400, body: `{"error":{"message":"malformed bundle"}}`},
	}}
	_, _, err := run(t, rt, 2048)
	if err == nil {
		t.Fatal("expected an error on a 400 chunk PUT")
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %v is not *api.Error", err)
	}
	if apiErr.StatusCode != 400 {
		t.Errorf("StatusCode = %d, want 400", apiErr.StatusCode)
	}
	if got := exit.For(err); got != 20 {
		t.Errorf("exit.For = %d, want 20 (bundles.upload 400 => malformed artifact)", got)
	}
	if len(rt.puts) != 1 {
		t.Errorf("PUTs issued = %d, want 1 (4xx is terminal, no resume)", len(rt.puts))
	}
}
