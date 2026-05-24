// Package edits_test exercises the Edit transactional lifecycle. The
// focus here is on the failure paths that don't already have coverage
// via the orchestrator-level tests — chiefly the panic-recovery
// branch, where a runaway panic inside the closure must still trigger
// auto-discard before propagating so a 24h Edit lock does not leak.
package edits_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/edits"
)

type editsRT struct {
	t      *testing.T
	editID string

	mu    sync.Mutex
	calls []string
}

func (r *editsRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	switch {
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits"):
		return jsonResp(200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, r.editID)), nil
	case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/"):
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	case strings.HasSuffix(req.URL.Path, ":commit"):
		return jsonResp(200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"0"}`, r.editID)), nil
	}
	r.t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
	return nil, nil
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// TestWithEdit_panicInClosure_triggersAutoDiscardAndRepropagates
// asserts the panic-safety net: when the caller-supplied closure
// panics (e.g. an unguarded slice index, a nil-pointer deref in
// downstream code), WithEdit MUST best-effort discard the open Edit
// before letting the panic continue. Without this, a panic leaks a
// 24h Edit lock and blocks the user's next publish.
func TestWithEdit_panicInClosure_triggersAutoDiscardAndRepropagates(t *testing.T) {
	rt := &editsRT{t: t, editID: "edit-panic"}
	hc := &http.Client{Transport: rt}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("WithEdit: expected panic to propagate, got none")
		}
		// Cleanup must have run before the panic continued.
		sawDelete := false
		for _, c := range rt.calls {
			if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-panic") {
				sawDelete = true
				break
			}
		}
		if !sawDelete {
			t.Errorf("auto-discard not triggered after panic; calls = %v", rt.calls)
		}
	}()

	_ = edits.WithEdit(context.Background(), hc, "com.example.app", edits.Options{}, func(editID string) error {
		panic("simulated panic from closure")
	})
}

// TestWithEdit_panicInClosure_keepOnFailure_doesNotDiscard asserts the
// --keep-edit-on-failure opt-out is honored on the panic path too:
// the panic re-propagates and no DELETE is emitted, so the operator
// can inspect the still-open Edit ID via `gplay edits discard`.
func TestWithEdit_panicInClosure_keepOnFailure_doesNotDiscard(t *testing.T) {
	rt := &editsRT{t: t, editID: "edit-panic-keep"}
	hc := &http.Client{Transport: rt}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("WithEdit: expected panic to propagate, got none")
		}
		for _, c := range rt.calls {
			if strings.HasPrefix(c, "DELETE ") {
				t.Errorf("KeepOnFailure=true but saw DELETE after panic: calls = %v", rt.calls)
			}
		}
	}()

	_ = edits.WithEdit(context.Background(), hc, "com.example.app", edits.Options{KeepOnFailure: true}, func(editID string) error {
		panic("simulated panic from closure")
	})
}
