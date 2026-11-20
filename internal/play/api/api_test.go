package api_test

import (
	"errors"
	"fmt"
	"strings"
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
		{"401 unauthorized → authentication (token refused by the API, #598)", 401, 10},
		{"403 forbidden → authorization", 403, 11},
		{"404 not found → generic 4xx", 404, 30},
		{"400 bad request → generic 4xx", 400, 30},
		{"409 conflict → state conflict", 409, 60},
		{"429 too many requests → state conflict (rate-limited)", 429, 60},
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
		{
			name:   "whitespace-only body falls back to HTTP <status>",
			body:   []byte("   \n\t  "),
			status: 502,
			want:   "HTTP 502",
		},
		{
			name:   "envelope with empty message and whitespace-only outer body still has a floor",
			body:   []byte(`{"error":{"message":""}}`),
			status: 500,
			want:   `{"error":{"message":""}}`,
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

func TestError_ExitCode(t *testing.T) {
	cases := []struct {
		name      string
		operation string
		status    int
		want      int
	}{
		{"bundles.upload 400 → malformed-AAB exit 20", "bundles.upload", 400, 20},
		{"bundles.upload 404 → malformed-AAB exit 20", "bundles.upload", 404, 20},
		{"bundles.upload 403 → auth still wins (11)", "bundles.upload", 403, 11},
		{"bundles.upload 500 → 5xx still wins (40)", "bundles.upload", 500, 40},
		{"bundles.upload 0 → transport still wins (50)", "bundles.upload", 0, 50},
		{"bundles.upload 409 → conflict still wins (60)", "bundles.upload", 409, 60},
		{"bundles.upload 429 → rate-limited (60)", "bundles.upload", 429, 60},
		{"apks.upload 400 → malformed-APK exit 20", "apks.upload", 400, 20},
		{"apks.upload 404 → malformed-APK exit 20", "apks.upload", 404, 20},
		{"apks.upload 403 → auth still wins (11)", "apks.upload", 403, 11},
		{"apks.upload 500 → 5xx still wins (40)", "apks.upload", 500, 40},
		{"edits.insert 400 → generic 4xx (30)", "edits.insert", 400, 30},
		{"edits.insert 404 → generic 4xx (30)", "edits.insert", 404, 30},
		{"edits.insert 429 → rate-limited (60)", "edits.insert", 429, 60},
		{"tracks.update 400 → generic 4xx (30)", "tracks.update", 400, 30},
		{"tracks.update 429 → rate-limited (60)", "tracks.update", 429, 60},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &api.Error{Operation: tc.operation, Package: "com.x", StatusCode: tc.status, Message: "x"}
			if got := e.ExitCode(); got != tc.want {
				t.Errorf("(&Error{Operation:%q,StatusCode:%d}).ExitCode() = %d, want %d",
					tc.operation, tc.status, got, tc.want)
			}
		})
	}
}

type codedErr struct{ code int }

func (c codedErr) Error() string { return "coded" }
func (c codedErr) ExitCode() int { return c.code }

func TestError_ExitCode_transportCauseCoderWins(t *testing.T) {
	cases := []struct {
		name   string
		status int
		cause  error
		want   int
	}{
		{"wrapped auth refusal -> 10", 0, fmt.Errorf("Post: %w", codedErr{10}), 10},
		{"plain transport cause -> 50", 0, errors.New("dial tcp: connection refused"), 50},
		{"no cause -> 50", 0, nil, 50},
		{"HTTP status wins over cause", 503, codedErr{10}, 40},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &api.Error{Operation: "edits.insert", Package: "com.x", StatusCode: tc.status, Message: "x", Cause: tc.cause}
			if got := e.ExitCode(); got != tc.want {
				t.Errorf("ExitCode() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestError_ExitCode_nilReceiver(t *testing.T) {
	var e *api.Error
	if got := e.ExitCode(); got != 0 {
		t.Errorf("(nil *Error).ExitCode() = %d, want 0", got)
	}
}

func TestError_Unwrap(t *testing.T) {
	cause := errors.New("dial tcp: connection refused")
	e := &api.Error{Operation: "edits.insert", Package: "com.x", Message: cause.Error(), Cause: cause}
	if !errors.Is(e, cause) {
		t.Errorf("errors.Is(e, cause) = false, want true")
	}
}

func TestError_Error_includesReasons(t *testing.T) {
	e := &api.Error{
		Operation:  "edits.insert",
		Package:    "com.example.app",
		StatusCode: 400,
		Message:    "Edit ID is required.",
		Reasons:    []string{"editAlreadyExists"},
	}
	got := e.Error()
	if !strings.Contains(got, "editAlreadyExists") {
		t.Errorf("Error() should include reason; got %q", got)
	}
	if !strings.Contains(got, "HTTP 400") {
		t.Errorf("Error() should still include HTTP status; got %q", got)
	}
}

func TestError_Error_omitsReasonsWhenEmpty(t *testing.T) {
	e := &api.Error{Operation: "edits.insert", Package: "com.x", StatusCode: 403, Message: "no"}
	got := e.Error()
	if strings.Contains(got, "[reason:") {
		t.Errorf("Error() should not emit empty [reason: ] suffix; got %q", got)
	}
}
