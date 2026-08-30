package signingcmd_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/signing/signingcmd"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/appsigning"
)

// errRT answers every request with the configured status and body: enough to
// drive the *api.Error path offline.
type errRT struct {
	status int
	body   string
}

func (e errRT) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: e.status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(e.body)),
	}, nil
}

// TestEnroll_nonSuccessBecomesApiError asserts a refusal surfaces as *api.Error
// tagged with the REST method id, so the exit-code taxonomy maps transparently.
func TestEnroll_nonSuccessBecomesApiError(t *testing.T) {
	hc := &http.Client{Transport: errRT{status: http.StatusForbidden, body: `{"error":{"code":403,"message":"caller lacks permission"}}`}}
	_, _, err := appsigning.Enroll(context.Background(), hc, "com.example.app", appsigning.EnrollOpts{KmsKeyResource: "k"})
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *api.Error", err)
	}
	if apiErr.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, want 403", apiErr.StatusCode)
	}
	if apiErr.Operation != "appsigning.enrollApp" {
		t.Errorf("Operation = %q, want appsigning.enrollApp", apiErr.Operation)
	}
}

// TestClassify_addsActionableHints asserts a 404 and a 403 become messages an
// agent can act on, while keeping the wrapped *api.Error's exit code.
func TestClassify_addsActionableHints(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusNotFound, "gplay apps list"},
		{http.StatusForbidden, "Decrypt and Sign"},
	}
	for _, tc := range cases {
		cause := &api.Error{Operation: "appsigning.enrollApp", StatusCode: tc.status, Message: "boom"}
		got := signingcmd.Classify("com.example.app", cause)
		if !strings.Contains(got.Error(), tc.want) {
			t.Errorf("Classify(%d) = %v, should hint %q", tc.status, got, tc.want)
		}
		var apiErr *api.Error
		if !errors.As(got, &apiErr) {
			t.Errorf("Classify(%d) must keep the *api.Error unwrappable", tc.status)
		}
	}
	// An error that is not an *api.Error passes through untouched.
	plain := errors.New("dial tcp: no route to host")
	if got := signingcmd.Classify("com.example.app", plain); !errors.Is(got, plain) {
		t.Errorf("Classify must pass a non-API error through: %v", got)
	}
}

// TestRotationReasons_coverTheApiEnumMinusUnspecified pins the vocabulary: the
// UNSPECIFIED value is deliberately not offerable, the API rejects it.
func TestRotationReasons_coverTheApiEnumMinusUnspecified(t *testing.T) {
	got := appsigning.RotationReasons()
	want := []string{"compromised-key", "other", "routine-key-upgrade", "use-same-key-for-multiple-apps", "use-stronger-key"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("RotationReasons() = %v, want %v (sorted)", got, want)
	}
	for _, choice := range got {
		enum, ok := appsigning.RotationReason(choice)
		if !ok || enum == "" || enum == "KEY_ROTATION_REASON_UNSPECIFIED" {
			t.Errorf("RotationReason(%q) = %q, %v", choice, enum, ok)
		}
	}
	if _, ok := appsigning.RotationReason("KEY_ROTATION_REASON_UNSPECIFIED"); ok {
		t.Error("the UNSPECIFIED enum must not be an accepted --reason choice")
	}
}
