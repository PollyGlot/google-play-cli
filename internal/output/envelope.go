package output

import (
	"errors"
	"io"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

// ErrorEnvelope is the machine-readable failure payload gplay writes to stdout
// when a command fails under `--output json`. Under any human format (table /
// markdown) stdout stays empty on failure and the error goes to stderr only;
// the envelope exists so an AI agent or CI harness can branch on failure modes
// without scraping human-oriented stderr text. Its shape is part of the
// public contract (ADR-0010); see ADR-0023 and ADR-0044.
//
// All the data it carries already exists internally: the diagnostic code and
// its retryable bit (exit.Classify), the semantic exit code, the failing API
// operation and package, the upstream API reasons[], the missing safety flag.
type ErrorEnvelope struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail is the body of an ErrorEnvelope.
//
//   - Code is the stable SCREAMING_SNAKE diagnostic code (ADR-0044), the field
//     an agent branches on: it discriminates failures that share an exit code
//     (EDIT_ALREADY_EXISTS vs STATE_CONFLICT, both exit 60). Always present on
//     a failure; the vocabulary is append-only and introspectable via
//     `gplay help exit-codes` / `gplay schema --codes`.
//   - ExitCode mirrors the process exit code (the semantic taxonomy of
//     docs/DESIGN.md §9), so a consumer reading stdout sees the same code the
//     shell sees.
//   - Retryable reports whether replaying the same command unchanged can
//     plausibly succeed, so retry logic needs no per-cause table. Always
//     emitted, including when false: a missing field and "not retryable" must
//     not look alike.
//   - Operation names the API call that failed (e.g. "edits.commit"), and
//     Package the package it targeted. Both omitted on a local failure, which
//     is itself the signal that no call was made.
//   - Message is the human-readable error string (the same text stderr shows).
//   - Reasons carries the upstream Google API error.errors[].reason values
//     (e.g. "editAlreadyExists", "rateLimitExceeded") when an API envelope was
//     parsed: the discriminating signal behind a shared HTTP status, surfaced
//     verbatim so a consumer can branch on a reason gplay has not promoted to
//     a code. Omitted when absent.
//   - Requires names the missing safety-acknowledgment flag on an exit-3
//     refusal (--confirm / --grant-admin). A safety refusal carries exactly one
//     flag today, emitted as a single-element list to match the list shape of
//     the ADR-0017 dry-run `requires` preview (carried to failure time so
//     exit-3 recovery stays deterministic). Omitted when the failure is not a
//     safety refusal.
type ErrorDetail struct {
	Code      string   `json:"code"`
	ExitCode  int      `json:"exitCode"`
	Retryable bool     `json:"retryable"`
	Operation string   `json:"operation,omitempty"`
	Package   string   `json:"package,omitempty"`
	Message   string   `json:"message"`
	Reasons   []string `json:"reasons,omitempty"`
	Requires  []string `json:"requires,omitempty"`
}

// WriteErrorEnvelope serializes err as a single ErrorEnvelope to w using the
// gplay-wide JSON shape (2-space indent, trailing newline). Every classified
// field comes from exit.Classify, the single classifier, so this function holds
// no failure taxonomy of its own; only requires[] is read here, because it is a
// recovery hint about flags rather than a diagnosis. Callers gate this on the
// resolved Format being FormatJSON; it does not check the format itself.
func WriteErrorEnvelope(w io.Writer, err error) error {
	diag := exit.Classify(err)
	d := ErrorDetail{
		Code:      string(diag.Code),
		ExitCode:  diag.ExitCode,
		Retryable: diag.Retryable,
		Operation: diag.Operation,
		Package:   diag.Package,
		Message:   diag.Message,
		Reasons:   diag.Reasons,
	}
	var safety *exit.SafetyFlagError
	if errors.As(err, &safety) && safety.Flag != "" {
		d.Requires = []string{safety.Flag}
	}
	return WriteJSON(w, ErrorEnvelope{Error: d})
}
