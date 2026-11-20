package kernel

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

func WrapArgErrors(cmd *cobra.Command) *cobra.Command {
	if cmd == nil {
		return cmd
	}
	if cmd.Args != nil {
		inner := cmd.Args
		takesNone := isNoArgs(inner)
		cmd.Args = func(c *cobra.Command, args []string) error {
			return asUsageError(c, args, takesNone, inner(c, args))
		}
	}
	for _, sub := range cmd.Commands() {
		WrapArgErrors(sub)
	}
	return cmd
}

func asUsageError(c *cobra.Command, args []string, takesNone bool, err error) error {
	if err == nil {
		return nil
	}
	var coder exit.Coder
	if errors.As(err, &coder) {
		return err
	}
	what := err.Error()
	placeholders := requiredPlaceholders(c.Use)
	switch {
	case takesNone && len(args) > 0 && !c.HasSubCommands():
		what = fmt.Sprintf("unexpected argument %q: %s takes no positional arguments", args[0], c.CommandPath())
	case len(args) < len(placeholders):
		what = "missing " + placeholders[len(args)]
	}
	return exit.Usagef("%s; usage: %s", what, c.UseLine())
}

func isNoArgs(v cobra.PositionalArgs) bool {
	return reflect.ValueOf(v).Pointer() == reflect.ValueOf(cobra.NoArgs).Pointer()
}

func requiredPlaceholders(use string) []string {
	fields := strings.Fields(use)
	if len(fields) < 2 {
		return nil
	}
	var out []string
	for _, f := range fields[1:] {
		if strings.HasPrefix(f, "-") || strings.HasPrefix(f, "[") {
			break
		}
		if strings.HasPrefix(f, "<") {
			out = append(out, strings.TrimSuffix(f, "..."))
		}
	}
	return out
}
