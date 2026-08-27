// codes.go: `gplay schema --codes`, the machine-readable projection of the
// diagnostic-code catalog (ADR-0044). It rides `schema` because that is
// already gplay's offline introspection surface: a skill author asking "what
// can this binary tell me about itself" has one command to call, and the JSON
// view keeps the catalog consumable without scraping the `help exit-codes`
// table. Like the rest of `schema` it is offline, needs no credentials, and
// wraps no Developer API call, so ADR-0003's pass-through rule does not apply.
package schema

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// CodesPayload renders the diagnostic-code catalog. It carries no state: the
// catalog is a compiled-in constant, and reading it through exit.CodeCatalog
// is what keeps this view from drifting from the classifier.
type CodesPayload struct{}

// Renderers satisfies output.Renderable.
func (p CodesPayload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { return p.renderJSON(w) },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

// codesJSONView is the JSON shape: an object with a named list, not a bare
// array, so a later addition (a contract version, a deprecation marker) is an
// added key rather than a breaking reshape.
type codesJSONView struct {
	Codes []exit.CodeDoc `json:"codes"`
}

func (CodesPayload) renderJSON(w io.Writer) error {
	return output.WriteJSON(w, codesJSONView{Codes: exit.CodeCatalog()})
}

func (CodesPayload) renderTable(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "CODE\tEXIT\tRETRYABLE\tMEANING"); err != nil {
		return err
	}
	for _, d := range exit.CodeCatalog() {
		if _, err := fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n", d.Code, d.ExitCode, yesNo(d.Retryable), d.Meaning); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func (CodesPayload) renderMarkdown(w io.Writer) error {
	var b strings.Builder
	b.WriteString("| Code | Exit | Retryable | Meaning |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, d := range exit.CodeCatalog() {
		fmt.Fprintf(&b, "| `%s` | %d | %s | %s |\n", d.Code, d.ExitCode, yesNo(d.Retryable), d.Meaning)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
