package kernel

import (
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

const NoPackageMsg = "no package: pass --package <pkg> or run gplay init in your repo"

func (rc *RunContext) Package(flag string) (string, error) {
	pkg := strings.TrimSpace(flag)
	if pkg == "" && rc != nil && rc.Resolved != nil {
		pkg = strings.TrimSpace(rc.Resolved.Pin)
	}
	if pkg == "" {
		return "", &exit.UsageError{Msg: NoPackageMsg}
	}
	return pkg, nil
}
