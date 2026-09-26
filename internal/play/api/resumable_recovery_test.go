package api_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// The recovery branches of the resumable loop (#587, TEST-03): each test
// scripts the exact PUT/probe sequence and asserts the loop's two promises, a
// finished upload is never re-sent and a dead one always terminates, by
// counting the requests it made.

// repeat returns n copies of s.
func repeat(n int, s ...step) []step {
	var out []step
	for range n {
		out = append(out, s...)
	}
	return out
}

// assertBackoffCurve checks that the recorded waits follow the --retry curve
// for the given attempt indexes: attempt k waits within [b/2, b] of
// b = 500ms * 2^k (jitter), capped at 30s.
func assertBackoffCurve(t *testing.T, waits []time.Duration, attempts ...int) {
	t.Helper()
	if len(waits) != len(attempts) {
		t.Fatalf("waits = %v, want %d of them (attempts %v)", waits, len(attempts), attempts)
	}
	for i, k := range attempts {
		ceil := 500 * time.Millisecond
		for j := 0; j < k && ceil < 30*time.Second; j++ {
			ceil *= 2
		}
		ceil = min(ceil, 30*time.Second)
		if waits[i] < ceil/2 || waits[i] > ceil {
			t.Errorf("wait #%d = %v, want attempt-%d backoff in [%v, %v]", i, waits[i], k, ceil/2, ceil)
		}
	}
}

// A transport error on the LAST chunk whose bytes did land: the probe answers
// 2xx with the resource, which is returned as is, with no further PUT.
func TestResumable_lostFinalResponse_probeReturnsResourceOnce(t *testing.T) {
	size := 2048
	rt := &resumeRT{t: t, putSteps: []step{
		{status: 0}, // final chunk: bytes landed, response lost
		{status: 200, body: `{"versionCode":11}`}, // probe: already complete
	}}
	body, status, err := run(t, rt, size)
	if err != nil {
		t.Fatalf("ResumableUpload: %v", err)
	}
	if status != 200 || string(body) != `{"versionCode":11}` {
		t.Fatalf("got status=%d body=%q, want the probe's resource", status, body)
	}
	if len(rt.puts) != 2 {
		t.Fatalf("PUT count = %d, want 2 (chunk, probe): a finished upload must not be re-sent", len(rt.puts))
	}
	if rt.puts[1].contentRange != "bytes */2048" {
		t.Errorf("second PUT = %q, want the probe", rt.puts[1].contentRange)
	}
	assertBackoffCurve(t, rt.waits, 0)
}

// The final chunk's 2xx arrives but its body is cut mid-read: same recovery,
// the probe hands the resource back instead of a truncated document.
func TestResumable_finalBodyCut_probeReturnsResource(t *testing.T) {
	rt := &resumeRT{t: t, putSteps: []step{
		{status: 200, body: `{"versionCode":12}`, cutBody: true},
		{status: 200, body: `{"versionCode":12}`},
	}}
	body, _, err := run(t, rt, 1024)
	if err != nil {
		t.Fatalf("ResumableUpload: %v", err)
	}
	if string(body) != `{"versionCode":12}` {
		t.Fatalf("body = %q, want the whole resource from the probe", body)
	}
	if len(rt.puts) != 2 {
		t.Fatalf("PUT count = %d, want 2 (chunk, probe)", len(rt.puts))
	}
}

// Transport errors on every chunk AND every probe: the stall bound ends the
// loop after maxResumeStalls rounds (8 chunk PUTs + 8 probes), with a backoff
// before each probe that grows along the --retry curve. Exit 50 (network).
func TestResumable_transportErrorsEverywhere_stallBoundTerminates(t *testing.T) {
	rt := &resumeRT{t: t, putSteps: repeat(8, step{status: 0}, step{status: 0})}
	_, _, err := run(t, rt, 4096)
	if err == nil {
		t.Fatal("expected the stall bound to fail the upload")
	}
	if got := exit.For(err); got != 50 {
		t.Errorf("exit.For = %d, want 50; err=%v", got, err)
	}
	if len(rt.puts) != 16 {
		t.Fatalf("PUT count = %d, want 16 (8 chunks + 8 probes)", len(rt.puts))
	}
	assertBackoffCurve(t, rt.waits, 0, 1, 2, 3, 4, 5, 6, 7)
}

// Transport errors on the chunk, but the probe answers 308 with no progress:
// the stall error names the lack of progress and keeps the transport cause.
func TestResumable_transportErrorNoProgress_stallBoundTerminates(t *testing.T) {
	rt := &resumeRT{t: t, putSteps: repeat(8, step{status: 0}, step{status: 308})}
	_, _, err := run(t, rt, 4096)
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Message, "accepted no further bytes") {
		t.Fatalf("err = %v, want the no-progress stall error", err)
	}
	var cause *net0Error
	if !errors.As(err, &cause) {
		t.Errorf("stall error %v lost its transport cause", err)
	}
	if got := exit.For(err); got != 50 {
		t.Errorf("exit.For = %d, want 50", got)
	}
	if len(rt.puts) != 16 {
		t.Fatalf("PUT count = %d, want 16", len(rt.puts))
	}
	assertBackoffCurve(t, rt.waits, 0, 1, 2, 3, 4, 5, 6, 7)
}

