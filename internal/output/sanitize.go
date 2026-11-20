package output

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	ansiOSC = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	ansiCSI = regexp.MustCompile(`\x1b\[[0-9;:<=>?]*[ -/]*[@-~]`)
)

func sanitizeCell(s string) string {
	return sanitize(s, false)
}

func SanitizeCell(s string) string {
	return sanitizeCell(s)
}

func SanitizeText(s string) string {
	return sanitize(s, true)
}

func sanitize(s string, keepLayout bool) string {
	if s == "" {
		return s
	}
	s = ansiOSC.ReplaceAllString(s, "")
	s = ansiCSI.ReplaceAllString(s, "")
	drop := func(r rune) bool {
		if keepLayout && (r == '\n' || r == '\t') {
			return false
		}
		return unicode.IsControl(r)
	}
	if strings.IndexFunc(s, drop) < 0 {
		return s
	}
	return strings.Map(func(r rune) rune {
		if drop(r) {
			return -1
		}
		return r
	}, s)
}
