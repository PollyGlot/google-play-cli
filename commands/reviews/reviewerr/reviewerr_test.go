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

func TestClassifyReply(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		wantExit int
		wantHint string // substring the message must contain ("" = passthrough)
	}{
		// 403 reuses the SHARED "Reply to reviews" grant hint (issue #62).
		{"forbidden shares the grant hint", 403, 11, "Reply to reviews"},
		// 404 on reply means the reviewId is unknown — NOT the package, so it
		// must not point the operator at `gplay apps list`.
		{"not found names the review", 404, 30, "review"},
		{"server error passthrough", 500, 40, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := &api.Error{Operation: "reviews.reply", Package: "com.example.app", StatusCode: tc.status, Message: "boom"}
			got := ClassifyReply("com.example.app", "gp:REVIEW123", in)
			if code := exitCodeOf(t, got); code != tc.wantExit {
				t.Errorf("status %d → exit %d, want %d", tc.status, code, tc.wantExit)
			}
			if tc.wantHint != "" && !strings.Contains(got.Error(), tc.wantHint) {
				t.Errorf("status %d message %q, want substring %q", tc.status, got.Error(), tc.wantHint)
			}
			var apiErr *api.Error
			if !errors.As(got, &apiErr) {
				t.Errorf("status %d: wrapped *api.Error no longer reachable", tc.status)
			}
		})
	}
	// The 404 hint must NOT carry list's package-not-found guidance.
	in := &api.Error{Operation: "reviews.reply", Package: "com.example.app", StatusCode: 404, Message: "boom"}
	if got := ClassifyReply("com.example.app", "gp:REVIEW123", in); strings.Contains(got.Error(), "gplay apps list") {
		t.Errorf("reply 404 must not borrow list's package hint, got %q", got.Error())
	}
}

func TestClassify_nonAPIError_passesThrough(t *testing.T) {
	in := errors.New("some transport hiccup")
	if got := Classify("com.example.app", in); got != in {
		t.Errorf("non-api error should pass through unchanged, got %v", got)
	}
}
