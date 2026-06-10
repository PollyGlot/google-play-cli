package output

import (
	"regexp"
	"strings"
	"unicode"
)

// Untrusted API strings (review text, store-listing copy, ...) reach the
// terminal verbatim through table and markdown cells. A hostile value can embed
// ANSI escape sequences — color/cursor codes, OSC title or hyperlink sequences —
// that a terminal or CI log interprets, letting an attacker rewrite the screen
// or smuggle a clickable link. sanitizeCell neutralizes that at the human-output
// render boundary. JSON output never calls it: machine consumers get the bytes
// verbatim (ADR-0003 pass-through).
//
// The two escape families that carry a payload are stripped whole (not just the
// ESC byte, which would leave litter like "[31m"):
//   - ansiOSC: ESC ] ... terminated by BEL or ST (ESC \) — titles, hyperlinks.
//   - ansiCSI: ESC [ params intermediates final — colors, cursor moves, erase.
//
// Raw-string patterns: regexp interprets \x1b / \x07 itself, so there is no
// Go-level escaping to get wrong.
var (
	ansiOSC = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	ansiCSI = regexp.MustCompile(`\x1b\[[0-9;:<=>?]*[ -/]*[@-~]`)
)

// sanitizeCell strips ANSI escape sequences and drops C0/C1 control characters
// from a single rendered cell. It is rune-based, so legitimate multi-byte UTF-8
// — accents, CJK, emoji — is untouched: only U+0000–U+001F, U+007F, and
// U+0080–U+009F (the control ranges, per unicode.IsControl) are removed, never
// the higher runes those scripts use. Stripping control characters also keeps
// an embedded TAB or newline from breaking table column alignment.
func sanitizeCell(s string) string {
	if s == "" {
		return s
	}
	s = ansiOSC.ReplaceAllString(s, "")
	s = ansiCSI.ReplaceAllString(s, "")
	// Drop any remaining control runes — stray ESC/BEL/NUL, a leftover C1 CSI
	// introducer (U+009B), etc. Fast-path the common all-printable case.
	if strings.IndexFunc(s, unicode.IsControl) < 0 {
		return s
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}
