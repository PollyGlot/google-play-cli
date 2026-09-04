// Migration proof for #518: orders now takes its verbs and URLs from
// internal/apiregistry instead of local literals. orders_test.go is untouched
// and still asserts the behaviour by substring; what is added here is the
// ABSOLUTE URL (host included) and the verb of each call, the `orders:batchGet`
// and `{orderId}:refund` custom verbs included.
package orders_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/orders"
)

type pinRT struct {
	url, verb string
}

func (r *pinRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if r.url == "" {
		r.url, r.verb = req.URL.String(), req.Method
	}
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{}`)),
	}, nil
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
			rt := &pinRT{}
			if err := tc.call(&http.Client{Transport: rt}); err != nil {
				t.Fatalf("call: %v", err)
			}
			if rt.url != tc.want {
				t.Errorf("URL = %q, want %q", rt.url, tc.want)
			}
			if rt.verb != tc.verb {
				t.Errorf("verb = %q, want %q", rt.verb, tc.verb)
			}
		})
	}
}

// TestEmptyOrderIDFailsBeforeTheWire asserts a missing path parameter is
// refused locally rather than sent as a truncated URL: on a money-moving
// endpoint, a request that reaches the wire malformed is the worst outcome.
func TestEmptyOrderIDFailsBeforeTheWire(t *testing.T) {
	rt := &pinRT{}
	_, err := orders.Refund(context.Background(), &http.Client{Transport: rt}, "com.example.app", "", false)
	if err == nil {
		t.Fatal("Refund with an empty order id succeeded, want an error")
	}
	if rt.url != "" {
		t.Errorf("a request was sent (%q); the missing parameter must be caught before the wire", rt.url)
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.Error", err, err)
	}
	if !strings.Contains(apiErr.Message, `"orderId"`) {
		t.Errorf("message = %q, want it to name the orderId parameter", apiErr.Message)
	}
}
