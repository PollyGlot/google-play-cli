package exit_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func googleError(status int, message, reason string) string {
	return `{"error":{"code":` + itoa(status) + `,"message":"` + message +
		`","errors":[{"reason":"` + reason + `","message":"` + message + `"}]}}`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestClassify_apiFailures(t *testing.T) {
	tests := []struct {
		name          string
		err           *api.Error
		wantCode      exit.Code
		wantExit      int
		wantRetryable bool
	}{
		{
			name:     "409 editAlreadyExists is discriminated from a plain conflict",
			err:      &api.Error{Operation: "edits.insert", Package: "com.example.app", StatusCode: 409, Message: "edit already exists", Reasons: []string{"editAlreadyExists"}},
			wantCode: exit.CodeEditAlreadyExists,
			wantExit: 60,
		},
		{
			name:     "409 without a known reason falls back to the conflict bucket",
			err:      &api.Error{Operation: "edits.commit", Package: "com.example.app", StatusCode: 409, Message: "conflict"},
			wantCode: exit.CodeStateConflict,
			wantExit: 60,
		},
		{
			name:          "429 is rate-limited and retryable even with no reason",
			err:           &api.Error{Operation: "edits.insert", Package: "com.example.app", StatusCode: 429, Message: "too many requests"},
			wantCode:      exit.CodeRateLimitExceeded,
			wantExit:      60,
			wantRetryable: true,
		},
		{
			name:          "quotaExceeded on a 403 still reads as rate-limited",
			err:           &api.Error{Operation: "edits.insert", Package: "com.example.app", StatusCode: 403, Message: "quota", Reasons: []string{"quotaExceeded"}},
			wantCode:      exit.CodeRateLimitExceeded,
			wantExit:      11,
			wantRetryable: true,
		},
		{
			name:     "401 from the API is an authentication failure (token refused), exit 10",
			err:      &api.Error{Operation: "tracks.list", Package: "com.example.app", StatusCode: 401, Message: "Request had invalid authentication credentials."},
			wantCode: exit.CodeAuthFailed,
			wantExit: 10,
		},
		{
			name:     "403 is a permission denial, not retryable",
			err:      &api.Error{Operation: "edits.insert", Package: "com.example.app", StatusCode: 403, Message: "caller does not have permission"},
			wantCode: exit.CodePermissionDenied,
			wantExit: 11,
		},
		{
			name:     "404 refines the generic 4xx bucket",
			err:      &api.Error{Operation: "tracks.get", Package: "com.example.app", StatusCode: 404, Message: "not found"},
			wantCode: exit.CodeNotFound,
			wantExit: 30,
		},
		{
			name:     "400 refines the generic 4xx bucket",
			err:      &api.Error{Operation: "tracks.update", Package: "com.example.app", StatusCode: 400, Message: "bad request"},
			wantCode: exit.CodeInvalidArgument,
			wantExit: 30,
		},
		{
			name:     "a malformed artifact stays a client-side validation failure, not NOT_FOUND",
			err:      &api.Error{Operation: "bundles.upload", Package: "com.example.app", StatusCode: 404, Message: "app requires an AAB"},
			wantCode: exit.CodeValidationFailed,
			wantExit: 20,
		},
		{
			name:          "5xx is upstream unavailability, retryable",
			err:           &api.Error{Operation: "edits.commit", Package: "com.example.app", StatusCode: 503, Message: "backend error"},
			wantCode:      exit.CodeUpstreamUnavailable,
			wantExit:      40,
			wantRetryable: true,
		},
		{
			name:          "a transport failure (no HTTP response) is a network error, retryable",
			err:           &api.Error{Operation: "edits.insert", Package: "com.example.app", StatusCode: 0, Message: "dial tcp: i/o timeout"},
			wantCode:      exit.CodeNetworkError,
			wantExit:      50,
			wantRetryable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := exit.Classify(tt.err)
			if d.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", d.Code, tt.wantCode)
			}
			if d.ExitCode != tt.wantExit {
				t.Errorf("exitCode = %d, want %d", d.ExitCode, tt.wantExit)
			}
			if d.Retryable != tt.wantRetryable {
				t.Errorf("retryable = %v, want %v", d.Retryable, tt.wantRetryable)
			}
			if d.Operation != tt.err.Operation {
				t.Errorf("operation = %q, want %q", d.Operation, tt.err.Operation)
			}
			if d.Package != tt.err.Package {
				t.Errorf("package = %q, want %q", d.Package, tt.err.Package)
			}
			if d.Message == "" {
				t.Error("message must carry the human string")
			}
		})
	}
}

func TestClassify_localFailures(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode exit.Code
		wantExit int
	}{
		{"usage", exit.Usagef("unknown column %q", "bogus"), exit.CodeUsageError, 2},
		{"safety flag", exit.SafetyFlag("confirm", "destructive; pass --confirm"), exit.CodeSafetyFlagRequired, 3},
		{"read-only policy", exit.Policyf("refused by GPLAY_READONLY"), exit.CodePolicyReadonly, 4},
		{"untyped", errors.New("something went sideways"), exit.CodeGenericError, 1},
		{"wrapped usage keeps its code", wrap(exit.Usagef("missing --to")), exit.CodeUsageError, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := exit.Classify(tt.err)
			if d.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", d.Code, tt.wantCode)
			}
			if d.ExitCode != tt.wantExit {
				t.Errorf("exitCode = %d, want %d", d.ExitCode, tt.wantExit)
			}
			if d.Retryable {
				t.Error("a local failure is never blindly retryable")
			}
			if d.Operation != "" || d.Package != "" {
				t.Errorf("a local failure made no API call; got operation=%q package=%q", d.Operation, d.Package)
			}
		})
	}
}

