package edits_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

type insertStatusRT struct {
	t        *testing.T
	status   int
	bodyJSON string
	fake     *testkit.Fake
}

func (r *insertStatusRT) client() *http.Client {
	r.fake = testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			body := r.bodyJSON
			if body == "" {
				body = fmt.Sprintf(`{"error":{"code":%d,"message":"stubbed"}}`, r.status)
			}
			return r.status, body, true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			return 204, "", true
		}
		return 0, "", false
	})
	return &http.Client{Transport: r.fake}
}

func (r *insertStatusRT) insertCalls() int {
	n := 0
	for _, c := range r.fake.Calls() {
		if c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits") {
			n++
		}
	}
	return n
}

type editsRT struct {
	t      *testing.T
	editID string
	fake   *testkit.Fake
}

func (r *editsRT) client() *http.Client {
	r.fake = testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return 200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, r.editID), true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			return 204, "", true
		case strings.HasSuffix(c.Path, ":commit"):
			return 200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"0"}`, r.editID), true
		}
		return 0, "", false
	})
	return &http.Client{Transport: r.fake}
}

func (r *editsRT) calls() []string { return callLines(r.fake) }

func callLines(f *testkit.Fake) []string {
	var out []string
	for _, c := range f.Calls() {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
}

func TestWithEdit_panicInClosure_triggersAutoDiscardAndRepropagates(t *testing.T) {
	rt := &editsRT{t: t, editID: "edit-panic"}
	hc := rt.client()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("WithEdit: expected panic to propagate, got none")
		}
		sawDelete := false
		for _, c := range rt.calls() {
			if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-panic") {
				sawDelete = true
				break
			}
		}
		if !sawDelete {
			t.Errorf("auto-discard not triggered after panic; calls = %v", rt.calls())
		}
	}()

	_ = edits.WithEdit(context.Background(), hc, "com.example.app", edits.Options{}, func(editID string) error {
		panic("simulated panic from closure")
	})
}

func TestWithEdit_panicInClosure_keepOnFailure_doesNotDiscard(t *testing.T) {
	rt := &editsRT{t: t, editID: "edit-panic-keep"}
	hc := rt.client()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("WithEdit: expected panic to propagate, got none")
		}
		for _, c := range rt.calls() {
			if strings.HasPrefix(c, "DELETE ") {
				t.Errorf("KeepOnFailure=true but saw DELETE after panic: calls = %v", rt.calls())
			}
		}
	}()

	_ = edits.WithEdit(context.Background(), hc, "com.example.app", edits.Options{KeepOnFailure: true}, func(editID string) error {
		panic("simulated panic from closure")
	})
}

func TestWithReadOnlyEdit_happyPath_insertsThenDiscardsNeverCommits(t *testing.T) {
	rt := &editsRT{t: t, editID: "edit-ro"}
	hc := rt.client()

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
	if len(rt.calls()) != len(want) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls()), rt.calls(), len(want))
	}
	for i, w := range want {
		if rt.calls()[i] != w {
			t.Errorf("call %d = %q, want %q", i, rt.calls()[i], w)
		}
	}
	for _, c := range rt.calls() {
		if strings.Contains(c, ":commit") {
			t.Errorf("read-only Edit must never commit; calls = %v", rt.calls())
		}
	}
}

func TestWithReadOnlyEdit_closureError_stillDiscardsAndPropagates(t *testing.T) {
	rt := &editsRT{t: t, editID: "edit-ro-err"}
	hc := rt.client()

	sentinel := errors.New("tracks.get blew up")
	err := edits.WithReadOnlyEdit(context.Background(), hc, "com.example.app", func(editID string) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithReadOnlyEdit error = %v, want sentinel", err)
	}

	sawDelete := false
	for _, c := range rt.calls() {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-ro-err") {
			sawDelete = true
		}
		if strings.Contains(c, ":commit") {
			t.Errorf("read-only Edit must never commit; calls = %v", rt.calls())
		}
	}
	if !sawDelete {
		t.Errorf("discard not triggered after closure error; calls = %v", rt.calls())
	}
}

func TestValidate_happyPath_insertsThenDiscardsNeverCommits(t *testing.T) {
	rt := &editsRT{t: t, editID: "edit-validate"}
	hc := rt.client()

	if err := edits.Validate(context.Background(), hc, "com.example.app"); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	want := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-validate",
	}
	if len(rt.calls()) != len(want) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls()), rt.calls(), len(want))
	}
	for i, w := range want {
		if rt.calls()[i] != w {
			t.Errorf("call %d = %q, want %q", i, rt.calls()[i], w)
		}
	}
}

func TestValidate_403_mapsToExitCode11(t *testing.T) {
	rt := &insertStatusRT{t: t, status: http.StatusForbidden}
	hc := rt.client()

	err := edits.Validate(context.Background(), hc, "com.example.app")
	if err == nil {
		t.Fatal("Validate: expected error on 403, got nil")
	}
	if got := exit.For(err); got != 11 {
		t.Errorf("exit.For = %d, want 11; err = %v", got, err)
	}
	if rt.insertCalls() != 1 {
		t.Errorf("insertCalls = %d, want 1", rt.insertCalls())
	}
}

func TestValidate_404_mapsToExitCode30(t *testing.T) {
	rt := &insertStatusRT{t: t, status: http.StatusNotFound}
	hc := rt.client()

	err := edits.Validate(context.Background(), hc, "com.example.unknown")
	if err == nil {
		t.Fatal("Validate: expected error on 404, got nil")
	}
	if got := exit.For(err); got != 30 {
		t.Errorf("exit.For = %d, want 30; err = %v", got, err)
	}
}

func TestValidate_editAlreadyOpen_mapsToExitCode30WithRecoveryHint(t *testing.T) {
	rt := &insertStatusRT{
		t:        t,
		status:   http.StatusBadRequest,
		bodyJSON: `{"error":{"code":400,"message":"Edit ID is required.","errors":[{"reason":"editAlreadyExists","message":"Edit already exists for this app."}]}}`,
	}
	hc := rt.client()

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

type validateInsertOKDeleteFailRT struct {
	t      *testing.T
	editID string
}

func (r *validateInsertOKDeleteFailRT) client() *http.Client {
	return &http.Client{Transport: testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return 200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, r.editID), true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			return 503, `{"error":{"code":503,"message":"backend hiccup"}}`, true
		}
		return 0, "", false
	})}
}

func TestValidate_discardFailure_surfacesDanglingEditError(t *testing.T) {
	rt := &validateInsertOKDeleteFailRT{t: t, editID: "edit-leak"}
	hc := rt.client()

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

func TestValidate_editAlreadyExistsOn429_forwardsExitCode60(t *testing.T) {
	rt := &insertStatusRT{
		t:        t,
		status:   http.StatusTooManyRequests,
		bodyJSON: `{"error":{"code":429,"message":"rate limited","errors":[{"reason":"editAlreadyExists","message":"already open"}]}}`,
	}
	hc := rt.client()

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

func TestWithReadOnlyEdit_panicInClosure_triggersDiscardAndRepropagates(t *testing.T) {
	rt := &editsRT{t: t, editID: "edit-ro-panic"}
	hc := rt.client()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("WithReadOnlyEdit: expected panic to propagate, got none")
		}
		sawDelete := false
		for _, c := range rt.calls() {
			if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-ro-panic") {
				sawDelete = true
				break
			}
		}
		if !sawDelete {
			t.Errorf("auto-discard not triggered after panic; calls = %v", rt.calls())
		}
	}()

	_ = edits.WithReadOnlyEdit(context.Background(), hc, "com.example.app", func(editID string) error {
		panic("simulated panic from read-only closure")
	})
}

func TestWithEdit_nothingToCommit_discardsAndSucceeds(t *testing.T) {
	for _, keep := range []bool{false, true} {
		rt := &editsRT{t: t, editID: "edit-noop"}
		err := edits.WithEdit(context.Background(), rt.client(), "com.example.app", edits.Options{KeepOnFailure: keep}, func(string) error {
			return fmt.Errorf("skipped: %w", edits.ErrNothingToCommit)
		})
		if err != nil {
			t.Fatalf("keep=%v: WithEdit = %v, want nil", keep, err)
		}
		want := []string{
			"POST /androidpublisher/v3/applications/com.example.app/edits",
			"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-noop",
		}
		if got := rt.calls(); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("keep=%v: calls = %v, want %v", keep, got, want)
		}
	}
}

func TestWithEdit_nothingToCommit_failedDiscard_reportsDanglingEdit(t *testing.T) {
	fake := testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return 200, `{"id":"edit-stuck"}`, true
		case c.Method == http.MethodDelete:
			return 403, `{"error":{"code":403,"message":"denied","status":"PERMISSION_DENIED"}}`, true
		}
		return 0, "", false
	})
	err := edits.WithEdit(context.Background(), &http.Client{Transport: fake}, "com.example.app", edits.Options{}, func(string) error {
		return edits.ErrNothingToCommit
	})
	var dangling *edits.DanglingEditError
	if !errors.As(err, &dangling) {
		t.Fatalf("WithEdit = %v, want *DanglingEditError", err)
	}
	if dangling.EditID != "edit-stuck" {
		t.Errorf("EditID = %q, want edit-stuck", dangling.EditID)
	}
	if got := exit.For(err); got != 11 {
		t.Errorf("exit %d, want 11 (the DELETE's 403)", got)
	}
}

func TestWithEdit_nothingToCommit_explicitEditUntouched(t *testing.T) {
	rt := &editsRT{t: t, editID: "unused"}
	err := edits.WithEdit(context.Background(), rt.client(), "com.example.app", edits.Options{ExplicitEditID: "edit-pinned"}, func(string) error {
		return edits.ErrNothingToCommit
	})
	if err != nil {
		t.Fatalf("WithEdit = %v, want nil", err)
	}
	if got := rt.calls(); len(got) != 0 {
		t.Errorf("explicit Edit saw lifecycle calls %v, want none", got)
	}
}
