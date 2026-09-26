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
// A value that is neither JSON nor a readable path is often a credential in the
// wrong shape (base64, quoted JSON, a leading BOM), and os.ReadFile would quote
// all of it in its *PathError ("open <value>: file name too long"). That
// message then reaches stderr, the JSON error envelope and the doctor
// checklist, and a base64 blob matches none of the redaction patterns. So a
// failed read reports the length and the OS reason, and echoes the value only
// when it cannot be a credential (see displayablePath): a typo'd path stays
// diagnosable, a misshapen key never shows.
func LoadValue(fr FileReader, source, value string) (*ServiceAccount, error) {
	if isInlineJSON(value) {
		return Parse([]byte(value))
	}
	data, err := fr.ReadFile(value)
	if err != nil {
		return nil, &UnreadableValueError{
			Source: source,
			Path:   displayablePath(value),
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
// JSON nor a readable file. It carries a description of the value, and the
// value itself only when it cannot be a credential: see LoadValue for why.
type UnreadableValueError struct {
	// Source is the flag or env var the value came from.
	Source string
	// Path is the value when displayablePath allows it, empty otherwise.
	Path string
	// Len is the value's length in bytes: enough to tell a path from a blob.
	Len int
	// Reason is the OS-level cause without the path ("no such file or
	// directory", "file name too long"), empty when unknown.
	Reason string
	// Hint names the likely shape mistake, or the generic expectation.
	Hint string
}

func (e *UnreadableValueError) Error() string {
	var msg string
	if e.Path != "" {
		msg = fmt.Sprintf("%s is neither inline JSON nor a readable file path (%q)", e.Source, e.Path)
	} else {
		msg = fmt.Sprintf("%s is neither inline JSON nor a readable file path (%d bytes, value not shown)", e.Source, e.Len)
	}
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

// maxDisplayablePath bounds the values shown back. A service account is over a
// kilobyte in any encoding, so a value this short cannot carry one, while a
// real path (NAME_MAX is 255 on common filesystems) fits.
const maxDisplayablePath = 255

// displayablePath returns value when it cannot be a credential, so a typo'd
// path is named in the error, and "" otherwise. Shown only if it is a single
// line of at most maxDisplayablePath bytes with no PEM marker, no '{' and not
// base64 of JSON: each condition alone rules out a service account or a piece
// of one.
func displayablePath(value string) string {
	if len(value) > maxDisplayablePath ||
		strings.ContainsAny(value, "\r\n{") ||
		strings.Contains(value, "-----BEGIN") || strings.Contains(value, "PRIVATE KEY") ||
		looksBase64JSON(strings.TrimSpace(value)) {
		return ""
	}
	return value
}

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
