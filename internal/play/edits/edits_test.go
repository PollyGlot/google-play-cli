// Package edits_test exercises the Edit transactional lifecycle. The
// focus here is on the failure paths that don't already have coverage
// via the orchestrator-level tests — chiefly the panic-recovery
// branch, where a runaway panic inside the closure must still trigger
// auto-discard before propagating so a 24h Edit lock does not leak.
package edits_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
)

// insertStatusRT returns the configured status on the initial POST /edits
// (with a canned error envelope), succeeds for any other request. Lets
// each error-mapping test stub one status at a time without re-defining
// the full RoundTripper shape.
type insertStatusRT struct {
	t           *testing.T
	status      int
	bodyJSON    string
	mu          sync.Mutex
	insertCalls int
}

func (r *insertStatusRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits") {
		r.insertCalls++
		body := r.bodyJSON
		if body == "" {
			body = fmt.Sprintf(`{"error":{"code":%d,"message":"stubbed"}}`, r.status)
		}
		return jsonResp(r.status, body), nil
	}
	if req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/") {
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	r.t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
	return nil, nil
}

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

// TestWithReadOnlyEdit_happyPath_insertsThenDiscardsNeverCommits asserts
// the read-only lifecycle: open an Edit, run the closure, then ALWAYS
// discard — never commit. A read-only listing (tracks.get) has no
// changes to commit; committing would at best burn a daily-publish slot
// and at worst mutate state, so the Edit must be discarded on success.
func TestWithReadOnlyEdit_happyPath_insertsThenDiscardsNeverCommits(t *testing.T) {
	rt := &editsRT{t: t, editID: "edit-ro"}
	hc := &http.Client{Transport: rt}

	var sawEditID string
	err := edits.WithReadOnlyEdit(context.Background(), hc, "com.example.app", func(editID string) error {
		sawEditID = editID
		return nil
	})
	if err != nil {
		t.Fatalf("WithReadOnlyEdit: %v", err)
	}
	if sawEditID != "edit-ro" {
		t.Errorf("closure saw editID %q, want %q", sawEditID, "edit-ro")
	}

	want := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-ro",
	}
	if len(rt.calls) != len(want) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls), rt.calls, len(want))
	}
	for i, w := range want {
		if rt.calls[i] != w {
			t.Errorf("call %d = %q, want %q", i, rt.calls[i], w)
		}
	}
	for _, c := range rt.calls {
		if strings.Contains(c, ":commit") {
			t.Errorf("read-only Edit must never commit; calls = %v", rt.calls)
		}
	}
}

// TestWithReadOnlyEdit_closureError_stillDiscardsAndPropagates asserts
// that a closure failure still triggers the discard (no Edit leak) and
// that the closure's error propagates verbatim so the caller's exit-code
// mapping stays intact.
func TestWithReadOnlyEdit_closureError_stillDiscardsAndPropagates(t *testing.T) {
	rt := &editsRT{t: t, editID: "edit-ro-err"}
	hc := &http.Client{Transport: rt}

	sentinel := errors.New("tracks.get blew up")
	err := edits.WithReadOnlyEdit(context.Background(), hc, "com.example.app", func(editID string) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithReadOnlyEdit error = %v, want sentinel", err)
	}

	sawDelete := false
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-ro-err") {
			sawDelete = true
		}
		if strings.Contains(c, ":commit") {
			t.Errorf("read-only Edit must never commit; calls = %v", rt.calls)
		}
	}
	if !sawDelete {
		t.Errorf("discard not triggered after closure error; calls = %v", rt.calls)
	}
}

// TestValidate_happyPath_insertsThenDiscardsNeverCommits asserts that
// edits.Validate (the cheap access probe used by `gplay apps add`)
// opens an Edit, never commits, then discards — so it does not burn a
// daily-publish slot or leak the open Edit.
func TestValidate_happyPath_insertsThenDiscardsNeverCommits(t *testing.T) {
	rt := &editsRT{t: t, editID: "edit-validate"}
	hc := &http.Client{Transport: rt}

	if err := edits.Validate(context.Background(), hc, "com.example.app"); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	want := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-validate",
	}
	if len(rt.calls) != len(want) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls), rt.calls, len(want))
	}
	for i, w := range want {
		if rt.calls[i] != w {
			t.Errorf("call %d = %q, want %q", i, rt.calls[i], w)
		}
	}
}

