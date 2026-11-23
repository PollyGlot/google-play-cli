package api_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/testkit"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	initiateURL = "https://androidpublisher.googleapis.com/upload/androidpublisher/v3/applications/com.example.app/edits/e1/bundles?uploadType=resumable"
	sessionURI  = "https://androidpublisher.googleapis.com/upload/session/abc123"
)

type putRecord struct {
	contentRange string
	bodyLen      int
}

type resumeRT struct {
	t *testing.T

	initStatus int
	initBody   string
	initNoLoc  bool

	putSteps []step
	putIdx   int
	puts     []putRecord

	waits []time.Duration
	onPut func(i int)
}

type step struct {
	status  int
	rng     string
	body    string
	hang    bool
	cutBody bool
}

func (r *resumeRT) serve(req *http.Request) (*http.Response, error) {
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
		resp := testkit.Response(status, r.initBody)
		if status >= 200 && status < 300 && !r.initNoLoc {
			resp.Header.Set("Location", sessionURI)
		}
		return resp, nil

	case http.MethodPut:
		body := testkit.ReadBody(req)
		if req.Body != nil {
			_ = req.Body.Close()
		}
		r.puts = append(r.puts, putRecord{contentRange: req.Header.Get("Content-Range"), bodyLen: len(body)})
		if r.putIdx >= len(r.putSteps) {
			r.t.Fatalf("unexpected PUT #%d: only %d scripted", r.putIdx+1, len(r.putSteps))
		}
		st := r.putSteps[r.putIdx]
		if r.onPut != nil {
			r.onPut(r.putIdx)
		}
		r.putIdx++
		if st.hang {
			<-req.Context().Done()
			return nil, req.Context().Err()
		}
		if st.status == 0 {
			return nil, &net0Error{}
		}
		resp := testkit.Response(st.status, st.body)
		if st.rng != "" {
			resp.Header.Set("Range", st.rng)
		}
		if st.cutBody {
			resp.Body = io.NopCloser(io.MultiReader(strings.NewReader(st.body[:len(st.body)/2]), errReader{}))
		}
		return resp, nil

	default:
		r.t.Fatalf("unexpected method %s", req.Method)
		return nil, nil
	}
}

type net0Error struct{}

func (*net0Error) Error() string { return "simulated transport failure" }

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("simulated reset mid-body") }

func reader(n int) io.ReaderAt { return bytes.NewReader(make([]byte, n)) }

func run(t *testing.T, rt *resumeRT, size int) ([]byte, int, error) {
	t.Helper()
	return runCtx(t, context.Background(), rt, size, time.Minute)
}

func runCtx(t *testing.T, ctx context.Context, rt *resumeRT, size int, chunkTimeout time.Duration) ([]byte, int, error) {
	t.Helper()
	api.SetResumeTiming(t, chunkTimeout, &rt.waits)
	hc := &http.Client{Transport: testkit.RoundTripFunc(rt.serve)}
	return api.ResumableUpload(ctx, hc, "bundles.upload", "com.example.app", initiateURL, "application/octet-stream", reader(size), int64(size))
}

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

