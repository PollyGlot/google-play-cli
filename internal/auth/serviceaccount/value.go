package serviceaccount

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"unicode"
)

// LoadValue resolves a credential value exactly as the user supplied it (the
// --service-account flag or $GPLAY_SERVICE_ACCOUNT): inline JSON when its
// first non-space rune is '{', a filesystem path read through fr otherwise.
// source names where the value came from, for the error message.
//
// The value is NEVER echoed back. A value that is neither JSON nor a readable
// path is, far more often than a typo'd path, a credential in the wrong shape
// (base64, quoted JSON, a leading BOM), and os.ReadFile would quote all of it
// in its *PathError ("open <value>: file name too long"). That message then
// reaches stderr, the JSON error envelope and the doctor checklist, and a
// base64 blob matches none of the redaction patterns. So a failed read reports
// the length and the OS reason only.
func LoadValue(fr FileReader, source, value string) (*ServiceAccount, error) {
	if isInlineJSON(value) {
		return Parse([]byte(value))
	}
	data, err := fr.ReadFile(value)
	if err != nil {
		return nil, &UnreadableValueError{
			Source: source,
			Len:    len(value),
			Reason: pathErrorReason(err),
			Hint:   shapeHint(value),
		}
	}
	return Parse(data)
}

// isInlineJSON reports whether value should be treated as inline JSON rather
// than a filesystem path. The rule (docs/DESIGN.md §1): skip leading
// whitespace; the value is JSON when the first remaining rune is '{'.
func isInlineJSON(value string) bool {
	return firstRune(value) == '{'
}

// UnreadableValueError is returned when a credential value is neither inline
// JSON nor a readable file. It carries a description of the value, never the
// value itself: see LoadValue for why.
type UnreadableValueError struct {
	// Source is the flag or env var the value came from.
	Source string
	// Len is the value's length in bytes: enough to tell a path from a blob.
	Len int
	// Reason is the OS-level cause without the path ("no such file or
	// directory", "file name too long"), empty when unknown.
	Reason string
	// Hint names the likely shape mistake, or the generic expectation.
	Hint string
}

func (e *UnreadableValueError) Error() string {
	msg := fmt.Sprintf("%s is neither inline JSON nor a readable file path (%d bytes, value not shown)", e.Source, e.Len)
	if e.Reason != "" {
		msg += ": " + e.Reason
	}
	if e.Hint != "" {
		msg += "; " + e.Hint
	}
	return msg
}

// ExitCode satisfies exit.Coder: an unusable credential is an auth failure.
func (*UnreadableValueError) ExitCode() int { return 10 }

// pathErrorReason keeps the OS cause of a failed read and drops the path. Only
// a *fs.PathError is unwrapped, because its Err field is known not to hold the
// path; any other error shape may quote its input, so it yields no reason.
func pathErrorReason(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) && pe.Err != nil {
		return pe.Err.Error()
	}
	return ""
}

// byteOrderMark is U+FEFF, which unicode.IsSpace does not count as space, so a
// BOM-prefixed JSON value falls through to the path branch.
const byteOrderMark = rune(0xFEFF)

// shapeHint recognises the three shapes a pasted credential takes in CI secret
// stores and YAML (a leading BOM, surrounding quotes, base64) and says how to
// fix each. They are diagnosed, not accepted: the detection rule stays the one
// documented in docs/DESIGN.md §1.
func shapeHint(value string) string {
	trimmed := strings.TrimLeftFunc(value, unicode.IsSpace)
	switch {
	case strings.HasPrefix(trimmed, string(byteOrderMark)) && isInlineJSON(strings.TrimPrefix(trimmed, string(byteOrderMark))):
		return "it looks like JSON prefixed with a UTF-8 byte-order mark; remove the BOM"
	case (strings.HasPrefix(trimmed, `"`) || strings.HasPrefix(trimmed, `'`)) && isInlineJSON(trimmed[1:]):
		return "it looks like JSON wrapped in quotes; pass the raw JSON without the surrounding quotes"
	case looksBase64JSON(trimmed):
		return "it looks base64-encoded, which is not supported; decode it first"
	default:
		return "pass the service-account JSON itself (starting with '{') or the path to its file"
	}
}

// looksBase64JSON reports whether s decodes, as standard base64 with or
// without padding, to text starting with '{'.
func looksBase64JSON(s string) bool {
	s = strings.TrimRightFunc(s, unicode.IsSpace)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
		if b, err := enc.DecodeString(s); err == nil && isInlineJSON(string(b)) {
			return true
		}
	}
	return false
}

// firstRune returns the first non-space rune of s, or 0 when there is none.
func firstRune(s string) rune {
	for _, r := range s {
		if !unicode.IsSpace(r) {
			return r
		}
	}
	return 0
}
