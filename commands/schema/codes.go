package schema

import (
	"fmt"
	"io"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

type CodesPayload struct{}

func (p CodesPayload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { return p.renderJSON(w) },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

type codesJSONView struct {
	Codes []exit.CodeDoc `json:"codes"`
}

func (CodesPayload) renderJSON(w io.Writer) error {
	return output.WriteJSON(w, codesJSONView{Codes: exit.CodeCatalog()})
}

func (CodesPayload) renderTable(w io.Writer) error {
	return exit.WriteCodeTable(w)
}

func (CodesPayload) renderMarkdown(w io.Writer) error {
	var b strings.Builder
	b.WriteString("| Code | Exit | Retryable | Meaning |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, d := range exit.CodeCatalog() {
		fmt.Fprintf(&b, "| `%s` | %d | %s | %s |\n", d.Code, d.ExitCode, exit.RetryableLabel(d.Retryable), d.Meaning)
	}
	_, err := io.WriteString(w, b.String())
	return err
}
