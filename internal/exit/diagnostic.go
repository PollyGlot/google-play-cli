// diagnostic.go: the machine-readable diagnostic-code layer that sits on top of
// the exit-code taxonomy (docs/DESIGN.md §9). An exit code says which bucket a
// failure fell into; a diagnostic code says which failure it was, so an agent
// can branch on EDIT_ALREADY_EXISTS instead of regexing the human message for
// the word "already". See ADR-0044.
//
// Classification is TOTAL: every non-nil error yields a code, derived from the
// exit code it already carries plus, on an upstream failure, the API status and
// the verbatim Google `error.errors[].reason` values. There is therefore no
// "unclassified error path" to police per error type: adding a new typed error
// anywhere in the tree inherits a code from its ExitCode by construction.
package exit

import (
	"errors"
	"net/http"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// Code is a stable, SCREAMING_SNAKE diagnostic identifier. Codes are part of
// the frozen Public contract (ADR-0010/0042) and APPEND-ONLY: a new failure
// mode earns a new code, an existing one is never renamed or repurposed, so a
// consumer's dispatch table keeps working across upgrades.
type Code string

// The frozen code vocabulary. Local failures (the first block) mirror the
// exit-code taxonomy one-for-one; upstream failures (the second) refine the
// coarse HTTP buckets with the discriminants agents actually branch on.
const (
	// CodeGenericError is the fallback when an error carries no exit code of
	// its own (exit 1). It is a code, not a hole: a consumer still gets a
	// stable token to log and alert on.
	CodeGenericError Code = "GENERIC_ERROR"
	// CodeUsageError is CLI misuse: unknown flag, bad value, wrong arity.
	CodeUsageError Code = "USAGE_ERROR"
	// CodeSafetyFlagRequired is the deterministically resolvable refusal: the
	// envelope's `requires` names the flag to re-run with (ADR-0017).
	CodeSafetyFlagRequired Code = "SAFETY_FLAG_REQUIRED"
	// CodePolicyReadonly is a refusal by environment policy (GPLAY_READONLY);
	// unlike SAFETY_FLAG_REQUIRED it is NOT resolvable by adding a flag.
	CodePolicyReadonly Code = "POLICY_READONLY"
	// CodeAuthFailed is an authentication failure: no Account, invalid service
	// account, token refused, missing scope.
	CodeAuthFailed Code = "AUTH_FAILED"
	// CodeValidationFailed is a client-side rejection before or instead of the
	// API call: malformed artifact, unknown locale, invalid catalog file.
	CodeValidationFailed Code = "VALIDATION_FAILED"

	// CodePermissionDenied is an upstream 403: the Account is authenticated but
	// not invited on the app or the Developer account.
	CodePermissionDenied Code = "PERMISSION_DENIED"
	// CodeInvalidArgument is an upstream 400 the client sent badly.
	CodeInvalidArgument Code = "INVALID_ARGUMENT"
	// CodeNotFound is an upstream 404: the package, track or Edit is not there.
	CodeNotFound Code = "NOT_FOUND"
	// CodeAPIError is the residual upstream 4xx bucket.
	CodeAPIError Code = "API_ERROR"
	// CodeUpstreamUnavailable is an upstream 5xx: Google is unhealthy, retry.
	CodeUpstreamUnavailable Code = "UPSTREAM_UNAVAILABLE"
	// CodeNetworkError is a transport failure with no HTTP response at all:
	// timeout, DNS, connection refused.
	CodeNetworkError Code = "NETWORK_ERROR"
	// CodeStateConflict is the residual 409 bucket: the remote state is not
	// what the call assumed (ambiguous target, concurrent change).
	CodeStateConflict Code = "STATE_CONFLICT"
	// CodeEditAlreadyExists is the `editAlreadyExists` reason: another Edit is
	// already open on the package, so this one cannot begin.
	CodeEditAlreadyExists Code = "EDIT_ALREADY_EXISTS"
	// CodeEditExpired is the `editExpired` reason: the pinned Edit aged out and
	// must be re-begun before the mutation is replayed.
	CodeEditExpired Code = "EDIT_EXPIRED"
	// CodeRateLimitExceeded is a 429 or a quota/rate reason: back off and retry.
	CodeRateLimitExceeded Code = "RATE_LIMIT_EXCEEDED"
)

// CodeDoc is one row of the diagnostic-code catalog, surfaced by
// `gplay help exit-codes` and `gplay schema --codes`.
type CodeDoc struct {
	Code Code `json:"code"`
	// ExitCode is the exit code this failure CANONICALLY exits with. Several
	// codes share one exit code on purpose: that is the whole point of the
	// layer (409 conflict vs editAlreadyExists both exit 60). It is
	// documentation, not a promise of equality: a handful of wrapped errors
	// keep a narrower exit code than the bucket (a 400+editAlreadyExists exits
	// 30, not 60), and the envelope's own exitCode field is always the
	// authoritative one for that failure.
	ExitCode int `json:"exitCode"`
	// Retryable reports whether replaying the same command unchanged can
	// plausibly succeed. It is a property of the code, so retry logic needs no
	// cause-specific table.
	Retryable bool   `json:"retryable"`
	Meaning   string `json:"meaning"`
}

// codeCatalog is the single source of truth for the vocabulary: the classifier
// reads Retryable from it, the CLI prints it, and the completeness test walks
// it. Ordered by exit code, then by specificity within a bucket.
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
	{CodeAPIError, 30, false, "Other API 4xx rejection"},
	{CodeUpstreamUnavailable, 40, true, "The API is temporarily unhealthy (5xx); retry"},
	{CodeNetworkError, 50, true, "Network failure with no HTTP response: timeout, DNS, refused"},
	{CodeStateConflict, 60, false, "Remote state conflicts with the request (409)"},
	{CodeEditAlreadyExists, 60, false, "An Edit is already open on this package; commit or delete it first"},
	{CodeEditExpired, 60, false, "The pinned Edit expired; begin a new Edit and replay the mutation"},
	{CodeRateLimitExceeded, 60, true, "Rate or quota limit exceeded; back off and retry"},
}

