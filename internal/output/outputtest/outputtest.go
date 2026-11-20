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

func ForceTerminal(t *testing.T, v bool) {
	t.Helper()
	prev := output.IsTerminalFunc()
	output.SetIsTerminalFunc(func(_ io.Writer) bool { return v })
	t.Cleanup(func() { output.SetIsTerminalFunc(prev) })
}

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

var htmlEscapes = [][]byte{[]byte(`\u003c`), []byte(`\u003e`), []byte(`\u0026`)}

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

const HostileJSON = `\u001b]0;pwned\u0007\u001b[2J`

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
