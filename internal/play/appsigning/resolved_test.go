// Migration proof for #518: appsigning now takes its verbs and URLs from
// internal/apiregistry instead of local literals. The package had no test at
// all, so this file is also its first: it pins the ABSOLUTE URL and the verb of
// both calls, including the colon verbs (`appSigning:enrollApp`,
// `appSigning:rotateAppSigningKey`) and the fact that the sole path parameter
// is `name`, not `packageName`.
package appsigning_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/appsigning"
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

const base = "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/com.example.app/appSigning:"

func TestResolvedURLsUnchanged(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		call func(*http.Client) error
		want string
	}{
		{
			name: "appsigning.enrollApp",
			call: func(hc *http.Client) error {
				_, _, err := appsigning.Enroll(ctx, hc, "com.example.app", appsigning.EnrollOpts{KmsKeyResource: "projects/p/k"})
				return err
			},
			want: base + "enrollApp",
		},
		{
			name: "appsigning.rotateAppSigningKey",
			call: func(hc *http.Client) error {
				_, _, err := appsigning.Rotate(ctx, hc, "com.example.app", appsigning.RotateOpts{KmsKeyResource: "projects/p/k"})
				return err
			},
			want: base + "rotateAppSigningKey",
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
			if rt.verb != http.MethodPost {
				t.Errorf("verb = %q, want POST", rt.verb)
			}
		})
	}
}

// TestEmptyNameFailsBeforeTheWire asserts a missing path parameter is refused
// locally rather than sent as a truncated URL: enrolment changes the live
// signing key of an app, so a malformed request must not reach the wire.
func TestEmptyNameFailsBeforeTheWire(t *testing.T) {
	rt := &pinRT{}
	_, _, err := appsigning.Enroll(context.Background(), &http.Client{Transport: rt}, "", appsigning.EnrollOpts{})
	if err == nil {
		t.Fatal("Enroll with an empty name succeeded, want an error")
	}
	if rt.url != "" {
		t.Errorf("a request was sent (%q); the missing parameter must be caught before the wire", rt.url)
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.Error", err, err)
	}
	if !strings.Contains(apiErr.Message, `"name"`) {
		t.Errorf("message = %q, want it to name the name parameter", apiErr.Message)
	}
}
