// Migration proof for #518: recovery (and its lifecycle half) now takes its
// verbs and URLs from internal/apiregistry instead of local literals. The
// pre-existing tests are untouched; what is added here is the ABSOLUTE URL
// (host included) and the verb of each call, the three colon verbs
// (`:deploy`, `:cancel`, `:addTargeting`) included.
package recovery_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/recovery"
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

const base = "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/com.example.app/appRecoveries"

func TestResolvedURLsUnchanged(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		call func(*http.Client) error
		verb string
		want string
	}{
		{
			name: "apprecovery.create",
			call: func(hc *http.Client) error {
				_, _, err := recovery.Create(ctx, hc, "com.example.app", recovery.CreateOpts{AllUsers: true})
				return err
			},
			verb: http.MethodPost,
			want: base,
		},
		{
			name: "apprecovery.list",
			call: func(hc *http.Client) error { _, _, err := recovery.List(ctx, hc, "com.example.app", 42); return err },
			verb: http.MethodGet,
			want: base + "?versionCode=42",
		},
		{
			name: "apprecovery.deploy",
			call: func(hc *http.Client) error { _, err := recovery.Deploy(ctx, hc, "com.example.app", "7"); return err },
			verb: http.MethodPost,
			want: base + "/7:deploy",
		},
		{
			name: "apprecovery.cancel",
			call: func(hc *http.Client) error { _, err := recovery.Cancel(ctx, hc, "com.example.app", "7"); return err },
			verb: http.MethodPost,
			want: base + "/7:cancel",
		},
		{
			name: "apprecovery.addTargeting",
			call: func(hc *http.Client) error {
				_, err := recovery.AddTargeting(ctx, hc, "com.example.app", "7", recovery.BuildTargeting(true, nil, nil))
				return err
			},
			verb: http.MethodPost,
			want: base + "/7:addTargeting",
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

// TestEmptyRecoveryIDFailsBeforeTheWire asserts a missing path parameter is
// refused locally rather than sent as a truncated URL: cancel is irreversible,
// so a malformed request must not reach the wire.
func TestEmptyRecoveryIDFailsBeforeTheWire(t *testing.T) {
	rt := &pinRT{}
	_, err := recovery.Cancel(context.Background(), &http.Client{Transport: rt}, "com.example.app", "")
	if err == nil {
		t.Fatal("Cancel with an empty recovery id succeeded, want an error")
	}
	if rt.url != "" {
		t.Errorf("a request was sent (%q); the missing parameter must be caught before the wire", rt.url)
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.Error", err, err)
	}
	if !strings.Contains(apiErr.Message, `"appRecoveryId"`) {
		t.Errorf("message = %q, want it to name the appRecoveryId parameter", apiErr.Message)
	}
}