func wrap(err error) error { return errWrapper{err} }

type errWrapper struct{ error }

func (w errWrapper) Unwrap() error { return w.error }

type diagnoserError struct{ code exit.Code }

func (diagnoserError) Error() string               { return "boom" }
func (diagnoserError) ExitCode() int               { return 60 }
func (e diagnoserError) DiagnosticCode() exit.Code { return e.code }

func TestClassify_diagnoserRefinementWins(t *testing.T) {
	d := exit.Classify(diagnoserError{code: exit.CodeEditExpired})
	if d.Code != exit.CodeEditExpired {
		t.Errorf("code = %q, want the error's own refinement %q", d.Code, exit.CodeEditExpired)
	}
	if d.ExitCode != 60 {
		t.Errorf("exitCode = %d, want 60 (the refinement does not move the exit code)", d.ExitCode)
	}
}

func TestCodeCatalog_notDraft(t *testing.T) {
	for _, code := range []exit.Code{exit.CodeBasePlanNotDraft, exit.CodeOfferNotDraft} {
		doc, ok := exit.LookupCode(code)
		if !ok {
			t.Fatalf("%s missing from the catalog", code)
		}
		if doc.ExitCode != 30 || doc.Retryable {
			t.Errorf("row = %+v, want exit 30 and not retryable", doc)
		}
		if !strings.Contains(doc.Meaning, "INACTIVE") {
			t.Errorf("%s meaning %q must name the keep-as-INACTIVE remedy", code, doc.Meaning)
		}
	}
}

func TestClassify_nilIsNotAFailure(t *testing.T) {
	d := exit.Classify(nil)
	if d.Code != "" || d.ExitCode != 0 {
		t.Errorf("Classify(nil) = %+v, want the zero Diagnostic", d)
	}
}

func TestClassify_neverReturnsAnEmptyCode(t *testing.T) {
	var errs []error
	for _, doc := range exit.Catalog() {
		if doc.Code == 0 {
			continue
		}
		errs = append(errs, fixedCodeError{doc.Code})
	}
	for _, status := range []int{0, 400, 401, 403, 404, 409, 410, 418, 429, 500, 503} {
		errs = append(errs, &api.Error{Operation: "edits.insert", Package: "com.example.app", StatusCode: status, Message: "x"})
	}
	errs = append(errs, errors.New("untyped"))

	for _, err := range errs {
		d := exit.Classify(err)
		if d.Code == "" {
			t.Errorf("Classify(%v) returned an empty code", err)
			continue
		}
		if _, ok := exit.LookupCode(d.Code); !ok {
			t.Errorf("Classify(%v) returned %q, which is not in the catalog", err, d.Code)
		}
	}
}

type fixedCodeError struct{ code int }

func (e fixedCodeError) Error() string { return "exit " + itoa(e.code) }
func (e fixedCodeError) ExitCode() int { return e.code }

func TestCodeCatalog_isAWellFormedRegistry(t *testing.T) {
	validExit := map[int]bool{}
	for _, d := range exit.Catalog() {
		validExit[d.Code] = true
	}

	seen := map[exit.Code]bool{}
	covered := map[int]bool{}
	for _, d := range exit.CodeCatalog() {
		if d.Code == "" {
			t.Error("catalog carries an unnamed code")
			continue
		}
		if seen[d.Code] {
			t.Errorf("code %q appears twice in the catalog", d.Code)
		}
		seen[d.Code] = true
		if d.Meaning == "" {
			t.Errorf("code %q has no meaning; the catalog is the documentation", d.Code)
		}
		if !validExit[d.ExitCode] {
			t.Errorf("code %q maps to exit %d, which is not in docs/DESIGN.md §9", d.Code, d.ExitCode)
		}
		if d.Code != exit.Code(upper(string(d.Code))) {
			t.Errorf("code %q is not SCREAMING_SNAKE", d.Code)
		}
		covered[d.ExitCode] = true
	}

	for _, d := range exit.Catalog() {
		if d.Code == 0 {
			continue
		}
		if !covered[d.Code] {
			t.Errorf("exit code %d has no diagnostic code; an agent would see a hole", d.Code)
		}
	}
}

func upper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}

func TestClassify_throughRealCallPath(t *testing.T) {
	hc := &http.Client{Transport: testkit.NewFake(testkit.Any(http.StatusConflict,
		googleError(http.StatusConflict, "Edit ID is required", "editAlreadyExists")))}
	_, err := edits.OpenExplicit(context.Background(), hc, "com.example.app")
	if err == nil {
		t.Fatal("expected the mocked 409 to fail the insert")
	}
	d := exit.Classify(err)
	if d.Code != exit.CodeEditAlreadyExists {
		t.Errorf("code = %q, want %q", d.Code, exit.CodeEditAlreadyExists)
	}
	if len(d.Reasons) != 1 || d.Reasons[0] != "editAlreadyExists" {
		t.Errorf("reasons = %v, want the verbatim upstream [editAlreadyExists]", d.Reasons)
	}
	if d.Operation != "edits.insert" {
		t.Errorf("operation = %q, want edits.insert", d.Operation)
	}
	if d.Package != "com.example.app" {
		t.Errorf("package = %q, want com.example.app", d.Package)
	}
	if d.ExitCode != exit.For(err) {
		t.Errorf("diagnostic exitCode %d disagrees with exit.For %d", d.ExitCode, exit.For(err))
	}
}
