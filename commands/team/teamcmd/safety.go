package teamcmd

import (
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/team/vocab"
)

type Gate struct {
	Destructive     bool
	AdminConferring bool
}

func (g Gate) Requires() []string {
	var out []string
	if g.Destructive {
		out = append(out, "confirm")
	}
	if g.AdminConferring {
		out = append(out, "grant-admin")
	}
	return out
}

func (g Gate) Verify(confirm, grantAdmin bool) error {
	if g.Destructive && !confirm {
		return exit.SafetyFlag("confirm", "this is a destructive, irreversible action; pass --confirm to proceed (rehearse first with --dry-run)")
	}
	if g.AdminConferring && !grantAdmin {
		return exit.SafetyFlag("grant-admin", "this confers admin (full control over the developer account); pass --grant-admin to proceed (rehearse first with --dry-run)")
	}
	return nil
}

func ResolvePerms(scope vocab.Scope, role string, perms []string, clear bool) (enums, warnings []string, err error) {
	switch {
	case clear:
		return []string{}, nil, nil
	case role != "":
		enums, err = vocab.ResolveRole(scope, role)
		return enums, nil, err
	default:
		return vocab.ResolvePermissions(scope, perms)
	}
}
