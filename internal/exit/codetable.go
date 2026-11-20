package exit

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

func RetryableLabel(retryable bool) string {
	if retryable {
		return "yes"
	}
	return "no"
}

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

func CodeTableString() string {
	var b strings.Builder
	_ = WriteCodeTable(&b)
	return b.String()
}
