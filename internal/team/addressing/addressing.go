// Package addressing resolves the Developer account id every `gplay team`
// command is keyed by (ADR-0015). The id rides on the Account, not the
// committed project pin: the org follows the credential, not the repo, and the
// API offers no way to discover it.
//
// Resolution order (later wins, mirroring ADR-0004):
//
//	active Account.DeveloperID → project-local config.local.json → GPLAY_DEVELOPER_ID → --developer-id
//
// The first two (in-config) layers are pre-merged into config.Resolved.DeveloperID
// by config.Load; this package applies the higher-precedence env var and flag
// on top. An unresolved id is an auth-family failure (exit 10), not a usage
// error: a missing developer-id is a credential-configuration gap, like a
// missing Account.
package addressing

import (
	"os"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// EnvDeveloperID is the env var supplying the developer-id. It sits above the
// in-config layers and below the --developer-id flag in the cascade.
const EnvDeveloperID = "GPLAY_DEVELOPER_ID"

// unresolvedError signals that no cascade layer yielded a developer-id. It is
// an auth-family failure (exit 10 per docs/DESIGN.md §9 / ADR-0015 §4), and
// its message names every way to set one.
type unresolvedError struct{}

func (unresolvedError) Error() string {
	return "no developer-id resolved for the active Account: set it with `gplay auth login --developer-id <id>`, " +
		"export GPLAY_DEVELOPER_ID, pin it in .gplay/config.local.json, or pass --developer-id <id> " +
		"(find it in the Play Console URL: play.google.com/console/u/0/developers/<developerId>/...)"
}
func (unresolvedError) ExitCode() int { return 10 }

// ErrUnresolved is returned when no layer yields a developer-id.
var ErrUnresolved error = unresolvedError{}

// ForRun resolves the developer-id for a running command: the --developer-id
// flag value, GPLAY_DEVELOPER_ID, then the config cascade on rc. When nothing
// resolves it checks the Account first (#593): with no Account at all, "no
// developer-id for the active Account" names the wrong gap, and setting a
// developer-id would only lead to the no-Account error one step later. The
// Account check runs only on that failure path, so a resolved id never costs a
// keyring probe here.
func ForRun(rc *kernel.RunContext, flag string) (string, error) {
	id, err := Resolve(flag, os.Getenv(EnvDeveloperID), rc.Resolved)
	if err == nil {
		return id, nil
	}
	if aerr := rc.RequireAccount(); aerr != nil {
		return "", aerr
	}
	return "", err
}

// Resolve returns the developer-id using the ADR-0015 cascade (later wins).
// flag is the --developer-id value, env the GPLAY_DEVELOPER_ID value (read
// once by the caller), and resolved the config cascade snapshot whose
// DeveloperID already merges the active Account and project-local layers.
// Returns ErrUnresolved (exit 10) when nothing resolves.
func Resolve(flag, env string, resolved *config.Resolved) (string, error) {
	if v := strings.TrimSpace(flag); v != "" {
		return v, nil
	}
	if v := strings.TrimSpace(env); v != "" {
		return v, nil
	}
	if resolved != nil {
		if v := strings.TrimSpace(resolved.DeveloperID); v != "" {
			return v, nil
		}
	}
	return "", ErrUnresolved
}
