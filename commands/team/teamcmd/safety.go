package teamcmd

import (
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/team/vocab"
)

// Gate models a `team` write's safety requirements (ADR-0017): the three tiers
// reduce to two orthogonal flags. Destructive (remove/revoke) requires
// --confirm; AdminConferring (a resolved set including the all-permissions
// enum) requires --grant-admin on top of its tier. Routine non-admin writes
// have neither set.
type Gate struct {
	Destructive     bool
	AdminConferring bool
}

// Requires returns the canonical names (no leading --) of the safety flags the
// LIVE write needs, in stable order (confirm before grant-admin). It is the
// `requires` array surfaced by --dry-run --output json so an agent can learn
// the gate before running live (ADR-0017 §4), independent of which flags are
// currently set.
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

// Verify checks the provided flags satisfy the gate, returning a
// *exit.SafetyFlagError (exit 3) naming the first missing flag, or nil. It is
// the LIVE-path guard; --dry-run never calls it (it reports Requires instead).
// CI=true is irrelevant here: gplay never auto-confirms (ADR-0017 §6); a gate
// is satisfied only by its explicit flag.
func (g Gate) Verify(confirm, grantAdmin bool) error {
	if g.Destructive && !confirm {
		return exit.SafetyFlag("confirm", "this is a destructive, irreversible action; pass --confirm to proceed (rehearse first with --dry-run)")
	}
	if g.AdminConferring && !grantAdmin {
		return exit.SafetyFlag("grant-admin", "this confers admin (full control over the developer account); pass --grant-admin to proceed (rehearse first with --dry-run)")
	}
	return nil
}

// ResolvePerms resolves a write's target permissions under scope from the
// already-XOR-validated flags: --clear yields an explicit empty set, --role
// expands a frozen bundle, otherwise the --permissions aliases/raw enums are
// resolved. Returns the resolved enums, any steering warnings (deprecated raw
// enums), and a *exit.UsageError (exit 2) for an unknown alias/role.
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
