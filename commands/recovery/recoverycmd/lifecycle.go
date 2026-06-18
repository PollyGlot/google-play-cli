package recoverycmd

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// RequireConfirm enforces the DESTRUCTIVE-tier --confirm gate (ADR-0017): the
// production-impacting recovery leaves (deploy/cancel/add-targeting) refuse with
// exit 3 (a deterministically resolvable *exit.SafetyFlagError naming the flag)
// unless --confirm is passed. The LIVE path calls this; --dry-run never does
// (it reports the requirement via LifecyclePayload's `requires` array instead).
func RequireConfirm(confirm bool, msg string) error {
	if confirm {
		return nil
	}
	return exit.SafetyFlag("confirm", "%s", msg)
}

// LifecyclePayload renders a recovery lifecycle write (deploy/cancel/add-targeting).
// On --dry-run it is the gplay-shaped rehearsal carrying the ADR-0017 `requires`
// array; on a live write it passes the (often empty {}) API response through
// verbatim (ADR-0003) and never fabricates a status object.
type LifecyclePayload struct {
	Verb       string // "deploy" | "cancel" | "add-targeting"
	RecoveryID string
	Package    string
	DryRun     bool
	Raw        json.RawMessage
}

func (p LifecyclePayload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { return p.renderJSON(w) },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

func (p LifecyclePayload) renderTable(w io.Writer) error {
	if p.DryRun {
		_, err := fmt.Fprintf(w, "would %s recovery %s for %s (dry-run); requires --confirm\n", p.Verb, p.RecoveryID, p.Package)
		return err
	}
	_, err := fmt.Fprintf(w, "recovery %s: %s ok\n", p.RecoveryID, p.Verb)
	return err
}

func (p LifecyclePayload) renderMarkdown(w io.Writer) error {
	if p.DryRun {
		_, err := fmt.Fprintf(w, "- **dry-run**: would %s recovery `%s` (requires --confirm)\n", p.Verb, p.RecoveryID)
		return err
	}
	_, err := fmt.Fprintf(w, "- **%s**: recovery `%s` ok\n", p.Verb, p.RecoveryID)
	return err
}

type dryRunView struct {
	DryRun     bool     `json:"dryRun"`
	Action     string   `json:"action"`
	RecoveryID string   `json:"recoveryId"`
	Package    string   `json:"package"`
	Requires   []string `json:"requires"`
}

func (p LifecyclePayload) renderJSON(w io.Writer) error {
	if p.DryRun {
		return output.WriteJSON(w, dryRunView{
			DryRun:     true,
			Action:     p.Verb,
			RecoveryID: p.RecoveryID,
			Package:    p.Package,
			Requires:   []string{"confirm"},
		})
	}
	// Live: verbatim API pass-through (often an empty {} body).
	_, err := w.Write(p.Raw)
	return err
}
