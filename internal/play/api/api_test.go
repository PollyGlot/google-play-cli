// Package api_test exercises the HTTP-status-to-exit-code mapping and
// the Google API error-envelope parser. Both are pure functions so the
// tests are table-driven.
package api_test

import (
	"errors"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

func TestStatusToExitCode(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   int
	}{
		{"transport-level (no response)", 0, 50},
		{"403 forbidden → authorization", 403, 11},
		{"404 not found → generic 4xx", 404, 30},
		{"400 bad request → generic 4xx", 400, 30},
		{"409 conflict → state conflict", 409, 60},
		{"410 gone → generic 4xx", 410, 30},
		{"418 teapot → generic 4xx", 418, 30},
		{"500 server error → 5xx retryable", 500, 40},
		{"502 bad gateway → 5xx retryable", 502, 40},
		{"503 service unavailable → 5xx retryable", 503, 40},
		{"504 gateway timeout → 5xx retryable", 504, 40},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := api.StatusToExitCode(tc.status)
			if got != tc.want {
				t.Errorf("StatusToExitCode(%d) = %d, want %d", tc.status, got, tc.want)
			}
		})
	}
}

func TestAPIErrorMessage(t *testing.T) {
	cases := []struct {
		name   string
		body   []byte
		status int
		want   string
	}{
		{
			name:   "well-formed envelope returns message",
			body:   []byte(`{"error":{"code":403,"message":"Service account is not invited on the app"}}`),
			status: 403,
			want:   "Service account is not invited on the app",
		},
		{
			name:   "empty body falls back to HTTP <status>",
			body:   nil,
			status: 503,
			want:   "HTTP 503",
		},
		{
			name:   "malformed body returns trimmed raw",
			body:   []byte("  <html>Something broke</html>  \n"),
			status: 500,
			want:   "<html>Something broke</html>",
		},
		{
			name:   "envelope without message falls back to raw",
			body:   []byte(`{"error":{"code":400}}`),
			status: 400,
			want:   `{"error":{"code":400}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := api.APIErrorMessage(tc.body, tc.status)
			if got != tc.want {
				t.Errorf("APIErrorMessage = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestError_implementsCoder asserts that *Error carries an ExitCode so
// internal/exit.For routes it without a dispatcher.
func TestError_implementsCoder(t *testing.T) {
	e := &api.Error{Operation: "edits.insert", Package: "com.x", StatusCode: 403, Message: "no"}
	var coder interface{ ExitCode() int }
	if !errors.As(e, &coder) {
		t.Fatalf("*api.Error does not implement ExitCode()")
	}
	if coder.ExitCode() != 11 {
		t.Errorf("ExitCode() = %d, want 11", coder.ExitCode())
	}
}

// TestError_Unwrap exposes the underlying transport error to errors.Is /
// errors.As callers.
func TestError_Unwrap(t *testing.T) {
	cause := errors.New("dial tcp: connection refused")
	e := &api.Error{Operation: "edits.insert", Package: "com.x", Message: cause.Error(), Cause: cause}
	if !errors.Is(e, cause) {
		t.Errorf("errors.Is(e, cause) = false, want true")
	}
}
