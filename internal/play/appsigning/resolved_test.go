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
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/testkit"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/appsigning"
)

// pinned answers {} to every call and records what was sent.
func pinned() *testkit.Fake { return testkit.NewFake(testkit.Any(http.StatusOK, `{}`)) }

// first is the first call f recorded, or the zero Call when none was sent.
func first(f *testkit.Fake) testkit.Call {
	if calls := f.Calls(); len(calls) > 0 {
		return calls[0]
	}
	return testkit.Call{}
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
			rt := pinned()
			if err := tc.call(&http.Client{Transport: rt}); err != nil {
				t.Fatalf("call: %v", err)
			}
			if first(rt).URL != tc.want {
				t.Errorf("URL = %q, want %q", first(rt).URL, tc.want)
			}
			if first(rt).Method != http.MethodPost {
				t.Errorf("verb = %q, want POST", first(rt).Method)
			}
		})
	}
}

// TestEmptyNameFailsBeforeTheWire asserts a missing path parameter is refused
// locally rather than sent as a truncated URL: enrolment changes the live
// signing key of an app, so a malformed request must not reach the wire.
func TestEmptyNameFailsBeforeTheWire(t *testing.T) {
	rt := pinned()
	_, _, err := appsigning.Enroll(context.Background(), &http.Client{Transport: rt}, "", appsigning.EnrollOpts{})
	if err == nil {
		t.Fatal("Enroll with an empty name succeeded, want an error")
	}
	if first(rt).URL != "" {
		t.Errorf("a request was sent (%q); the missing parameter must be caught before the wire", first(rt).URL)
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.Error", err, err)
	}
	if !strings.Contains(apiErr.Message, `"name"`) {
		t.Errorf("message = %q, want it to name the name parameter", apiErr.Message)
	}
}