func TestResumable_5xxMidUpload_resumesFromAckedOffset(t *testing.T) {
	size := api.ResumableChunkSize + 300
	rt := &resumeRT{t: t, putSteps: []step{
		{status: 500},
		{status: 308, rng: "bytes=0-8388607"},
		{status: 200, body: `{"versionCode":9}`},
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
	if rt.puts[1].contentRange != "bytes */8388908" || rt.puts[1].bodyLen != 0 {
		t.Errorf("probe = {range=%q len=%d}, want {bytes */8388908, 0}", rt.puts[1].contentRange, rt.puts[1].bodyLen)
	}
	if rt.puts[2].contentRange != "bytes 8388608-8388907/8388908" {
		t.Errorf("resumed chunk Content-Range = %q, want bytes 8388608-8388907/8388908", rt.puts[2].contentRange)
	}
}

func TestResumable_transportErrorMidUpload_probeAndResume(t *testing.T) {
	size := api.ResumableChunkSize + 100
	rt := &resumeRT{t: t, putSteps: []step{
		{status: 0},
		{status: 308, rng: "bytes=0-8388607"},
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

func TestResumable_probeTransientFailure_retriesUnderStallBound(t *testing.T) {
	size := api.ResumableChunkSize + 300
	rt := &resumeRT{t: t, putSteps: []step{
		{status: 500},
		{status: 503},
		{status: 0},
		{status: 308, rng: "bytes=0-8388607"},
		{status: 200, body: `{"versionCode":5}`},
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

func TestResumable_probeTerminal4xx_isImmediate(t *testing.T) {
	size := api.ResumableChunkSize + 300
	rt := &resumeRT{t: t, putSteps: []step{
		{status: 500},
		{status: 404, body: `{"error":{"message":"session gone"}}`},
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

func TestResumable_ackedOffsetBeyondSize_isProtocolError(t *testing.T) {
	size := api.ResumableChunkSize + 300
	rt := &resumeRT{t: t, putSteps: []step{
		{status: 308, rng: "bytes=0-999999999"},
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

func TestResumable_shortRead_isLocalIOError(t *testing.T) {
	rt := &resumeRT{t: t, putSteps: []step{}}
	hc := &http.Client{Transport: testkit.RoundTripFunc(rt.serve)}
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

func TestResumable_308AcksEveryByte_finalizesWithProbe(t *testing.T) {
	rt := &resumeRT{t: t, putSteps: []step{
		{status: 308, rng: "bytes=0-2047"},
		{status: 200, body: `{"versionCode":11}`},
	}}
	body, status, err := run(t, rt, 2048)
	if err != nil {
		t.Fatalf("ResumableUpload: %v", err)
	}
	if status != 200 || string(body) != `{"versionCode":11}` {
		t.Fatalf("got status=%d body=%q, want the probe's resource", status, body)
	}
	if len(rt.puts) != 2 {
		t.Fatalf("PUT count = %d, want 2 (chunk, probe)", len(rt.puts))
	}
	if rt.puts[1].contentRange != "bytes */2048" || rt.puts[1].bodyLen != 0 {
		t.Errorf("second PUT = {range=%q len=%d}, want the probe {bytes */2048, 0}", rt.puts[1].contentRange, rt.puts[1].bodyLen)
	}
	assertNoEmptyChunk(t, rt.puts)
	if len(rt.waits) != 0 {
		t.Errorf("waits = %v, want none before the first probe after a full acknowledgement", rt.waits)
	}
}

func TestResumable_308AcksEveryByte_probeNeverFinalizes_stallBoundTerminates(t *testing.T) {
	rt := &resumeRT{t: t, putSteps: repeat(9, step{status: 308, rng: "bytes=0-2047"})}
	_, _, err := run(t, rt, 2048)
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Message, "stalled") {
		t.Fatalf("err = %v, want the stall error", err)
	}
	if len(rt.puts) != 9 {
		t.Fatalf("PUT count = %d, want 9 (chunk + 8 probes)", len(rt.puts))
	}
	if rt.puts[0].contentRange != "bytes 0-2047/2048" {
		t.Errorf("first PUT = %q, want the chunk bytes 0-2047/2048", rt.puts[0].contentRange)
	}
	for i, p := range rt.puts[1:] {
		if p.contentRange != "bytes */2048" || p.bodyLen != 0 {
			t.Errorf("PUT #%d = {range=%q len=%d}, want a probe after the acknowledgement", i+2, p.contentRange, p.bodyLen)
		}
	}
	assertNoEmptyChunk(t, rt.puts)
	assertBackoffCurve(t, rt.waits, 1, 2, 3, 4, 5, 6, 7)
}

func TestResumable_emptyArtifact_singlePutWithZeroRange(t *testing.T) {
	rt := &resumeRT{t: t, putSteps: []step{{status: 200, body: `{"versionCode":13}`}}}
	body, _, err := run(t, rt, 0)
	if err != nil {
		t.Fatalf("ResumableUpload: %v", err)
	}
	if string(body) != `{"versionCode":13}` {
		t.Fatalf("body = %q", body)
	}
	if len(rt.puts) != 1 || rt.puts[0].contentRange != "bytes */0" || rt.puts[0].bodyLen != 0 {
		t.Fatalf("PUTs = %+v, want exactly one {bytes */0, 0}", rt.puts)
	}
}

func assertNoEmptyChunk(t *testing.T, puts []putRecord) {
	t.Helper()
	for i, p := range puts {
		if p.bodyLen == 0 && !strings.HasPrefix(p.contentRange, "bytes */") {
			t.Errorf("PUT #%d sent an empty chunk with Content-Range %q", i+1, p.contentRange)
		}
	}
}
