// Package exitcodes registers `gplay exit-codes` (also reachable as
// `gplay help exit-codes`): a help topic that prints gplay's semantic
// exit-code taxonomy (docs/DESIGN.md §9). The table is built from
// internal/exit.Catalog so the documented contract cannot drift from the code
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
		Long:  "gplay returns a semantic exit code so scripts and agents can branch on the\noutcome without parsing output (docs/DESIGN.md §9):\n\n" + table(),
		Args:  cobra.NoArgs,
	}
}

// table renders exit.Catalog as an aligned CODE/MEANING/RETRY-SAFE block.
func table() string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "CODE\tMEANING\tRETRY-SAFE")
	for _, d := range exit.Catalog() {
		fmt.Fprintf(tw, "%d\t%s\t%s\n", d.Code, d.Meaning, d.RetrySafe)
	}
	_ = tw.Flush()
	return b.String()
}
