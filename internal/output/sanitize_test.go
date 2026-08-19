package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestSanitizeCell(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello world", "hello world"},
		{"empty", "", ""},
		{"csi color", "\x1b[31mRED\x1b[0m", "RED"},
		{"csi cursor + erase", "a\x1b[2J\x1b[1;1Hb", "ab"},
		{"osc title (BEL)", "\x1b]0;pwned title\x07keep", "keep"},
		{"osc hyperlink (ST)", "\x1b]8;;http://evil.example\x1b\\link\x1b]8;;\x1b\\", "link"},
		{"bare C0 controls", "a\x00b\x07c\x08d", "abcd"},
		{"DEL", "a\x7fb", "ab"},
		{"tab and newline stripped (single cell)", "col1\tcol2\nrow", "col1col2row"},
		{"lone ESC then letter", "\x1bcResetSeq", "cResetSeq"},
		{"C1 control rune dropped", "a\u009bb", "ab"},
		// Legitimate multi-byte content must survive untouched.
		{"accents", "café déjà vu", "café déjà vu"},
		{"cjk", "日本語のレビュー", "日本語のレビュー"},
		{"emoji", "great app 🎉🚀", "great app 🎉🚀"},
		{"mixed hostile + multibyte", "\x1b[31mRED\x1b[0m\x07\x00 \x1b]0;pwn\x07tail 日本 é 🎉", "RED tail 日本 é 🎉"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeCell(tc.in); got != tc.want {
				t.Errorf("sanitizeCell(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestSanitizeCell_noEscapeBytesSurvive is a property check: whatever the input,
// the output never contains an ESC (0x1b) or BEL (0x07) byte: the carriers of
// an ANSI injection.
func TestSanitizeCell_noEscapeBytesSurvive(t *testing.T) {
	for _, in := range []string{
		"\x1b[38;5;201mfancy\x1b[0m",
		"\x1b]8;;javascript:alert(1)\x07click\x1b]8;;\x07",
		"\x1b[?25lhidden cursor",
		"plain text with é and 漢字",
	} {
		got := sanitizeCell(in)
		if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\x07') {
			t.Errorf("sanitizeCell(%q) = %q still contains an escape/BEL byte", in, got)
		}
	}
}

// hostileColumn renders a fixed hostile string for any item: the untrusted-cell
// stand-in for the render-boundary integration tests.
const hostileCell = "\x1b[31mRED\x1b[0m\x07start \x1b]0;pwn\x07 café 漢字 🎉"
const hostileWant = "REDstart  café 漢字 🎉"

func TestRenderTable_neutralizesHostileCells(t *testing.T) {
	cols := []Column[int]{{Key: "c", Header: "C", Value: func(int) string { return hostileCell }}}
	var buf bytes.Buffer
	if err := RenderTable(&buf, cols, []int{1}); err != nil {
		t.Fatalf("RenderTable: %v", err)
	}
	out := buf.String()
	if strings.ContainsRune(out, '\x1b') || strings.ContainsRune(out, '\x07') {
		t.Errorf("table output still carries escape/BEL bytes: %q", out)
	}
	if !strings.Contains(out, hostileWant) {
		t.Errorf("table output = %q, want it to contain the sanitized cell %q", out, hostileWant)
	}
}

func TestRenderMarkdown_neutralizesHostileCells(t *testing.T) {
	cols := []Column[int]{{Key: "c", Header: "C", Value: func(int) string { return hostileCell }}}
	var buf bytes.Buffer
	if err := RenderMarkdown(&buf, cols, []int{1}); err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	out := buf.String()
	if strings.ContainsRune(out, '\x1b') || strings.ContainsRune(out, '\x07') {
		t.Errorf("markdown output still carries escape/BEL bytes: %q", out)
	}
	if !strings.Contains(out, hostileWant) {
		t.Errorf("markdown output = %q, want it to contain the sanitized cell %q", out, hostileWant)
	}
}

func TestMarkdownTable_neutralizesAndStillEscapesPipes(t *testing.T) {
	var buf bytes.Buffer
	if err := MarkdownTable(&buf, []string{"H"}, [][]string{{"\x1b[31ma|b\x1b[0m"}}); err != nil {
		t.Fatalf("MarkdownTable: %v", err)
	}
	out := buf.String()
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("markdown table still carries ESC: %q", out)
	}
	// Sanitized AND the literal pipe still escaped to \| (both happen in joinCells).
	if !strings.Contains(out, `a\|b`) {
		t.Errorf("markdown table = %q, want sanitized cell with escaped pipe a\\|b", out)
	}
}
