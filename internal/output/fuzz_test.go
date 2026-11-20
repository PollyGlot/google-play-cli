package output

import (
	"strings"
	"testing"
	"unicode"
)

func FuzzSanitizeCell(f *testing.F) {
	for _, seed := range []string{
		"",
		"plain review text",
		"\x1b[31mred\x1b[0m",
		"\x1b]8;;https://evil.example\x07click\x1b]8;;\x07",
		"\x1b]0;title\x1b\\after",
		"\x1b[\x1b[31m31m",
		"\u009b31m",
		"tab\there\nnewline\r\x00nul\x7fdel",
		"é 日本語 🎉",
		"\xff\xfe invalid utf-8",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := sanitizeCell(s)
		if i := strings.IndexFunc(out, unicode.IsControl); i >= 0 {
			t.Fatalf("sanitizeCell(%q) = %q keeps control rune %U", s, out, []rune(out[i:])[0])
		}
		if again := sanitizeCell(out); again != out {
			t.Fatalf("sanitizeCell is not idempotent: %q -> %q -> %q", s, out, again)
		}
	})
}
