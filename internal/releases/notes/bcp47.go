package notes

import (
	"fmt"
	"path/filepath"
	"strings"
)

func ValidateDirLocales(dir string) error {
	if dir == "" {
		return nil
	}
	entries, err := osReadDir(dir)
	if err != nil {
		return &ValidationError{Message: "cannot read the release-notes directory: " + err.Error()}
	}
	var bad []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".txt") {
			continue
		}
		loc := strings.TrimSuffix(name, ".txt")
		if loc == defaultLocaleFile {
			continue
		}
		if !ValidBCP47(loc) {
			bad = append(bad, filepath.Join(dir, name))
		}
	}
	if len(bad) == 0 {
		return nil
	}
	return &ValidationError{
		Message: fmt.Sprintf(
			"release-notes file name(s) are not a valid BCP-47 locale tag: %s: rename them to the Play locale code (hyphen, not underscore: en-US, pt-BR, zh-Hant-TW)",
			strings.Join(bad, ", ")),
	}
}

func ValidBCP47(s string) bool {
	if s == "" {
		return false
	}
	parts := strings.Split(s, "-")
	if !alphaOnly(parts[0]) || len(parts[0]) < 2 || len(parts[0]) > 3 {
		return false
	}
	rest := parts[1:]
	for len(rest) > 0 && len(rest[0]) == 3 && alphaOnly(rest[0]) {
		rest = rest[1:]
	}
	if len(rest) > 0 && len(rest[0]) == 4 && alphaOnly(rest[0]) {
		rest = rest[1:]
	}
	if len(rest) > 0 && ((len(rest[0]) == 2 && alphaOnly(rest[0])) || (len(rest[0]) == 3 && digitsOnly(rest[0]))) {
		rest = rest[1:]
	}
	for _, v := range rest {
		if !isVariant(v) {
			return false
		}
	}
	return true
}

func isVariant(s string) bool {
	if len(s) >= 5 && len(s) <= 8 && alphanumOnly(s) {
		return true
	}
	return len(s) == 4 && isDigit(s[0]) && alphanumOnly(s)
}

func alphaOnly(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isAlpha(s[i]) {
			return false
		}
	}
	return len(s) > 0
}

func digitsOnly(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return len(s) > 0
}

func alphanumOnly(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isAlpha(s[i]) && !isDigit(s[i]) {
			return false
		}
	}
	return len(s) > 0
}

func isAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
