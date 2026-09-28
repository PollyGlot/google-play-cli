// Migration proof for #518: recovery (and its lifecycle half) now takes its
// verbs and URLs from internal/apiregistry instead of local literals. The
// pre-existing tests are untouched; what is added here is the ABSOLUTE URL
// (host included) and the verb of each call, the three colon verbs
// (`:deploy`, `:cancel`, `:addTargeting`) included.
package recovery_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/testkit"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/recovery"
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

// TestEmptyRecoveryIDFailsBeforeTheWire asserts a missing path parameter is
// refused locally rather than sent as a truncated URL: cancel is irreversible,
// so a malformed request must not reach the wire.
func TestEmptyRecoveryIDFailsBeforeTheWire(t *testing.T) {
	rt := pinned()
	_, err := recovery.Cancel(context.Background(), &http.Client{Transport: rt}, "com.example.app", "")
	if err == nil {
		t.Fatal("Cancel with an empty recovery id succeeded, want an error")
	}
	if first(rt).URL != "" {
		t.Errorf("a request was sent (%q); the missing parameter must be caught before the wire", first(rt).URL)
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.Error", err, err)
	}
	if !strings.Contains(apiErr.Message, `"appRecoveryId"`) {
		t.Errorf("message = %q, want it to name the appRecoveryId parameter", apiErr.Message)
	}
}
