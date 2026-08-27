// Package exitcodes registers `gplay exit-codes` (also reachable as
// `gplay help exit-codes`): a help topic that prints gplay's semantic
// exit-code taxonomy (docs/DESIGN.md §9) and, below it, the diagnostic-code
// vocabulary the JSON error envelope carries (ADR-0044). Both tables come from
// internal/exit (Catalog here, CodeTableString rendered there and shared with
// `gplay schema --codes`) so the documented contract cannot drift from the code
// that implements it.
package exitcodes

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

// NewCommand returns the `exit-codes` help-topic command. It has no Run: a bare
// `gplay exit-codes` and `gplay help exit-codes` both print the taxonomy via
// the Long text (cobra prints help for a runless, childless command).
func NewCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "exit-codes",
		Short: "Explain gplay's semantic exit codes",
		Long: "gplay returns a semantic exit code so scripts and agents can branch on the\noutcome without parsing output (docs/DESIGN.md §9):\n\n" +
			table() +
			"\nUnder --output json a failure also carries a stable diagnostic CODE, which\n" +
			"discriminates failures that share an exit code, plus a RETRYABLE bit\n" +
			"(ADR-0044). The vocabulary is append-only; `gplay schema --codes --output json`\n" +
			"prints this same catalog for a machine to consume:\n\n" +
			exit.CodeTableString(),
		Args: cobra.NoArgs,
	}
}

// table renders exit.Catalog as an aligned CODE/MEANING/RETRY-SAFE block.
func table() string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 2, 2, ' ', 0)
	// Writes to the tabwriter buffer; any error surfaces at Flush, which is
	// itself best-effort here (the result feeds a static help string).
	_, _ = fmt.Fprintln(tw, "CODE\tMEANING\tRETRY-SAFE")
	for _, d := range exit.Catalog() {
		_, _ = fmt.Fprintf(tw, "%d\t%s\t%s\n", d.Code, d.Meaning, d.RetrySafe)
	}
	_ = tw.Flush()
	return b.String()
}
