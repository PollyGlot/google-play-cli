package list

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/apihint"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/testers"
)

type Input struct {
	Package string
	Track   string
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
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

func renderTable(w io.Writer, p Payload) error {
	if _, err := fmt.Fprintf(w, "Track: %s\n", p.Track); err != nil {
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

func renderJSON(w io.Writer, p Payload) error {
	_, err := w.Write(p.Raw)
	return err
}

func renderMarkdown(w io.Writer, p Payload) error {
	if _, err := fmt.Fprintf(w, "Track: %s\n\n", p.Track); err != nil {
		return err
	}
	rows := make([][]string, 0, len(p.Groups))
	for _, g := range p.Groups {
		rows = append(rows, []string{g})
	}
	return output.MarkdownTable(w, []string{"GROUP"}, rows)
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

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	in.Track = strings.TrimSpace(in.Track)
	if in.Track == "" {
		return nil, exit.Usagef("missing --track: pass --track <name> (any closed-track name, e.g. alpha)")
	}

	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	var (
		parsed *testers.Testers
		raw    json.RawMessage
	)
	if err := edits.WithReadOnlyEdit(rc.Ctx, httpClient, pkg, func(editID string) error {
		tt, r, e := testers.Get(rc.Ctx, httpClient, pkg, editID, in.Track)
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
		Use:   "list",
		Short: "List the Google Groups authorized to test a track",
		Long: `List the authorized audience of --track: the Google Groups that may
test it. The testers resource exposes only Google Groups: individual
tester emails are not supported by the API, so this lists groups
exclusively.

Reads the audience inside a read-only Edit (open → testers.get → discard);
nothing is committed. Replacing the audience is the job of ` + "`gplay testers set`" + `.

--output json is the raw testers.get payload; --output markdown
renders a Markdown table.`,
		Example: `  gplay testers list --track alpha
  gplay testers list --track qa-team --output json`,
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
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.Track, "track", "", "track whose testers to list (any closed-track name) (required)")
	return cmd
}
