// Package list implements `gplay games achievements list --application-id <id>`:
// list a game's achievement configurations (achievementConfigurations.list).
// Read-only. --output json passes AchievementConfigurationListResponse through
// verbatim, including nextPageToken (ADR-0003). Ships [experimental] (ADR-0033).
package list

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/games/gamescmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/games"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	ApplicationID string
	MaxResults    int
	PageToken     string
	Columns       string
}

// Payload renders the achievements as a table / verbatim JSON.
type Payload struct {
	Rows []gamescmd.AchievementRow
	Cols []output.Column[gamescmd.AchievementRow]
	Raw  json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, p.Cols, p.Rows) },
		JSON:     func(w io.Writer) error { _, err := w.Write(p.Raw); return err },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, p.Cols, p.Rows) },
	}
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if in.MaxResults < 0 {
		return nil, gamescmd.Usagef("invalid --max-results: must be >= 0")
	}
	cols, err := gamescmd.ResolveAchievementColumns(in.Columns)
	if err != nil {
		return nil, err
	}
	appID, err := gamescmd.ResolveApplicationID(in.ApplicationID)
	if err != nil {
		return nil, err
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	lr, raw, err := games.ListAchievements(rc.Ctx, httpClient, appID, in.MaxResults, in.PageToken)
	if err != nil {
		return nil, gamescmd.Classify(appID, err)
	}
	return Payload{Rows: gamescmd.BuildAchievementRows(lr.Items), Cols: cols, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay games achievements list`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List a game's achievement configurations",
		Long: `List the achievement configurations for a Play Games Services application.

Addressing rides the numeric Play Games application ID (--application-id): a
distinct ID space from the Android package (ADR-0033). Use --max-results and
--page-token to page; --output json passes the
AchievementConfigurationListResponse through verbatim, including nextPageToken
(ADR-0003).`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.ApplicationID, "application-id", "", "numeric Play Games Services application ID (required)")
	cmd.Flags().IntVar(&in.MaxResults, "max-results", 0, "max configs per page (API default when unset)")
	cmd.Flags().StringVar(&in.PageToken, "page-token", "", "page token from a previous response's nextPageToken")
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns (default: "+gamescmd.DefaultAchievementColumns()+")")
	return cmd
}
