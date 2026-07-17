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

// TestResumable_probeTransientFailure_retriesUnderStallBound: a 5xx to the
// offset probe itself is transient — the helper retries the probe under the
// stall bound instead of aborting the upload on the first failure.
func TestResumable_probeTransientFailure_retriesUnderStallBound(t *testing.T) {
	size := api.ResumableChunkSize + 300
	rt := &resumeRT{t: t, putSteps: []step{
		{status: 500},                            // chunk 1 PUT: upstream hiccup
		{status: 503},                            // probe #1: also hiccups
		{status: 0},                              // probe #2: connection dropped
		{status: 308, rng: "bytes=0-8388607"},    // probe #3: server has chunk 1
		{status: 200, body: `{"versionCode":5}`}, // final chunk, resumed
	}}
	body, status, err := run(t, rt, size)
	if err != nil {
		t.Fatalf("ResumableUpload: %v", err)
	}
	if status != 200 || string(body) != `{"versionCode":5}` {
		t.Fatalf("got status=%d body=%q", status, body)
	}
	if len(rt.puts) != 5 {
		t.Fatalf("PUT count = %d, want 5 (chunk, probe x3, chunk)", len(rt.puts))
	}
}

// TestResumable_probeTerminal4xx_isImmediate: a 4xx to the probe is terminal —
// no retry, the error surfaces with its upstream status.
func TestResumable_probeTerminal4xx_isImmediate(t *testing.T) {
	size := api.ResumableChunkSize + 300
	rt := &resumeRT{t: t, putSteps: []step{
		{status: 500}, // chunk 1 PUT: upstream hiccup
		{status: 404, body: `{"error":{"message":"session gone"}}`}, // probe: terminal
	}}
	_, _, err := run(t, rt, size)
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 {
		t.Fatalf("err = %v, want *api.Error with StatusCode 404", err)
	}
	if len(rt.puts) != 2 {
		t.Fatalf("PUT count = %d, want 2 (no probe retry after 4xx)", len(rt.puts))
	}
}

// TestResumable_ackedOffsetBeyondSize_isProtocolError: a 308 Range past the
// artifact's end is rejected as a protocol error instead of computing a
// negative chunk length.
func TestResumable_ackedOffsetBeyondSize_isProtocolError(t *testing.T) {
	size := api.ResumableChunkSize + 300
	rt := &resumeRT{t: t, putSteps: []step{
		{status: 308, rng: "bytes=0-999999999"}, // acked offset 10^9 > size
	}}
	_, _, err := run(t, rt, size)
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *api.Error", err)
	}
	if !strings.Contains(apiErr.Message, "outside artifact size") {
		t.Errorf("Message = %q, want an out-of-range offset protocol error", apiErr.Message)
	}
}

// shortReader reports a full size but returns fewer bytes than asked: the
// artifact shrank (or lied about its size) between stat and read.
type shortReader struct{ actual int }

func (s *shortReader) ReadAt(p []byte, off int64) (int, error) {
	n := s.actual - int(off)
	if n <= 0 {
		return 0, io.EOF
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p[:n], make([]byte, n))
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// TestResumable_shortRead_isLocalIOError: a short ReadAt (clean io.EOF
// included) must not upload the zero-filled tail of the buffer — it is a
// LocalIOError (exit 20).
func TestResumable_shortRead_isLocalIOError(t *testing.T) {
	rt := &resumeRT{t: t, putSteps: []step{}}
	hc := &http.Client{Transport: rt}
	_, _, err := api.ResumableUpload(context.Background(), hc, "bundles.upload", "com.example.app", initiateURL, "application/octet-stream", &shortReader{actual: 512}, 1024)
	var lioErr *api.LocalIOError
	if !errors.As(err, &lioErr) {
		t.Fatalf("err = %v, want *api.LocalIOError", err)
	}
	if got := exit.For(err); got != 20 {
		t.Errorf("exit.For = %d, want 20", got)
	}
	if len(rt.puts) != 0 {
		t.Errorf("PUTs issued = %d, want 0 (nothing uploaded on short read)", len(rt.puts))
	}
}
