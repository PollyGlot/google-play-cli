package reviewerr

import (
	"errors"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

func exitCodeOf(t *testing.T, err error) int {
	t.Helper()
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	return coder.ExitCode()
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		wantExit int
		wantHint string // substring the message must contain ("" = passthrough)
	}{
		{"forbidden", 403, 11, "Reply to reviews"},
		{"not found", 404, 30, "gplay apps list"},
		{"server error passthrough", 500, 40, ""},
		{"conflict passthrough", 409, 60, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := &api.Error{Operation: "reviews.list", Package: "com.example.app", StatusCode: tc.status, Message: "boom"}
			got := Classify("com.example.app", in)
			if code := exitCodeOf(t, got); code != tc.wantExit {
				t.Errorf("status %d → exit %d, want %d", tc.status, code, tc.wantExit)
			}
			if tc.wantHint != "" && !strings.Contains(got.Error(), tc.wantHint) {
				t.Errorf("status %d message %q, want substring %q", tc.status, got.Error(), tc.wantHint)
			}
			// The wrapped *api.Error must remain reachable for the Coder chain.
			var apiErr *api.Error
			if !errors.As(got, &apiErr) {
				t.Errorf("status %d: wrapped *api.Error no longer reachable via errors.As", tc.status)
			}
		})
	}
}

func TestClassify_nonAPIError_passesThrough(t *testing.T) {
	in := errors.New("some transport hiccup")
	if got := Classify("com.example.app", in); got != in {
		t.Errorf("non-api error should pass through unchanged, got %v", got)
	}
}