// A 503 storm with no progress: 8 chunk PUTs and 8 probes, backed off, then a
// stall error carrying the upstream status (exit 40).
func TestResumable_5xxNoProgress_stallBoundTerminates(t *testing.T) {
	rt := &resumeRT{t: t, putSteps: repeat(8, step{status: 503}, step{status: 308})}
	_, _, err := run(t, rt, 4096)
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 503 || !strings.Contains(apiErr.Message, "upstream 5xx") {
		t.Fatalf("err = %v, want the 5xx stall error", err)
	}
	if got := exit.For(err); got != 40 {
		t.Errorf("exit.For = %d, want 40", got)
	}
	if len(rt.puts) != 16 {
		t.Fatalf("PUT count = %d, want 16", len(rt.puts))
	}
	assertBackoffCurve(t, rt.waits, 0, 1, 2, 3, 4, 5, 6, 7)
}

// A server that keeps answering 308 without moving its offset: 8 chunk PUTs,
// then the 308 stall error. The waits between them back off too.
func TestResumable_308NoProgress_stallBoundTerminates(t *testing.T) {
	rt := &resumeRT{t: t, putSteps: repeat(8, step{status: 308})}
	_, _, err := run(t, rt, 4096)
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 308 {
		t.Fatalf("err = %v, want the 308 stall error", err)
	}
	if len(rt.puts) != 8 {
		t.Fatalf("PUT count = %d, want 8", len(rt.puts))
	}
	assertBackoffCurve(t, rt.waits, 1, 2, 3, 4, 5, 6, 7)
}

// A terminal 403 from the probe that follows a transport error ends the upload
// at once (exit 11), with no further PUT.
func TestResumable_probe403AfterTransportError_isImmediate(t *testing.T) {
	rt := &resumeRT{t: t, putSteps: []step{
		{status: 0},
		{status: 403, body: `{"error":{"message":"denied"}}`},
	}}
	_, _, err := run(t, rt, 4096)
	if got := exit.For(err); got != 11 {
		t.Fatalf("exit.For = %d, want 11; err=%v", got, err)
	}
	if len(rt.puts) != 2 {
		t.Fatalf("PUT count = %d, want 2 (chunk, probe)", len(rt.puts))
	}
}

// Progress resets the curve: after a no-progress round and a round that moves
// the offset, the next failure waits the first backoff again.
func TestResumable_progressResetsBackoff(t *testing.T) {
	size := api.ResumableChunkSize + 300
	rt := &resumeRT{t: t, putSteps: []step{
		{status: 500}, {status: 308}, // no progress: stalls=1
		{status: 500}, {status: 308, rng: "bytes=0-8388607"}, // progress: stalls=0
		{status: 500}, {status: 308, rng: "bytes=0-8388607"}, // no progress again
		{status: 200, body: `{"versionCode":1}`},
	}}
	if _, _, err := run(t, rt, size); err != nil {
		t.Fatalf("ResumableUpload: %v", err)
	}
	assertBackoffCurve(t, rt.waits, 0, 1, 0)
}

// A chunk whose connection stops moving is cut by the chunk bound and resumed
// through the probe, instead of hanging until the runner kill.
func TestResumable_stalledChunk_isCutAndResumed(t *testing.T) {
	size := api.ResumableChunkSize + 300
	rt := &resumeRT{t: t, putSteps: []step{
		{hang: true},                          // chunk 1: connection stalls
		{status: 308, rng: "bytes=0-8388607"}, // probe: chunk 1 had landed
		{status: 200, body: `{"versionCode":4}`},
	}}
	start := time.Now()
	body, _, err := runCtx(t, context.Background(), rt, size, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("ResumableUpload: %v", err)
	}
	if string(body) != `{"versionCode":4}` {
		t.Fatalf("body = %q", body)
	}
	if len(rt.puts) != 3 {
		t.Fatalf("PUT count = %d, want 3 (stalled chunk, probe, chunk 2)", len(rt.puts))
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("upload took %v: the chunk bound did not cut the stall", d)
	}
}

// A canceled ctx is not a transient failure: the loop returns on the first
// failed PUT with no probe and no backoff, instead of spinning through the
// stall budget (the refutation note on TEST-03).
func TestResumable_canceledCtx_stopsAtOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt := &resumeRT{t: t, putSteps: repeat(8, step{status: 0}, step{status: 0})}
	rt.onPut = func(i int) {
		if i == 0 {
			cancel()
		}
	}
	_, _, err := runCtx(t, ctx, rt, 4096, time.Minute)
	if err == nil {
		t.Fatal("expected an error on a canceled upload")
	}
	if got := exit.For(err); got != 50 {
		t.Errorf("exit.For = %d, want 50", got)
	}
	if len(rt.puts) != 1 || len(rt.waits) != 0 {
		t.Fatalf("PUTs = %d, waits = %v; want 1 PUT and no backoff after cancel", len(rt.puts), rt.waits)
	}
}

// A cancel that lands during a backoff wait ends the upload without the probe.
func TestResumable_canceledDuringBackoff_skipsProbe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt := &resumeRT{t: t, putSteps: []step{{status: 500}, {status: 308}}}
	// The 500 is answered, then the cancel arrives before the probe's wait.
	rt.onPut = func(i int) {
		if i == 0 {
			cancel()
		}
	}
	_, _, err := runCtx(t, ctx, rt, 4096, time.Minute)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(rt.puts) != 1 {
		t.Fatalf("PUT count = %d, want 1 (no probe after cancel)", len(rt.puts))
	}
}
