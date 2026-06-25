package gamescmd

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/PollyGlot/google-play-cli/internal/output"
)

// writeDryRunView is the gplay-shaped --dry-run report for a create/update: the
// verb, the addressing target, and the exact request body that WOULD be sent —
// so an agent can inspect the payload offline before writing live.
type writeDryRunView struct {
	DryRun bool            `json:"dryRun"`
	Verb   string          `json:"verb"`
	Target string          `json:"target"`
	Body   json.RawMessage `json:"body"`
}

// AchievementWritePayload renders a create/update result (the row table + the
// verbatim API response for --output json) or, under --dry-run, a preview of
// the request body.
type AchievementWritePayload struct {
	Verb   string // "create" | "update"
	Target string // e.g. "application 123" | "achievement a7"
	DryRun bool
	Body   json.RawMessage // the request body (preview in dry-run)
	Row    AchievementRow
	Cols   []output.Column[AchievementRow]
	Raw    json.RawMessage // live API response
}

func (p AchievementWritePayload) Renderers() output.Renderers {
	return output.Renderers{
		Table: func(w io.Writer) error {
			if p.DryRun {
				return writeDryRunTable(w, p.Verb, p.Target)
			}
			return output.RenderTable(w, p.Cols, []AchievementRow{p.Row})
		},
		JSON: func(w io.Writer) error { return writeJSON(w, p.DryRun, p.Verb, p.Target, p.Body, p.Raw) },
		Markdown: func(w io.Writer) error {
			if p.DryRun {
				return writeDryRunMarkdown(w, p.Verb, p.Target)
			}
			return output.RenderMarkdown(w, p.Cols, []AchievementRow{p.Row})
		},
	}
}

// LeaderboardWritePayload mirrors AchievementWritePayload for leaderboards.
type LeaderboardWritePayload struct {
	Verb   string
	Target string
	DryRun bool
	Body   json.RawMessage
	Row    LeaderboardRow
	Cols   []output.Column[LeaderboardRow]
	Raw    json.RawMessage
}

func (p LeaderboardWritePayload) Renderers() output.Renderers {
	return output.Renderers{
		Table: func(w io.Writer) error {
			if p.DryRun {
				return writeDryRunTable(w, p.Verb, p.Target)
			}
			return output.RenderTable(w, p.Cols, []LeaderboardRow{p.Row})
		},
		JSON: func(w io.Writer) error { return writeJSON(w, p.DryRun, p.Verb, p.Target, p.Body, p.Raw) },
		Markdown: func(w io.Writer) error {
			if p.DryRun {
				return writeDryRunMarkdown(w, p.Verb, p.Target)
			}
			return output.RenderMarkdown(w, p.Cols, []LeaderboardRow{p.Row})
		},
	}
}

// DeletePayload renders a delete result. The API returns no body, so the JSON
// view is a synthesized {"id":...,"deleted":true} (not an API pass-through —
// there is nothing to pass through). --dry-run reports the --confirm gate.
type DeletePayload struct {
	Kind   string // "achievement" | "leaderboard"
	ID     string
	DryRun bool
}

type deleteView struct {
	Kind     string   `json:"kind"`
	ID       string   `json:"id"`
	Deleted  bool     `json:"deleted,omitempty"`
	DryRun   bool     `json:"dryRun,omitempty"`
	Requires []string `json:"requires,omitempty"`
}

func (p DeletePayload) Renderers() output.Renderers {
	return output.Renderers{
		Table: func(w io.Writer) error {
			if p.DryRun {
				_, err := fmt.Fprintf(w, "would delete %s %s (dry-run); requires --confirm\n", p.Kind, p.ID)
				return err
			}
			_, err := fmt.Fprintf(w, "deleted %s %s\n", p.Kind, p.ID)
			return err
		},
		JSON: func(w io.Writer) error {
			if p.DryRun {
				return output.WriteJSON(w, deleteView{Kind: p.Kind, ID: p.ID, DryRun: true, Requires: []string{"confirm"}})
			}
			return output.WriteJSON(w, deleteView{Kind: p.Kind, ID: p.ID, Deleted: true})
		},
		Markdown: func(w io.Writer) error {
			if p.DryRun {
				_, err := fmt.Fprintf(w, "- **dry-run**: would delete %s `%s` (requires --confirm)\n", p.Kind, p.ID)
				return err
			}
			_, err := fmt.Fprintf(w, "- **deleted** %s `%s`\n", p.Kind, p.ID)
			return err
		},
	}
}

func writeJSON(w io.Writer, dryRun bool, verb, target string, body, raw json.RawMessage) error {
	if dryRun {
		return output.WriteJSON(w, writeDryRunView{DryRun: true, Verb: verb, Target: target, Body: body})
	}
	_, err := w.Write(raw)
	return err
}

func writeDryRunTable(w io.Writer, verb, target string) error {
	_, err := fmt.Fprintf(w, "would %s on %s (dry-run); --output json shows the request body\n", verb, target)
	return err
}

func writeDryRunMarkdown(w io.Writer, verb, target string) error {
	_, err := fmt.Fprintf(w, "- **dry-run**: would %s on %s\n", verb, target)
	return err
}
