package set

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/edits/commitflags"
	"github.com/PollyGlot/google-play-cli/internal/apihint"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/testers"
)

type Input struct {
	Package           string
	Track             string
	Groups            []string
	GroupsSet         bool
	Clear             bool
	DryRun            bool
	KeepEditOnFailure bool
	Commit            commitflags.Flags
}

type trackNotFoundError struct {
	track string
	cause error
}

func (e *trackNotFoundError) Error() string {
	return fmt.Sprintf("track %q not found: run `gplay tracks list` to see the tracks configured for this app: %v", e.track, e.cause)
}

func (e *trackNotFoundError) Unwrap() error { return e.cause }

type Payload struct {
	Track  string          `json:"-"`
	Groups []string        `json:"-"`
	Raw    json.RawMessage `json:"-"`
	DryRun bool            `json:"-"`
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

func renderTable(w io.Writer, p Payload) error {
	if _, err := fmt.Fprintf(w, "Track: %s%s\n", p.Track, dryRunSuffix(p.DryRun)); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "GROUP"); err != nil {
		return err
	}
	if len(p.Groups) == 0 {
		if _, err := fmt.Fprintln(tw, "(no testers)"); err != nil {
			return err
		}
	}
	for _, g := range p.Groups {
		if _, err := fmt.Fprintln(tw, g); err != nil {
			return err
		}
	}
	return tw.Flush()
}

type dryRunView struct {
	DryRun       bool     `json:"dryRun"`
	Track        string   `json:"track"`
	GoogleGroups []string `json:"googleGroups"`
}

func renderJSON(w io.Writer, p Payload) error {
	if p.DryRun {
		return output.WriteJSON(w, dryRunView{DryRun: true, Track: p.Track, GoogleGroups: output.NonNil(p.Groups)})
	}
	_, err := w.Write(p.Raw)
	return err
}

func renderMarkdown(w io.Writer, p Payload) error {
	if _, err := fmt.Fprintf(w, "Track: %s%s\n\n", p.Track, dryRunSuffix(p.DryRun)); err != nil {
		return err
	}
	rows := make([][]string, 0, len(p.Groups))
	for _, g := range p.Groups {
		rows = append(rows, []string{g})
	}
	return output.MarkdownTable(w, []string{"GROUP"}, rows)
}

func dryRunSuffix(dryRun bool) string {
	if dryRun {
		return " (dry-run)"
	}
	return ""
}

func isStatus(err error, status int) bool {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == status
	}
	return false
}

func classifyTrackError(pkg string, err error) error {
	var tnf *trackNotFoundError
	if errors.As(err, &tnf) {
		return err
	}
	return apihint.ForPackage(pkg, err)
}

func targetGroups(in Input) []string {
	if in.Clear {
		return []string{}
	}
	groups := make([]string, 0, len(in.Groups))
	for _, g := range in.Groups {
		if g = strings.TrimSpace(g); g != "" {
			groups = append(groups, g)
		}
	}
	return groups
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	in.Track = strings.TrimSpace(in.Track)
	if in.Track == "" {
		return nil, exit.Usagef("missing --track: pass --track <name> (any closed-track name, e.g. alpha)")
	}

	if !in.GroupsSet && !in.Clear {
		return nil, &exit.UsageError{Msg: "refusing to change testers without --group or --clear (a forgotten --group must not silently wipe the list); pass --group a@googlegroups.com[,…] to declare the set, or --clear to empty it"}
	}
	if in.GroupsSet && in.Clear {
		return nil, &exit.UsageError{Msg: "--group and --clear are mutually exclusive"}
	}

	groups := targetGroups(in)
	if in.GroupsSet && len(groups) == 0 {
		return nil, &exit.UsageError{Msg: "--group was passed but lists no non-empty group; pass --group a@googlegroups.com[,…], or --clear to empty the list"}
	}

	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}

	if in.DryRun {
		return Payload{
			Track:  in.Track,
			Groups: groups,
			DryRun: true,
		}, nil
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	explicitEditID, err := rc.ExplicitEditID(pkg)
	if err != nil {
		return nil, err
	}

	var (
		parsed *testers.Testers
		raw    json.RawMessage
	)
	if err := edits.WithEdit(rc.Ctx, httpClient, pkg, edits.Options{KeepOnFailure: in.KeepEditOnFailure, ExplicitEditID: explicitEditID, Commit: in.Commit.For(rc, explicitEditID)}, func(editID string) error {
		tt, r, e := testers.Update(rc.Ctx, httpClient, pkg, editID, in.Track, groups)
		if e != nil {
			if isStatus(e, http.StatusNotFound) {
				return &trackNotFoundError{track: in.Track, cause: e}
			}
			return e
		}
		parsed, raw = tt, r
		return nil
	}); err != nil {
		return nil, classifyTrackError(pkg, err)
	}

	rc.ConfirmMutation(explicitEditID, "testers set on track %q (%d group(s))", in.Track, len(parsed.GoogleGroups))
	return Payload{
		Track:  in.Track,
		Groups: parsed.GoogleGroups,
		Raw:    raw,
	}, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Replace the Google Groups authorized to test a track",
		Long: `Replace the authorized audience of --track with the Google Groups passed
to --group. testers set is declarative: it replaces the WHOLE list (it
maps 1:1 to testers.update: there is no add/remove). The testers resource
exposes only Google Groups; individual tester emails are not supported by
the API.

A bare ` + "`set`" + ` with neither --group nor --clear is refused (exit 2) so a
forgotten --group can never silently wipe the list; empty the audience on
purpose with --clear. There is no --confirm: a closed test track is
low-stakes and reversible.

Writes inside an implicit Edit (open → testers.update → commit). Use
--dry-run to validate and preview the payload without any HTTP call, and
--keep-edit-on-failure to skip the auto-discard cleanup for debugging.`,
		Example: `  # Open the alpha track to two Google Groups (replaces the whole list)
  gplay testers set --track alpha --group qa@example.com,beta-testers@googlegroups.com

  # Preview the new audience without any HTTP call
  gplay testers set --track qa-team --group qa@example.com --dry-run

  # Close the test: empty the audience on purpose
  gplay testers set --track qa-team --clear`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			b := boot
			b.Stdout = cmd.OutOrStdout()
			b.Stderr = cmd.ErrOrStderr()
			in.GroupsSet = cmd.Flags().Changed("group")
			return kernel.Run(b, kernel.FromCobra(cmd, outputFlag), func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.Track, "track", "", "track whose testers to replace (any closed-track name) (required)")
	cmd.Flags().StringSliceVar(&in.Groups, "group", nil, "Google Group email(s) authorized to test (repeatable or comma-separated)")
	cmd.Flags().BoolVar(&in.Clear, "clear", false, "replace the tester list with an empty set (close the closed test)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "validate inputs and preview the tester list without any HTTP call")
	cmd.Flags().BoolVar(&in.KeepEditOnFailure, "keep-edit-on-failure", false, "skip the auto-discard cleanup on failure (debug)")
	commitflags.Register(cmd, &in.Commit)
	return cmd
}