// TestValidate_403_mapsToExitCode11 asserts that a 403 from the
// upstream insert call is surfaced as an *api.Error with ExitCode 11
// (authorization), so `gplay apps add` can refuse to persist the package
// when the active service-account is not invited on the app.
func TestValidate_403_mapsToExitCode11(t *testing.T) {
	rt := &insertStatusRT{t: t, status: http.StatusForbidden}
	hc := &http.Client{Transport: rt}

	err := edits.Validate(context.Background(), hc, "com.example.app")
	if err == nil {
		t.Fatal("Validate: expected error on 403, got nil")
	}
	if got := exit.For(err); got != 11 {
		t.Errorf("exit.For = %d, want 11; err = %v", got, err)
	}
	if rt.insertCalls != 1 {
		t.Errorf("insertCalls = %d, want 1", rt.insertCalls)
	}
}

// TestValidate_404_mapsToExitCode30 asserts that a 404 from the
// upstream insert call (the package does not exist on the developer
// account) is surfaced as exit 30 — a typo or unknown package, not an
// auth problem. The active credential is fine; the input is wrong.
func TestValidate_404_mapsToExitCode30(t *testing.T) {
	rt := &insertStatusRT{t: t, status: http.StatusNotFound}
	hc := &http.Client{Transport: rt}

	err := edits.Validate(context.Background(), hc, "com.example.unknown")
	if err == nil {
		t.Fatal("Validate: expected error on 404, got nil")
	}
	if got := exit.For(err); got != 30 {
		t.Errorf("exit.For = %d, want 30; err = %v", got, err)
	}
}

// TestValidate_editAlreadyOpen_mapsToExitCode30WithRecoveryHint asserts
// that when the package already has an open Edit (the user previously
// ran `gplay edits begin` and forgot to commit/discard, or a CI run
// died mid-publish), Validate returns a *EditConflictError carrying a
// distinct exit 30 and a recovery hint pointing at `gplay edits
// discard`. The distinct typed error lets `gplay apps add` surface a
// targeted message instead of the generic "HTTP 4xx" wording.
//
// Google Play signals "editAlreadyExists" via an HTTP 400 with a
// per-error reason field on the JSON error envelope.
func TestValidate_editAlreadyOpen_mapsToExitCode30WithRecoveryHint(t *testing.T) {
	rt := &insertStatusRT{
		t:        t,
		status:   http.StatusBadRequest,
		bodyJSON: `{"error":{"code":400,"message":"Edit ID is required.","errors":[{"reason":"editAlreadyExists","message":"Edit already exists for this app."}]}}`,
	}
	hc := &http.Client{Transport: rt}

	err := edits.Validate(context.Background(), hc, "com.example.app")
	if err == nil {
		t.Fatal("Validate: expected EditConflictError, got nil")
	}
	var conflict *edits.EditConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("Validate err = %v (%T); want *edits.EditConflictError", err, err)
	}
	if got := exit.For(err); got != 30 {
		t.Errorf("exit.For = %d, want 30; err = %v", got, err)
	}
	// The recovery hint must point at a path that exists today (Edit
	// auto-expiry or the Play Console), NOT at `gplay edits discard`
	// which is planned per DESIGN.md §4 but not wired in cmd/gplay/main.go.
	if !strings.Contains(err.Error(), "Play Console") && !strings.Contains(err.Error(), "expire") {
		t.Errorf("error message should hint at a today-working recovery path (Play Console / Edit expiry); got %q", err.Error())
	}
	if strings.Contains(err.Error(), "gplay edits discard") {
		t.Errorf("error message must not point at the unimplemented `gplay edits discard` subcommand; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "com.example.app") {
		t.Errorf("error message should name the package; got %q", err.Error())
	}
}

