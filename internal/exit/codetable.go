// codetable.go: the one rendering of the diagnostic-code catalog. It lives
// beside the catalog rather than in either caller because it has two of them
// (`gplay help exit-codes` builds a static help string, `gplay schema --codes`
// streams a Renderable), and a second copy of the column layout is exactly the
// kind of drift the single-source catalog exists to prevent.
package exit

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// RetryableLabel is the human spelling of the retryable bit, shared by every
// view so the table and the markdown table cannot disagree on the wording.
func RetryableLabel(retryable bool) string {
	if retryable {
		return "yes"
	}
	return "no"
}

// WriteCodeTable renders the catalog to w as an aligned
// CODE/EXIT/RETRYABLE/MEANING block.
func WriteCodeTable(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "CODE\tEXIT\tRETRYABLE\tMEANING"); err != nil {
		return err
	}
	for _, d := range CodeCatalog() {
		if _, err := fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n", d.Code, d.ExitCode, RetryableLabel(d.Retryable), d.Meaning); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// CodeTableString is WriteCodeTable for the callers that need a string rather
// than a stream: cobra prints a command's Long verbatim, so the help topic has
// nowhere to surface a write error anyway. A failed render degrades to the
// partial buffer instead of panicking; the catalog is a compiled-in constant
// and a bytes.Buffer write does not fail in practice.
func CodeTableString() string {
	var b strings.Builder
	_ = WriteCodeTable(&b)
	return b.String()
}
