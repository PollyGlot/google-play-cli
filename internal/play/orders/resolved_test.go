package orders_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/testkit"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/orders"
)

func pinned() *testkit.Fake { return testkit.NewFake(testkit.Any(http.StatusOK, `{}`)) }

func first(f *testkit.Fake) testkit.Call {
	if calls := f.Calls(); len(calls) > 0 {
		return calls[0]
	}
	return testkit.Call{}
}

const base = "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/com.example.app"

func TestResolvedURLsUnchanged(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		call func(*http.Client) error
		verb string
		want string
	}{
		{
			name: "orders.get",
			call: func(hc *http.Client) error { _, _, err := orders.Get(ctx, hc, "com.example.app", "GPA.1"); return err },
			verb: http.MethodGet,
			want: base + "/orders/GPA.1",
		},
		{
			name: "orders.batchget",
			call: func(hc *http.Client) error {
				_, _, err := orders.BatchGet(ctx, hc, "com.example.app", []string{"GPA.1", "GPA.2"})
				return err
			},
			verb: http.MethodGet,
			want: base + "/orders:batchGet?orderIds=GPA.1&orderIds=GPA.2",
		},
		{
			name: "orders.refund",
			call: func(hc *http.Client) error {
				_, err := orders.Refund(ctx, hc, "com.example.app", "GPA.1", false)
				return err
			},
			verb: http.MethodPost,
			want: base + "/orders/GPA.1:refund",
		},
		{
			name: "orders.refund with revoke",
			call: func(hc *http.Client) error {
				_, err := orders.Refund(ctx, hc, "com.example.app", "GPA.1", true)
				return err
			},
			verb: http.MethodPost,
			want: base + "/orders/GPA.1:refund?revoke=true",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := pinned()
			if err := tc.call(&http.Client{Transport: rt}); err != nil {
				t.Fatalf("call: %v", err)
			}
			if first(rt).URL != tc.want {
				t.Errorf("URL = %q, want %q", first(rt).URL, tc.want)
			}
			if first(rt).Method != tc.verb {
				t.Errorf("verb = %q, want %q", first(rt).Method, tc.verb)
			}
		})
	}
}

func TestEmptyOrderIDFailsBeforeTheWire(t *testing.T) {
	rt := pinned()
	_, err := orders.Refund(context.Background(), &http.Client{Transport: rt}, "com.example.app", "", false)
	if err == nil {
		t.Fatal("Refund with an empty order id succeeded, want an error")
	}
	if first(rt).URL != "" {
		t.Errorf("a request was sent (%q); the missing parameter must be caught before the wire", first(rt).URL)
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.Error", err, err)
	}
	if !strings.Contains(apiErr.Message, `"orderId"`) {
		t.Errorf("message = %q, want it to name the orderId parameter", apiErr.Message)
	}
}
