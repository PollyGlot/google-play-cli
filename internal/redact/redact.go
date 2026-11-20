package redact

import (
	"io"
	"regexp"
)

const Mask = "[REDACTED]"

const secretKey = `[a-z0-9_-]*(?:token|secret|password|passwd|pwd|api[_-]?key)`

const fieldKey = `(?:[a-z0-9_-]*(?:secret|password|passwd|pwd|api[_-]?key)|[a-z0-9_-]+[_-]token)`

const valueChar = `[^\s"'` + "`" + `,;)\]}]`

var unquotedValue = `(?:` + regexp.QuoteMeta(Mask) + valueChar + `*|` + valueChar + `+)`

type rule struct {
	re   *regexp.Regexp
	repl string
}

var patterns = []rule{
	{
		re:   regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----(?:.|\\n)*?-----END [A-Z ]*PRIVATE KEY-----`),
		repl: Mask,
	},
	{
		re:   regexp.MustCompile(`(?i)"(private_key|private_key_id)"\s*:\s*"(?:[^"\\]|\\.)*"`),
		repl: `"${1}": "` + Mask + `"`,
	},
	{
		re:   regexp.MustCompile(`(?i)(authorization"?\s*:\s*\[?"?\s*)(bearer|basic|token)(\s+)[A-Za-z0-9\-._~+/=]+`),
		repl: `${1}${2}${3}` + Mask,
	},
	{
		re:   regexp.MustCompile(`ey[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
		repl: Mask,
	},
	{
		re:   regexp.MustCompile(`\b(?:ya29|1//|ATZAV)[A-Za-z0-9\-._~+/]{10,}={0,2}`),
		repl: Mask,
	},
	{
		re:   regexp.MustCompile(`(?i)\b(bearer|basic)(\s+)[A-Za-z0-9\-._~+/]{16,}={0,2}`),
		repl: `${1}${2}` + Mask,
	},
	{
		re:   regexp.MustCompile(`(?i)("?` + secretKey + `"?\s*[:=]\s*")(?:[^"\\]|\\.)+"`),
		repl: `${1}` + Mask + `"`,
	},
	{
		re:   regexp.MustCompile(`(?i)(` + secretKey + `"?\s*=\s*)` + unquotedValue),
		repl: `${1}` + Mask,
	},
	{
		re:   regexp.MustCompile(`(?i)(` + fieldKey + `"?\s*:\s*)` + unquotedValue),
		repl: `${1}` + Mask,
	},
}

func String(s string) string {
	if s == "" {
		return s
	}
	for _, r := range patterns {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

func Bytes(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	return []byte(String(string(b)))
}

type writer struct{ w io.Writer }

func (rw writer) Write(p []byte) (int, error) {
	masked := Bytes(p)
	if _, err := rw.w.Write(masked); err != nil {
		return 0, err
	}
	return len(p), nil
}

func Writer(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	if _, ok := w.(writer); ok {
		return w
	}
	return writer{w: w}
}