// validateInsertOKDeleteFailRT serves a successful insert then a 5xx
// on the deferred delete, so tests can assert Validate surfaces the
// discard failure (don't silently report success when an Edit was just
// leaked for ~24h).
type validateInsertOKDeleteFailRT struct {
	t      *testing.T
	editID string

	mu    sync.Mutex
	calls []string
}

func (r *validateInsertOKDeleteFailRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	switch {
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits"):
		return jsonResp(200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, r.editID)), nil
	case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/"):
		return jsonResp(503, `{"error":{"code":503,"message":"backend hiccup"}}`), nil
	}
	r.t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
	return nil, nil
}

// TestValidate_discardFailure_surfacesDanglingEditError asserts the
// fix for the silent-leak bug: a successful insert followed by a failed
// delete must NOT return nil. The Validate probe's whole point is to
// catch access issues at registration time; reporting success while
// leaking a 24h-locked Edit corrupts the very state it was meant to
// verify. The surfaced error must carry the open Edit ID so the
// operator can release it via the Play Console.
func TestValidate_discardFailure_surfacesDanglingEditError(t *testing.T) {
	rt := &validateInsertOKDeleteFailRT{t: t, editID: "edit-leak"}
	hc := &http.Client{Transport: rt}

	err := edits.Validate(context.Background(), hc, "com.example.app")
	if err == nil {
		t.Fatal("Validate: expected DanglingEditError on discard failure, got nil (Edit was just leaked!)")
	}
	var dangling *edits.DanglingEditError
	if !errors.As(err, &dangling) {
		t.Fatalf("Validate err = %v (%T); want *edits.DanglingEditError", err, err)
	}
	if dangling.EditID != "edit-leak" {
		t.Errorf("DanglingEditError.EditID = %q, want %q", dangling.EditID, "edit-leak")
	}
}

// TestValidate_editAlreadyExistsOn429_forwardsExitCode60 asserts the
// fix for the hardcoded-30 bug: when Google Play surfaces
// editAlreadyExists on an HTTP 429 (rate-limited probe), the wrapped
// *api.Error maps the status to exit 60 (state conflict, retry with
// backoff) via StatusToExitCode. EditConflictError's ExitCode must
// forward that decision, not override it with the recoverable-4xx
// exit 30, otherwise CI retry-on-60 logic silently fails to retry
// what is genuinely a transient state.
func TestValidate_editAlreadyExistsOn429_forwardsExitCode60(t *testing.T) {
	rt := &insertStatusRT{
		t:        t,
		status:   http.StatusTooManyRequests,
		bodyJSON: `{"error":{"code":429,"message":"rate limited","errors":[{"reason":"editAlreadyExists","message":"already open"}]}}`,
	}
	hc := &http.Client{Transport: rt}

	err := edits.Validate(context.Background(), hc, "com.example.app")
	if err == nil {
		t.Fatal("Validate: expected error on 429+editAlreadyExists, got nil")
	}
	var conflict *edits.EditConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("Validate err = %v (%T); want *edits.EditConflictError", err, err)
	}
	if got := exit.For(err); got != 60 {
		t.Errorf("exit.For = %d, want 60 (forwarded from wrapped 429 → state conflict); err = %v", got, err)
	}
}

// TestWithReadOnlyEdit_panicInClosure_triggersDiscardAndRepropagates
// mirrors WithEdit's panic-safety net: a panic inside the closure must
// still best-effort discard the open Edit before propagating, so a 24h
// Edit lock does not leak.
func TestWithReadOnlyEdit_panicInClosure_triggersDiscardAndRepropagates(t *testing.T) {
	rt := &editsRT{t: t, editID: "edit-ro-panic"}
	hc := &http.Client{Transport: rt}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("WithReadOnlyEdit: expected panic to propagate, got none")
		}
		sawDelete := false
		for _, c := range rt.calls {
			if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-ro-panic") {
				sawDelete = true
				break
			}
		}
		if !sawDelete {
			t.Errorf("auto-discard not triggered after panic; calls = %v", rt.calls)
		}
	}()

	_ = edits.WithReadOnlyEdit(context.Background(), hc, "com.example.app", func(editID string) error {
		panic("simulated panic from read-only closure")
	})
}
