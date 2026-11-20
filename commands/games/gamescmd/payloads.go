package gamescmd

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/PollyGlot/google-play-cli/internal/output"
)

type writeDryRunView struct {
	DryRun bool            `json:"dryRun"`
	Verb   string          `json:"verb"`
	Target string          `json:"target"`
	Body   json.RawMessage `json:"body"`
}

type AchievementWritePayload struct {
	Verb   string
	Target string
	DryRun bool
	Body   json.RawMessage
	Row    AchievementRow
	Cols   []output.Column[AchievementRow]
	Raw    json.RawMessage
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

type DeletePayload struct {
	Kind   string
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
