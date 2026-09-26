package kernel

import (
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

// NoPackageMsg is the exit-2 message every package-axis command returns when
// neither --package nor a Project pin names the app. It is exported so tests
// in any command package can assert on the one wording instead of a copy.
const NoPackageMsg = "no package: pass --package <pkg> or run gplay init in your repo"

// Package resolves the app package a package-axis command targets: the
// --package flag wins, else the Project pin (.gplay/config.json), else an
// exit-2 usage error. Both sources are trimmed, so a flag of only whitespace
// falls through to the pin exactly as an empty flag does.
//
// It is the one resolver: 41 per-command copies used to drift on the trim
// (some sent " com.x " verbatim to the API) and on the nil guards. A nil
// receiver, or a RunContext without a resolved cascade, behaves as "no pin"
// so tests that build a bare context and pass --package keep working.
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
