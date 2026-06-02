// Package teamcmd holds the helpers shared by every `gplay team` leaf command:
// today, resolving the Developer account id (ADR-0015 cascade) and the
// type-once persistence that makes it sticky.
package teamcmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/team/addressing"
)

// DeveloperID resolves the developer-id for a team command via the ADR-0015
// cascade (active Account → project-local → GPLAY_DEVELOPER_ID → flag) and
// performs the type-once capture (#151): when the --developer-id flag supplied
// the value AND the active Account has none yet, it is persisted to the global
// Account record — mirroring how `apps add` populates Packages. An Account that
// already carries a developer-id is NOT overwritten; an explicit flag then
// overrides for that invocation only. Persistence always targets the global
// Account record, never the committed project config, and a hiccup is reported
// to stderr rather than failing the command (resolution already succeeded).
func DeveloperID(rc *kernel.RunContext, flag string) (string, error) {
	id, err := addressing.Resolve(flag, os.Getenv(addressing.EnvDeveloperID), rc.Resolved)
	if err != nil {
		return "", err
	}
	if f := strings.TrimSpace(flag); f != "" {
		captureTypeOnce(rc, f)
	}
	return id, nil
}

// captureTypeOnce persists id to the active Account's record iff that Account
// currently lacks a developer-id. It is a no-op for an ad-hoc (inline)
// credential, which has no global Account record to write to.
func captureTypeOnce(rc *kernel.RunContext, id string) {
	if rc.AccountName == "" || rc.ConfigPath == "" {
		return
	}
	g, err := config.LoadGlobalOrEmpty(rc.Ctx, fsOrDefault(rc), rc.ConfigPath)
	if err != nil {
		return
	}
	for i := range g.Accounts {
		if g.Accounts[i].Name != rc.AccountName {
			continue
		}
		if strings.TrimSpace(g.Accounts[i].DeveloperID) != "" {
			return // already set — an explicit flag is an invocation-only override
		}
		g.Accounts[i].DeveloperID = id
		if err := g.Save(rc.Ctx, fsOrDefault(rc), rc.ConfigPath); err != nil {
			_, _ = fmt.Fprintf(rc.Stderr, "gplay: warning: could not persist developer-id to Account %q: %v\n", rc.AccountName, err)
			return
		}
		_, _ = fmt.Fprintf(rc.Stderr, "✓ developer-id %s saved to Account %q\n", id, rc.AccountName)
		return
	}
}

func fsOrDefault(rc *kernel.RunContext) config.FS {
	if rc.FS != nil {
		return rc.FS
	}
	return config.OSFS{}
}
