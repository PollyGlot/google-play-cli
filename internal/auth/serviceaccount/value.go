package serviceaccount

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"unicode"
)

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

func isInlineJSON(value string) bool {
	return firstRune(value) == '{'
}

type UnreadableValueError struct {
	Source string
	Path   string
	Len    int
	Reason string
	Hint   string
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

func (*UnreadableValueError) ExitCode() int { return 10 }

const maxDisplayablePath = 255

func displayablePath(value string) string {
	if len(value) > maxDisplayablePath ||
		strings.ContainsAny(value, "\r\n{") ||
		strings.Contains(value, "-----BEGIN") || strings.Contains(value, "PRIVATE KEY") ||
		looksBase64JSON(strings.TrimSpace(value)) {
		return ""
	}
	return value
}

func pathErrorReason(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) && pe.Err != nil {
		return pe.Err.Error()
	}
	return ""
}

const byteOrderMark = rune(0xFEFF)

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

func looksBase64JSON(s string) bool {
	s = strings.TrimRightFunc(s, unicode.IsSpace)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
		if b, err := enc.DecodeString(s); err == nil && isInlineJSON(string(b)) {
			return true
		}
	}
	return false
}

func firstRune(s string) rune {
	for _, r := range s {
		if !unicode.IsSpace(r) {
			return r
		}
	}
	return 0
}
