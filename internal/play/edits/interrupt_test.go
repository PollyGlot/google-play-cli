package edits_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

type deadlineProbe struct {
	fake *testkit.Fake

	mu     sync.Mutex
	budget time.Duration
}

func (p *deadlineProbe) client() *http.Client {
	return &http.Client{Transport: testkit.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodDelete {
			if dl, ok := req.Context().Deadline(); ok {
				p.mu.Lock()
				p.budget = time.Until(dl)
				p.mu.Unlock()
			}
		}
		return p.fake.RoundTrip(req)
	})}
}

func newDeadlineProbe() *deadlineProbe {
	return &deadlineProbe{fake: testkit.NewFake(
		func(c testkit.Call) (int, string, bool) {
			if c.Method == http.MethodPost && c.Path == "/androidpublisher/v3/applications/com.example.app/edits" {
				return 200, `{"id":"e1"}`, true
			}
			return 0, "", false
		},
		func(c testkit.Call) (int, string, bool) {
			return 204, "", c.Method == http.MethodDelete
		},
	)}
}

func deletes(f *testkit.Fake) int {
	n := 0
	for _, c := range f.Calls() {
		if c.Method == http.MethodDelete && c.Path == "/androidpublisher/v3/applications/com.example.app/edits/e1" {
			n++
		}
	}
	return n
}

func TestWithEdit_canceledCtx_discardsWithinInterruptBound(t *testing.T) {
	probe := newDeadlineProbe()
	hc := probe.client()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := edits.WithEdit(ctx, hc, "com.example.app", edits.Options{}, func(string) error {
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n := deletes(probe.fake); n != 1 {
		t.Fatalf("DELETE edits/e1 calls = %d, want 1", n)
	}
	for _, c := range probe.fake.Calls() {
		if c.Method == http.MethodPost && c.Path != "/androidpublisher/v3/applications/com.example.app/edits" {
			t.Errorf("unexpected call after cancel: %s %s", c.Method, c.Path)
		}
	}
	if probe.budget <= 0 || probe.budget > 5*time.Second {
		t.Errorf("interrupted discard budget = %v, want within (0, 5s]", probe.budget)
	}
}

func TestWithEdit_failure_discardsWithRegularBound(t *testing.T) {
	probe := newDeadlineProbe()
	hc := probe.client()

	err := edits.WithEdit(context.Background(), hc, "com.example.app", edits.Options{}, func(string) error {
		return errors.New("boom")
	})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v, want boom", err)
	}
	if n := deletes(probe.fake); n != 1 {
		t.Fatalf("DELETE calls = %d, want 1", n)
	}
	if probe.budget <= 5*time.Second || probe.budget > 10*time.Second {
		t.Errorf("discard budget = %v, want within (5s, 10s]", probe.budget)
	}
}

func TestWithReadOnlyEdit_canceledCtx_discardsWithinInterruptBound(t *testing.T) {
	probe := newDeadlineProbe()
	hc := probe.client()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = edits.WithReadOnlyEdit(ctx, hc, "com.example.app", func(string) error {
		cancel()
		return ctx.Err()
	})
	if n := deletes(probe.fake); n != 1 {
		t.Fatalf("DELETE calls = %d, want 1", n)
	}
	if probe.budget <= 0 || probe.budget > 5*time.Second {
		t.Errorf("interrupted discard budget = %v, want within (0, 5s]", probe.budget)
	}
}
