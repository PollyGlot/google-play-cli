package teamcmd

import (
	"fmt"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// EmitWarnings prints vocabulary steering warnings (a deprecated raw enum was
// used) to stderr so they surface regardless of --output format.
func EmitWarnings(rc *kernel.RunContext, warnings []string) {
	for _, w := range warnings {
		_, _ = fmt.Fprintf(rc.Stderr, "warning: %s\n", w)
	}
}

// JoinOrNone renders a permission list for the human (table/markdown) views,
// using "(none)" for an empty/cleared set so it never reads as a blank cell.
func JoinOrNone(s []string) string {
	if len(s) == 0 {
		return "(none)"
	}
	return strings.Join(s, ", ")
}

// NonNil normalises a nil slice to an empty one so JSON renders [] rather than
// null: the shape an agent parsing the dry-run payload expects.
func NonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
