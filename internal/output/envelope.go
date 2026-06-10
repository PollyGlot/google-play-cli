package output

import (
	"errors"
	"io"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// ErrorEnvelope is the machine-readable failure payload gplay writes to stdout
// when a command fails under `--output json`. Under any human format (table /
// markdown) stdout stays empty on failure and the error goes to stderr only;
// the envelope exists so an AI agent or CI harness can branch on failure modes
// without scraping human-oriented stderr text. Its shape is part of the
// public contract (ADR-0010); see ADR-0023.
//
// All the data it carries already exists internally — the semantic exit code,
// the upstream API reasons[], the missing safety flag — it was simply never
// serialized before.
type ErrorEnvelope struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail is the body of an ErrorEnvelope.
//
//   - ExitCode mirrors the process exit code (the semantic taxonomy of
//     docs/DESIGN.md §9), so a consumer reading stdout sees the same code the
//     shell sees.
//   - Message is the human-readable error string (the same text stderr shows).
//   - Reasons carries the upstream Google API error.errors[].reason values
//     (e.g. "editAlreadyExists", "rateLimitExceeded") when an API envelope was
//     parsed — the discriminating signal behind a shared HTTP status. Omitted
//     when absent.
//   - Requires names the missing safety-acknowledgment flag on an exit-3
//     refusal (--confirm / --grant-admin). A safety refusal carries exactly one
//     flag today, emitted as a single-element list to match the list shape of
//     the ADR-0017 dry-run `requires` preview (carried to failure time so
//     exit-3 recovery stays deterministic). Omitted when the failure is not a
//     safety refusal.
type ErrorDetail struct {
	ExitCode int      `json:"exitCode"`
	Message  string   `json:"message"`
	Reasons  []string `json:"reasons,omitempty"`
	Requires []string `json:"requires,omitempty"`
}

// WriteErrorEnvelope serializes err as a single ErrorEnvelope to w using the
// gplay-wide JSON shape (2-space indent, trailing newline). It extracts the
// structured fields from the error chain: exit.For for the code, *api.Error for
// reasons[], and *exit.SafetyFlagError for requires[]. Callers gate this on the
// resolved Format being FormatJSON; it does not check the format itself.
func WriteErrorEnvelope(w io.Writer, err error) error {
	d := ErrorDetail{
		ExitCode: exit.For(err),
		Message:  err.Error(),
	}
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		d.Reasons = apiErr.Reasons
	}
	var safety *exit.SafetyFlagError
	if errors.As(err, &safety) && safety.Flag != "" {
		d.Requires = []string{safety.Flag}
	}
	return WriteJSON(w, ErrorEnvelope{Error: d})
}
