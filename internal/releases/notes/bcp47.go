// bcp47.go: the pre-Edit locale gate for release notes (PRD #446 / #452).
//
// In Dir mode the locale is a FILE NAME: `--release-notes-dir notes/` ships
// whatever `<locale>.txt` it finds. A typo there (`en_US.txt`, the underscore
// form every other tool uses) used to travel all the way to tracks.update, so
// the failure arrived AFTER edits.insert and after the artifact upload: an Edit
// burnt, the good locales half-applied, and an error naming only the first bad
// tag. ValidateDirLocales runs the same names through a BCP-47 syntax check
// while the process is still offline, and reports every offending file at once.
//
// The check is SYNTACTIC, not a registry lookup: it answers "is this shaped
// like a language tag", which is what catches `en_US`, `klingon` and `e-US`.
// Whether Google Play actually serves a well-formed tag is Play's answer to
// give (the API rejects it clearly), and gplay ships no exhaustive Play locale
// table on this path: internal/metadata/locale's registry is a point-in-time
// snapshot on purpose, and using it here would reject a locale Google added
// last week. Go has no language-tag parser in its standard library, and pulling
// golang.org/x/text in for one predicate would add its CLDR tables to a binary
// whose whole pitch is one small static file (ADR-0007's dependency stance), so
// the grammar below is hand-rolled, like every other wire contract in this repo.
package notes

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ValidateDirLocales checks every `<locale>.txt` in dir for a well-formed
// BCP-47 language tag and returns a *ValidationError (exit 2) listing ALL the
// offending files when any is malformed, so a directory with three typos is
// fixed in one pass rather than three round-trips.
//
// It is pure filesystem I/O and MUST be called before the Edit is opened. An
// empty dir (the "no --release-notes-dir" case) is a no-op. An UNREADABLE dir
// is a rejection here, not a pass: a misspelt `--release-notes-dir` is knowable
// offline like a misspelt locale, and swallowing the read error only deferred
// it to Load, which runs INSIDE WithEdit, i.e. after edits.insert and after the
// artifact upload. That deferral is the exact failure mode #452 exists to
// remove, and it costs an Edit; Load keeps its own check for a caller that
// bypasses this gate.
//
// `default.txt` is exempt: it is the fallback marker, not a locale, and the
// locale it resolves to comes from edits.details.get (the API's own value).
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

// ValidBCP47 reports whether s is a well-formed BCP-47 language tag in the
// `language[-script][-region][-variant...]` shape Google Play uses for store
// locales (RFC 5646 langtag, minus the extension and private-use subtags no
// Play locale carries).
//
// Well-formed, not registered: `xy-ZZ` passes the grammar and is left for the
// API to refuse, while `en_US`, `en-`, `klingon` and `e` do not. Matching is
// case-INSENSITIVE (RFC 5646 §2.1: tags are case-insensitive, the mixed casing
// is a convention), so a `fr-fr.txt` is not rejected here for its casing; the
// exact spelling Play wants is a separate concern from tag well-formedness.
func ValidBCP47(s string) bool {
	if s == "" {
		return false
	}
	parts := strings.Split(s, "-")
	// Primary language subtag: 2-3 letters (ISO 639-1/-2/-3). RFC 5646 also
	// reserves 4-letter and registers 5-8-letter primaries, but none exists in
	// practice and allowing them would wave `klingon` through as "well-formed",
	// which defeats the point of the gate.
	if !alphaOnly(parts[0]) || len(parts[0]) < 2 || len(parts[0]) > 3 {
		return false
	}
	rest := parts[1:]
	// Up to 3 extlang subtags (3 letters each), e.g. `zh-yue`.
	for len(rest) > 0 && len(rest[0]) == 3 && alphaOnly(rest[0]) {
		rest = rest[1:]
	}
	// Optional script: exactly 4 letters (`Hant`).
	if len(rest) > 0 && len(rest[0]) == 4 && alphaOnly(rest[0]) {
		rest = rest[1:]
	}
	// Optional region: 2 letters (`US`) or 3 digits (`419`).
	if len(rest) > 0 && ((len(rest[0]) == 2 && alphaOnly(rest[0])) || (len(rest[0]) == 3 && digitsOnly(rest[0]))) {
		rest = rest[1:]
	}
	// Remaining subtags must be variants: 5-8 alphanumerics, or 4 starting
	// with a digit (`1996`, `rozaj`).
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