// CodeCatalog returns the documented diagnostic-code vocabulary in catalog
// order. It is the introspection surface behind `gplay help exit-codes` and
// `gplay schema --codes`, so a skill author never reads source to stay in sync.
func CodeCatalog() []CodeDoc {
	out := make([]CodeDoc, len(codeCatalog))
	copy(out, codeCatalog)
	return out
}

// LookupCode returns the catalog row for c.
func LookupCode(c Code) (CodeDoc, bool) {
	for _, d := range codeCatalog {
		if d.Code == c {
			return d, true
		}
	}
	return CodeDoc{}, false
}

// Diagnoser is the opt-in refinement contract, the diagnostic-code sibling of
// Coder: an error that knows a more precise code than its exit code implies
// declares it next to its own definition, and no dispatcher in this package
// grows a case for it.
type Diagnoser interface {
	DiagnosticCode() Code
}

// Diagnostic is the classified view of a failure: everything a machine
// consumer needs to branch, in one struct. It is what internal/output
// serializes into the JSON error envelope (ADR-0023).
type Diagnostic struct {
	Code      Code
	ExitCode  int
	Retryable bool
	Operation string   // API operation, e.g. "edits.commit"; empty on a local failure
	Package   string   // targeted package; empty when the failure is not package-scoped
	Reasons   []string // verbatim upstream error.errors[].reason values
	Message   string   // the same human string stderr carries
}

// Classify is the one classifier: it maps any error to its Diagnostic. A nil
// error yields the zero value (no code, exit 0). It never returns an empty
// Code for a non-nil error.
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
		d.Package = apiErr.Package
		d.Reasons = apiErr.Reasons
	}
	return d
}

// classifyCode resolves the code in three passes, most specific first: an
// explicit Diagnoser, then the upstream API refinement, then the exit-code
// table. Local typed errors (UsageError, SafetyFlagError, PolicyError, the
// ~40 per-command validation errors) deliberately get NO branch here: they
// already declare an exit code, and the table maps it, so a new one is
// classified the day it is written rather than the day someone remembers
// to extend a dispatcher.
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

// reasonCodes maps the Google `error.errors[].reason` values gplay promotes to
// a dedicated code. Keys are lowercased for a case-insensitive lookup. The map
// is deliberately small: a reason earns a code when a consumer would act
// differently on it, otherwise the status bucket is enough and the raw reason
// still travels verbatim in the envelope's `reasons`.
var reasonCodes = map[string]Code{
	"editalreadyexists":     CodeEditAlreadyExists,
	"editexpired":           CodeEditExpired,
	"ratelimitexceeded":     CodeRateLimitExceeded,
	"userratelimitexceeded": CodeRateLimitExceeded,
	"quotaexceeded":         CodeRateLimitExceeded,
}

// classifyAPI refines an upstream failure: a known reason wins (it is Google's
// own discriminant), then the HTTP statuses whose meaning the coarse exit
// bucket loses, then the exit-code table.
func classifyAPI(e *api.Error, exitCode int) Code {
	for _, r := range e.Reasons {
		if c, ok := reasonCodes[strings.ToLower(strings.TrimSpace(r))]; ok {
			return c
		}
	}
	// The 400/404 refinements are guarded on exit 30 because the artifact
	// uploads remap those same statuses to exit 20 ("malformed artifact",
	// api.Error.ExitCode): there, VALIDATION_FAILED is the truthful code and
	// NOT_FOUND would actively mislead.
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

// codeForExit is the total fallback from the exit-code taxonomy to a code. Any
// exit code without a row (which the catalog test forbids) degrades to
// GENERIC_ERROR rather than to an empty string: a consumer always gets a token.
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
	default:
		return CodeGenericError
	}
}
