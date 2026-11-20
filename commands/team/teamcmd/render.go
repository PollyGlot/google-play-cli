package teamcmd

import (
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

func EmitWarnings(rc *kernel.RunContext, warnings []string) {
	for _, w := range warnings {
		rc.Warnf("%s", w)
	}
}

func JoinOrNone(s []string) string {
	if len(s) == 0 {
		return "(none)"
	}
	return strings.Join(s, ", ")
}

func NonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
