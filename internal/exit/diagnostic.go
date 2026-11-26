package exit

import (
	"errors"
	"net/http"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

type Code string

const (
	CodeGenericError       Code = "GENERIC_ERROR"
	CodeUsageError         Code = "USAGE_ERROR"
	CodeSafetyFlagRequired Code = "SAFETY_FLAG_REQUIRED"
	CodePolicyReadonly     Code = "POLICY_READONLY"
	CodeAuthFailed         Code = "AUTH_FAILED"
	CodeValidationFailed   Code = "VALIDATION_FAILED"

	CodePermissionDenied     Code = "PERMISSION_DENIED"
	CodeInvalidArgument      Code = "INVALID_ARGUMENT"
	CodeNotFound             Code = "NOT_FOUND"
	CodeBasePlanNotDraft     Code = "BASE_PLAN_NOT_DRAFT"
	CodeOfferNotDraft        Code = "OFFER_NOT_DRAFT"
	CodeAPIError             Code = "API_ERROR"
	CodeUpstreamUnavailable  Code = "UPSTREAM_UNAVAILABLE"
	CodeNetworkError         Code = "NETWORK_ERROR"
	CodeCommitOutcomeUnknown Code = "COMMIT_OUTCOME_UNKNOWN"
	CodeStateConflict        Code = "STATE_CONFLICT"
	CodeEditAlreadyExists    Code = "EDIT_ALREADY_EXISTS"
	CodeEditExpired          Code = "EDIT_EXPIRED"
	CodeRateLimitExceeded    Code = "RATE_LIMIT_EXCEEDED"

	CodeFindingsPresent Code = "FINDINGS_PRESENT"
)

type CodeDoc struct {
	Code      Code   `json:"code"`
	ExitCode  int    `json:"exitCode"`
	Retryable bool   `json:"retryable"`
	Meaning   string `json:"meaning"`
}

var codeCatalog = []CodeDoc{
	{CodeGenericError, 1, false, "Unclassified failure (no typed exit code); consult the message"},
	{CodeUsageError, 2, false, "CLI misuse: unknown flag, bad value, wrong number of positional args"},
	{CodeSafetyFlagRequired, 3, false, "A named safety flag is missing; re-run with the flag in `requires`"},
	{CodePolicyReadonly, 4, false, "Refused by the read-only environment policy; not resolvable by a flag"},
	{CodeAuthFailed, 10, false, "Authentication failed: no Account, invalid credential, token refused"},
	{CodePermissionDenied, 11, false, "Authorization failed (403): the Account is not invited on this app"},
	{CodeValidationFailed, 20, false, "Client-side validation rejected the input before the API accepted it"},
	{CodeInvalidArgument, 30, false, "The API rejected the request as malformed (400)"},
	{CodeNotFound, 30, false, "The API found no such package, track, Edit or resource (404)"},
	{CodeBasePlanNotDraft, 30, false, "The API refused to delete a base plan that is not DRAFT; keep it in the file with state: INACTIVE instead"},
	{CodeOfferNotDraft, 30, false, "The API refused to delete a subscription offer that is not DRAFT; keep it in the file with state: INACTIVE instead"},
	{CodeAPIError, 30, false, "Other API 4xx rejection, or a 2xx body gplay cannot decode"},
	{CodeUpstreamUnavailable, 40, true, "The API is temporarily unhealthy (5xx); retry"},
	{CodeNetworkError, 50, true, "Network failure with no HTTP response: timeout, DNS, refused"},
	{CodeCommitOutcomeUnknown, 50, false, "An Edit commit failed after it was sent (timeout, reset or 5xx, exit 50 or 40) and may be live; check the live state before re-running"},
	{CodeStateConflict, 60, false, "Remote state conflicts with the request (409)"},
	{CodeEditAlreadyExists, 60, false, "An Edit is already open on this package; commit or delete it first"},
	{CodeEditExpired, 60, false, "The pinned Edit expired; clear its pin with `gplay edits discard`, then begin a new Edit and replay the mutation"},
	{CodeRateLimitExceeded, 60, true, "Rate or quota limit exceeded; back off and retry"},
	{CodeFindingsPresent, 70, false, "A read-only check command completed and reported findings; not a failure"},
}

func CodeCatalog() []CodeDoc {
	out := make([]CodeDoc, len(codeCatalog))
	copy(out, codeCatalog)
	return out
}

func LookupCode(c Code) (CodeDoc, bool) {
	for _, d := range codeCatalog {
		if d.Code == c {
			return d, true
		}
	}
	return CodeDoc{}, false
}

type Diagnoser interface {
	DiagnosticCode() Code
}

type Diagnostic struct {
	Code      Code
	ExitCode  int
	Retryable bool
	Operation string
	Resource  api.Resource
	Package   string
	Reasons   []string
	Message   string
}

func Classify(err error) Diagnostic {
	if err == nil {
		return Diagnostic{}
	}
	d := Diagnostic{ExitCode: For(err), Message: err.Error()}
	d.Code = classifyCode(err, d.ExitCode)
	if doc, ok := LookupCode(d.Code); ok {
		d.Retryable = doc.Retryable
	}
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		d.Operation = apiErr.Operation
		d.Resource = apiErr.Target()
		if d.Resource.Kind == api.KindPackage {
			d.Package = d.Resource.ID
		}
		d.Reasons = apiErr.Reasons
	}
	return d
}

func classifyCode(err error, exitCode int) Code {
	var refined Diagnoser
	if errors.As(err, &refined) {
		if c := refined.DiagnosticCode(); c != "" {
			return c
		}
	}
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return classifyAPI(apiErr, exitCode)
	}
	return codeForExit(exitCode)
}

var reasonCodes = map[string]Code{
	"editalreadyexists":     CodeEditAlreadyExists,
	"editexpired":           CodeEditExpired,
	"ratelimitexceeded":     CodeRateLimitExceeded,
	"userratelimitexceeded": CodeRateLimitExceeded,
	"quotaexceeded":         CodeRateLimitExceeded,
}

func classifyAPI(e *api.Error, exitCode int) Code {
	for _, r := range e.Reasons {
		if c, ok := reasonCodes[strings.ToLower(strings.TrimSpace(r))]; ok {
			return c
		}
	}
	switch e.StatusCode {
	case http.StatusTooManyRequests:
		return CodeRateLimitExceeded
	case http.StatusBadRequest:
		if exitCode == 30 {
			return CodeInvalidArgument
		}
	case http.StatusNotFound:
		if exitCode == 30 {
			return CodeNotFound
		}
	}
	return codeForExit(exitCode)
}

func codeForExit(exitCode int) Code {
	switch exitCode {
	case 2:
		return CodeUsageError
	case 3:
		return CodeSafetyFlagRequired
	case 4:
		return CodePolicyReadonly
	case 10:
		return CodeAuthFailed
	case 11:
		return CodePermissionDenied
	case 20:
		return CodeValidationFailed
	case 30:
		return CodeAPIError
	case 40:
		return CodeUpstreamUnavailable
	case 50:
		return CodeNetworkError
	case 60:
		return CodeStateConflict
	case FindingsCode:
		return CodeFindingsPresent
	default:
		return CodeGenericError
	}
}
