// Package redact masks credential material on its way out of gplay.
//
// gplay holds three kinds of secret: the PEM private key of a service account,
// the JSON fields that carry it (private_key, private_key_id), and the OAuth2
// bearer token minted from it. Any of those can reach stderr by accident: a
// JSON parse error quoting the offending bytes, a verbose transport line
// dumping request headers, a wrapped library error echoing its input. A CI log
// is then pasted into an issue, and the credential is gone.
//
// The defence is a single filter at the stderr boundary (PRD #459 / slice
// #460) rather than a rule every future log line must remember. Wrap an
// io.Writer with Writer and everything written through it is masked. Stdout is
// deliberately NOT wrapped: it mirrors API responses verbatim (ADR-0003), and
// the API never hands back gplay's own credentials.
//
// The filter is always on: there is no flag to disable it.
package redact

import (
	"io"
	"regexp"
)

// Mask is what replaces every secret match. It is deliberately not a run of
// asterisks sized to the input: the length of a secret is itself a hint.
const Mask = "[REDACTED]"

// The patterns are ordered from most to least specific, and each is applied to
// the whole buffer in turn.
//
// Deliberately NOT anchored to a line: a PEM block spans many lines, and error
// wrapping often folds one onto a single line. `(?s)` lets `.` cross newlines
// so both shapes are caught.
var patterns = []*regexp.Regexp{
	// A PEM block, whatever its label (PRIVATE KEY, RSA PRIVATE KEY, ...), with
	// the body masked but the delimiters kept: "a PEM block was here" is the
	// diagnostic the user needs, its bytes are not. Non-greedy so two blocks in
	// one buffer do not collapse into one match. \\n covers the escaped form the
	// key carries inside service-account JSON.
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----(?:.|\\n)*?-----END [A-Z ]*PRIVATE KEY-----`),
	// The secret fields of a service-account JSON, matched as JSON so a dumped
	// credential file is masked field by field rather than wholesale. Value is
	// matched with escapes honoured (\" does not end the string), because a PEM
	// body embedded in JSON is full of \n and may contain quotes.
	regexp.MustCompile(`(?i)"(private_key|private_key_id|refresh_token|client_secret)"\s*:\s*"(?:[^"\\]|\\.)*"`),
	// An Authorization header, in the two shapes a Go dump produces
	// ("Authorization: Bearer x" and `"Authorization":["Bearer x"]`). The scheme
	// is kept so the line still says what kind of credential it was.
	regexp.MustCompile(`(?i)(authorization"?\s*:\s*\[?"?\s*)(bearer|basic|token)(\s+)[A-Za-z0-9\-._~+/=]+`),
	// A JWT, the shape both a service-account assertion and an OAuth2 ID token
	// take: three base64url segments separated by dots, the first two being JSON
	// objects so they always start with "ey". The length floor keeps ordinary
	// dotted identifiers (a package name, a version) from matching.
	regexp.MustCompile(`ey[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
	// A Google OAuth2 access token, which has no dots and so is invisible to the
	// JWT pattern: the "ya29." family, plus the refresh-token prefixes.
	regexp.MustCompile(`\b(?:ya29|1//|ATZAV)[A-Za-z0-9\-._~+/]{10,}={0,2}`),
	// The generic "token"/"secret"/"password" JSON field or key=value, last
	// because it is the broadest. Only non-empty values are masked, so an
	// empty-string field still reads as empty (a useful diagnostic).
	regexp.MustCompile(`(?i)("(?:access_token|id_token|refresh_token|api_key|password|secret)"\s*:\s*")(?:[^"\\]|\\.)+"`),
}

// String returns s with every credential shape masked.
func String(s string) string {
	if s == "" {
		return s
	}
	for i, re := range patterns {
		switch i {
		case 1:
			// Keep the field name so the error still says WHICH field was bad.
			s = re.ReplaceAllString(s, `"$1": "`+Mask+`"`)
		case 2:
			// Keep the "Authorization: Bearer " prefix, mask only the credential.
			s = re.ReplaceAllString(s, `${1}${2}${3}`+Mask)
		case 5:
			s = re.ReplaceAllString(s, `${1}`+Mask+`"`)
		default:
			s = re.ReplaceAllString(s, Mask)
		}
	}
	return s
}

// Bytes is String over a byte slice.
func Bytes(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	return []byte(String(string(b)))
}

// writer is an io.Writer that masks credential material before forwarding.
//
// It filters each Write call independently rather than buffering until a
// newline. That is a deliberate trade: a secret split across two Write calls
// would slip through, but a buffering filter would hold back a partial line
// (progress output, a prompt) that the user needs to see NOW, and would need a
// Close nobody would remember to call. In practice every gplay stderr write is
// one whole message: fmt.Fprintf/Fprintln with the full string.
type writer struct{ w io.Writer }

// Write masks p and forwards it. It reports len(p) as written, not the masked
// length: callers treat a short write as an error, and masking legitimately
// changes the byte count. A forwarding error is returned verbatim.
func (rw writer) Write(p []byte) (int, error) {
	masked := Bytes(p)
	if _, err := rw.w.Write(masked); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Writer wraps w so everything written through it is masked. A nil w yields
// io.Discard; wrapping an already-wrapped writer is a no-op, so the kernel and
// main can both wrap without stacking filters.
func Writer(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	if _, ok := w.(writer); ok {
		return w
	}
	return writer{w: w}
}
