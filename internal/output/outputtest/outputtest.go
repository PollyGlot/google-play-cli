// Package outputtest is the test-only seam for internal/output. It hides
// the SetIsTerminalFunc/IsTerminalFunc swap-and-restore dance behind a
// single ForceTerminal helper so command-level tests no longer copy it,
// and renders a command's JSON output against a golden file (GoldenJSON).
//
// Production code must not import this package.
package outputtest

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"unicode"

	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// ForceTerminal pins output.isTTY to v for the duration of t, restoring
// the previous detector via t.Cleanup. Use it instead of attaching a pty
// to the test process.
func ForceTerminal(t *testing.T, v bool) {
	t.Helper()
	prev := output.IsTerminalFunc()
	output.SetIsTerminalFunc(func(_ io.Writer) bool { return v })
	t.Cleanup(func() { output.SetIsTerminalFunc(prev) })
}

// RenderJSON runs r's JSON renderer and returns the bytes it wrote. A nil JSON
// renderer or a render error fails the test: every command supports
// --output json, and the renderer is the code the struct-level tests of Run
// never execute.
func RenderJSON(t testing.TB, r output.Renderable) []byte {
	t.Helper()
	fn := r.Renderers().JSON
	if fn == nil {
		t.Fatalf("outputtest: %T has no JSON renderer", r)
	}
	var buf bytes.Buffer
	if err := fn(&buf); err != nil {
		t.Fatalf("outputtest: render JSON: %v", err)
	}
	return buf.Bytes()
}

// htmlEscapes are the sequences encoding/json writes for <, > and & when HTML
// escaping is left on. WriteJSON turns it off (a consumer greps these bytes),
// so any of them in a gplay-authored golden means an encoder bypassed it.
var htmlEscapes = [][]byte{[]byte(`\u003c`), []byte(`\u003e`), []byte(`\u0026`)}

// GoldenJSON renders r's JSON output and compares it with testdata/<name>
// (testkit.Golden, so `go test <pkg> -update` rewrites it). It is meant for
// gplay-authored shapes (views, dry-run previews, summaries): the frozen
// contract of ADR-0010. API passthrough output is asserted byte-equal with the
// fixture body instead, since the API owns that shape (ADR-0003).
//
// Beyond the byte comparison it checks the output is one valid JSON document
// and carries no HTML escape, so a golden cannot freeze a broken encoder.
func GoldenJSON(t testing.TB, name string, r output.Renderable) {
	t.Helper()
	got := RenderJSON(t, r)
	if !json.Valid(got) {
		t.Fatalf("outputtest: %s is not valid JSON:\n%s", name, got)
	}
	for _, esc := range htmlEscapes {
		if bytes.Contains(got, esc) {
			t.Errorf("outputtest: %s contains the HTML escape %s: an encoder bypassed output.WriteJSON", name, esc)
		}
	}
	testkit.Golden(t, name, got)
}

// HostileJSON is an API string value, JSON-escaped for a fake response body,
// that carries the two escape families a hostile value uses: an OSC
// window-title set (ESC ] 0 ; ... BEL) and a CSI erase-screen (ESC [ 2 J).
// Splice it into a fixture field that a human view prints.
const HostileJSON = `\u001b]0;pwned\u0007\u001b[2J`

// RenderHuman renders r in every human format it supports (table, markdown)
// through output.Render, the path the kernel takes, and fails t when one of
// them lets a control rune other than \n or \t through, or leaves the OSC
// payload or the CSI parameters of HostileJSON behind. It returns the outputs
// keyed by format, for the caller's own assertions.
func RenderHuman(t testing.TB, r output.Renderable) map[output.Format]string {
	t.Helper()
	rs := r.Renderers()
	out := map[output.Format]string{}
	for f, fn := range map[output.Format]func(io.Writer) error{output.FormatTable: rs.Table, output.FormatMarkdown: rs.Markdown} {
		if fn == nil {
			continue
		}
		var buf bytes.Buffer
		if err := output.Render(&buf, f, rs); err != nil {
			t.Fatalf("outputtest: render %s: %v", f, err)
		}
		got := buf.String()
		if i := strings.IndexFunc(got, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }); i >= 0 {
			t.Errorf("outputtest: --output %s leaks control rune %q at byte %d:\n%q", f, []rune(got[i:])[0], i, got)
		}
		for _, litter := range []string{"pwned", "[2J"} {
			if strings.Contains(got, litter) {
				t.Errorf("outputtest: --output %s keeps %q of an escape sequence:\n%q", f, litter, got)
			}
		}
		out[f] = got
	}
	if len(out) == 0 {
		t.Fatalf("outputtest: %T has no human renderer", r)
	}
	return out
}
