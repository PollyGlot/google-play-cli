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

// secretKey is the key-name shape the two generic rules below key off. The
// free prefix is what makes `client_secret`, `access_token` and `my_password`
// all match without listing them one by one; the alternation stays a closed
// list of words that only ever name a credential. Bare `key` is deliberately
// NOT in it: `private_key` has its own rule, and "cache key" or "sort key"
// would otherwise blank real diagnostics.
const secretKey = `[a-z0-9_-]*(?:token|secret|password|passwd|pwd|api[_-]?key)`

// fieldKey is secretKey minus the bare word `token`, for the one rule that
// cannot afford it: an UNQUOTED `key: value`. Go's error convention is
// `pkg: message`, and `token` is one of gplay's packages; worse, the oauth2
// library we do not control renders "cannot fetch token: 401 Unauthorized".
// Masking there would blank the status code, the single most useful thing in an
// auth failure. A compound (`access_token:`) or a `secret`/`password` key never
// reads as a package prefix, so those stay in.
const fieldKey = `(?:[a-z0-9_-]*(?:secret|password|passwd|pwd|api[_-]?key)|[a-z0-9_-]+[_-]token)`

// rule is a pattern plus its replacement template. The template is what keeps
// the surrounding diagnostic readable: masking is not silencing, so a rule
// normally captures the key (or the auth scheme) and replaces only the value.
type rule struct {
	re   *regexp.Regexp
	repl string
}

// The rules are ordered from most to least specific, and each is applied to the
// whole buffer in turn.
//
// Deliberately NOT anchored to a line: a PEM block spans many lines, and error
// wrapping often folds one onto a single line. `(?s)` lets `.` cross newlines
// so both shapes are caught.
var patterns = []rule{
	// A PEM block, whatever its label (PRIVATE KEY, RSA PRIVATE KEY, ...), with
	// the body masked but the delimiters kept: "a PEM block was here" is the
	// diagnostic the user needs, its bytes are not. Non-greedy so two blocks in
	// one buffer do not collapse into one match. \\n covers the escaped form the
	// key carries inside service-account JSON.
	{
		re:   regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----(?:.|\\n)*?-----END [A-Z ]*PRIVATE KEY-----`),
		repl: Mask,
	},
	// The secret fields of a service-account JSON, matched as JSON so a dumped
	// credential file is masked field by field rather than wholesale. Value is
	// matched with escapes honoured (\" does not end the string), because a PEM
	// body embedded in JSON is full of \n and may contain quotes. Keep the field
	// name so the error still says WHICH field was bad.
	{
		re:   regexp.MustCompile(`(?i)"(private_key|private_key_id)"\s*:\s*"(?:[^"\\]|\\.)*"`),
		repl: `"${1}": "` + Mask + `"`,
	},
	// An Authorization header, in the two shapes a Go dump produces
	// ("Authorization: Bearer x" and `"Authorization":["Bearer x"]`). The scheme
	// is kept so the line still says what kind of credential it was.
	{
		re:   regexp.MustCompile(`(?i)(authorization"?\s*:\s*\[?"?\s*)(bearer|basic|token)(\s+)[A-Za-z0-9\-._~+/=]+`),
		repl: `${1}${2}${3}` + Mask,
	},
	// A JWT, the shape both a service-account assertion and an OAuth2 ID token
	// take: three base64url segments separated by dots, the first two being JSON
	// objects so they always start with "ey". The length floor keeps ordinary
	// dotted identifiers (a package name, a version) from matching.
	{
		re:   regexp.MustCompile(`ey[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
		repl: Mask,
	},
	// A Google OAuth2 access token, which has no dots and so is invisible to the
	// JWT pattern: the "ya29." family, plus the refresh-token prefixes.
	{
		re:   regexp.MustCompile(`\b(?:ya29|1//|ATZAV)[A-Za-z0-9\-._~+/]{10,}={0,2}`),
		repl: Mask,
	},
	// A credential carried by an auth scheme with no "Authorization" in front:
	// a proxy diagnostic, a curl-style line, a library error quoting only the
	// header VALUE. The 16-character floor is what keeps prose out ("Basic
	// authentication is not supported": the longest word that can follow a
	// scheme name here is 14 characters), and a real token or base64 blob is
	// always longer.
	{
		re:   regexp.MustCompile(`(?i)\b(bearer|basic)(\s+)[A-Za-z0-9\-._~+/]{16,}={0,2}`),
		repl: `${1}${2}` + Mask,
	},
	// The generic credential field with a QUOTED value: JSON (`"token":"x"`) and
	// the `key="x"` form alike. Only non-empty values are masked, so an
	// empty-string field still reads as empty (a useful diagnostic).
	{
		re:   regexp.MustCompile(`(?i)("?` + secretKey + `"?\s*[:=]\s*")(?:[^"\\]|\\.)+"`),
		repl: `${1}` + Mask + `"`,
	},
	// The same field with an UNQUOTED value in `key=value` form: a query string,
	// an env dump, a flag echoed back in an error. Nothing narrates with `=`, so
	// this one takes the full key list.
	{
		re:   regexp.MustCompile(`(?i)(` + secretKey + `"?\s*=\s*)[^\s"'` + "`" + `,;)\]}]+`),
		repl: `${1}` + Mask,
	},
	// And in `key: value` form (`password: hunter2`), on the narrower fieldKey:
	// see its comment for why bare `token:` is excluded. Last because it is the
	// broadest. The value stops at whitespace or at a delimiter that cannot be
	// part of a credential, so the rest of the line survives. It never re-matches
	// what the quoted rule produced: that leaves a `"` in value position, which
	// this character class excludes.
	{
		re:   regexp.MustCompile(`(?i)(` + fieldKey + `"?\s*:\s*)[^\s"'` + "`" + `,;)\]}]+`),
		repl: `${1}` + Mask,
	},
}

// String returns s with every credential shape masked.
func String(s string) string {
	if s == "" {
		return s
	}
	for _, r := range patterns {
		s = r.re.ReplaceAllString(s, r.repl)
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
