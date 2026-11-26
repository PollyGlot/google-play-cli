package availability

import (
	"encoding/json"
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
	"github.com/PollyGlot/google-play-cli/internal/play/countryavailability"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
)

type Input struct {
	Package string
	Track   string
}

type notFoundError struct {
	track string
	cause error
}

func (e *notFoundError) Error() string {
	return fmt.Sprintf("track %q or package not found: run `gplay tracks list` to see the tracks configured for this app: %v", e.track, e.cause)
}

func (e *notFoundError) Unwrap() error { return e.cause }

func classifyAvailabilityError(pkg, track string, err error) error {
	if apihint.Status(err) == http.StatusNotFound {
		return &notFoundError{track: track, cause: err}
	}
	return apihint.Forbidden(pkg, err)
}

type Payload struct {
	Track              string          `json:"-"`
	SyncWithProduction bool            `json:"-"`
	RestOfWorld        bool            `json:"-"`
	Countries          []string        `json:"-"`
	Raw                json.RawMessage `json:"-"`
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

func countriesCell(codes []string) string {
	if len(codes) == 0 {
		return "(none)"
	}
	return strings.Join(codes, ", ")
}

func renderTable(w io.Writer, p Payload) error {
	if _, err := fmt.Fprintf(w, "Track: %s\n", p.Track); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	rows := [][2]string{
		{"SYNC_WITH_PRODUCTION", fmt.Sprintf("%t", p.SyncWithProduction)},
		{"REST_OF_WORLD", fmt.Sprintf("%t", p.RestOfWorld)},
		{"COUNTRIES", countriesCell(p.Countries)},
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(tw, "%s\t%s\n", r[0], r[1]); err != nil {
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
	_, err := fmt.Fprintf(w,
		"Track: %s\n\n- **Sync with production**: %t\n- **Rest of world**: %t\n- **Countries**: %s\n",
		p.Track, p.SyncWithProduction, p.RestOfWorld, countriesCell(p.Countries))
	return err
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	track := strings.TrimSpace(in.Track)
	if track == "" {
		return nil, &exit.UsageError{Msg: "missing --track: Country availability is keyed by track; pass --track <name> (e.g. production)"}
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
		parsed *countryavailability.TrackCountryAvailability
		raw    json.RawMessage
	)
	if err := edits.WithReadOnlyEdit(rc.Ctx, httpClient, pkg, func(editID string) error {
		ca, r, e := countryavailability.Get(rc.Ctx, httpClient, pkg, editID, track)
		if e != nil {
			return e
		}
		parsed, raw = ca, r
		return nil
	}); err != nil {
		return nil, classifyAvailabilityError(pkg, track, err)
	}

	codes := make([]string, 0, len(parsed.Countries))
	for _, c := range parsed.Countries {
		codes = append(codes, c.CountryCode)
	}
	return Payload{
		Track:              track,
		SyncWithProduction: parsed.SyncWithProduction,
		RestOfWorld:        parsed.RestOfWorld,
		Countries:          codes,
		Raw:                raw,
	}, nil
}

func NewViewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "view",
		Short: "Show the Country availability of a track (read-only)",
		Long: `Show which countries an app's artifacts are distributed to on --track:
syncWithProduction, restOfWorld, and the list of targeted countries (CLDR
two-letter codes).

Country availability is read-only at the Developer API level and keyed by
track, so --track is REQUIRED (there is no implicit production default)
and gplay never writes it. Reads inside a read-only Edit (open →
countryavailability.get → discard); nothing is committed. To CHANGE where
an app is available, use the Play Console.

--output json returns the edits.countryavailability.get body verbatim (a
clean pass-through: a single endpoint is read).`,
		Example: `  gplay tracks availability view --track production
  gplay tracks availability view --track beta --output json`,
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
	cmd.Flags().StringVar(&in.Track, "track", "", "track whose Country availability to read (required; e.g. production)")
	return cmd
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "availability",
		Short: "Read a track's Country availability (read-only)",
		Long: `Group for a track's Country availability, which countries an app's
artifacts are distributed to on a track.

` + "`tracks availability view`" + ` reads it. The resource is read-only at the
Developer API level, so there is no writer; to CHANGE where an app is
available, use the Play Console. The bare ` + "`tracks availability`" + ` command
prints this help.`,
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(NewViewCommand(boot))
	return cmd
}
