package exitcodes

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exit-codes",
		Short: "Explain gplay's semantic exit codes",
		Long: "gplay returns a semantic exit code so scripts and agents can branch on the\noutcome without parsing output\n(https://gplay.sh/docs/concepts/exit-codes/):\n\n" +
			table() +
			"\nUnder --output json a failure also carries a stable diagnostic CODE, which\n" +
			"discriminates failures that share an exit code, plus a RETRYABLE bit.\n" +
			"The vocabulary is append-only; `gplay schema --codes --output json`\n" +
			"prints this same catalog for a machine to consume:\n\n" +
			exit.CodeTableString(),
		Example: `  gplay exit-codes`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.SetHelpTemplate("{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}\n\n{{end}}")
	return cmd
}

func table() string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "CODE\tMEANING\tRETRY-SAFE")
	for _, d := range exit.Catalog() {
		_, _ = fmt.Fprintf(tw, "%d\t%s\t%s\n", d.Code, d.Meaning, d.RetrySafe)
	}
	_ = tw.Flush()
	return b.String()
}
