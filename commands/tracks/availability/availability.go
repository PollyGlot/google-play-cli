// Package availability implements `tracks availability`, a pure grouping
// noun over the Country availability of a single track, which
// countries an app's artifacts are distributed to on --track.
//
// Per ADR-0019 a read always carries a verb, so the bare `availability`
// only prints help and the read lives under `tracks availability view`.
// The read is thin glue: resolve --package, open a read-only Edit, consume
// internal/play/countryavailability.Get, and render. The Edit is opened
// and discarded via edits.WithReadOnlyEdit, never committed.
//
// The resource is read-only at the API level (no insert/patch/update) and
// keyed by TRACK, not by app, so --track is REQUIRED (no implicit
// production default) and gplay introduces no availability writer (the
// group holds only `view`, no `set`). See ADR-0012. A user who wants to
// CHANGE where an app is available is pointed at the Play Console.
package availability

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/countryavailability"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package string
	Track   string
}

// usageError is a CLI-misuse error (missing --track, no package);
// ExitCode()=2 per docs/DESIGN.md §9.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// notFoundError wraps a 404 (unknown track OR package) with a hint
// pointing at `gplay tracks list`. It carries no ExitCode of its own so
// the wrapped *api.Error (404 → exit 30) stays authoritative through the
// Coder chain. Availability is track-scoped, so a 404's most likely cause
// is a wrong/missing track: `gplay tracks list` is the right next step
// for both track and package misses.
type notFoundError struct {
	track string
	cause error
}

// Error renders the not-found message plus the `gplay tracks list` hint.
func (e *notFoundError) Error() string {
	return fmt.Sprintf("track %q or package not found: run `gplay tracks list` to see the tracks configured for this app: %v", e.track, e.cause)
}

// Unwrap exposes the underlying *api.Error so the Coder chain keeps
// mapping the 404 to exit 30.
func (e *notFoundError) Unwrap() error { return e.cause }

// forbiddenError wraps a 403 (service account not granted access on the
// app) with the standard grant-access hint. It carries no ExitCode of its
// own so the wrapped *api.Error (403 → exit 11) stays authoritative.
type forbiddenError struct {
	pkg   string
	cause error
}

// Error renders the forbidden message plus the Play Console grant hint.
func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account is not granted access to %q: in the Play Console, open Setup → API access and grant this service account permission on the app: %v", e.pkg, e.cause)
}

// Unwrap exposes the underlying *api.Error so the Coder chain keeps
// mapping the 403 to exit 11.
func (e *forbiddenError) Unwrap() error { return e.cause }

// classifyEditError attaches an actionable hint to the operator-facing
// failures of an availability read, while leaving the wrapped *api.Error
// to drive the exit code. A 404 (on edits.insert OR countryAvailability.get)
// means an unknown track or package (→ `gplay tracks list` hint); a 403
// means the service account lacks API access on the app. Every other
// failure (5xx, network, edit conflict) propagates verbatim.
func classifyEditError(pkg, track string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusNotFound:
			return &notFoundError{track: track, cause: err}
		case http.StatusForbidden:
			return &forbiddenError{pkg: pkg, cause: err}
		}
	}
	return err
}

// Payload satisfies output.Renderable. Raw carries the
// countryavailability.get body for the ADR-0003 JSON pass-through; Track
// is gplay-derived context shown above the human views (never in the
// JSON, which stays a faithful pass-through). Countries is the flattened
// list of CLDR codes for the human views.
type Payload struct {
	Track              string          `json:"-"`
	SyncWithProduction bool            `json:"-"`
	RestOfWorld        bool            `json:"-"`
	Countries          []string        `json:"-"`
	Raw                json.RawMessage `json:"-"`
}

// Renderers satisfies output.Renderable with one renderer per Format. The
// JSON form is the ADR-0003 countryavailability.get pass-through; table
// and markdown are human-shaped views over the same resource.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

// countriesCell joins the CLDR codes for the human views, rendering the
// empty set as `(none)` so an empty list reads unambiguously (an app that
// targets nowhere explicit, relying on restOfWorld).
func countriesCell(codes []string) string {
	if len(codes) == 0 {
		return "(none)"
	}
	return strings.Join(codes, ", ")
}

// renderTable writes a track-context header then a `FIELD  VALUE` table
// of the three availability fields.
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

// renderJSON emits the raw countryavailability.get body verbatim
// (ADR-0003 pass-through). Raw is always populated on the Run path; an
// empty Raw would mean we never captured the API body, so we error rather
// than emit zero bytes.
func renderJSON(w io.Writer, p Payload) error {
	if len(p.Raw) == 0 {
		return fmt.Errorf("missing raw countryavailability.get payload for --output json")
	}
	_, err := w.Write(p.Raw)
	return err
}

// renderMarkdown writes a `- **Field**: value` list per docs/DESIGN.md §7
// (single-record read), preceded by the same track-context line as the
// table view so a pasted report stands on its own.
func renderMarkdown(w io.Writer, p Payload) error {
	_, err := fmt.Fprintf(w,
		"Track: %s\n\n- **Sync with production**: %t\n- **Rest of world**: %t\n- **Countries**: %s\n",
		p.Track, p.SyncWithProduction, p.RestOfWorld, countriesCell(p.Countries))
	return err
}

// Run is the business function the kernel invokes. It validates that
// --track is present (REQUIRED: no implicit default), resolves the
// package, builds an authenticated HTTP client, then opens a read-only
// Edit and reads the track's Country availability.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	track := strings.TrimSpace(in.Track)
	if track == "" {
		return nil, &usageError{msg: "missing --track: Country availability is keyed by track; pass --track <name> (e.g. production)"}
	}

	pkg := strings.TrimSpace(in.Package)
	if pkg == "" && rc.Resolved != nil {
		pkg = strings.TrimSpace(rc.Resolved.Pin)
	}
	if pkg == "" {
		return nil, &usageError{msg: "no package: pass --package <pkg> or run gplay init in your repo"}
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
		return nil, classifyEditError(pkg, track, err)
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

// NewViewCommand returns the read command `tracks availability view`: it
// reads a track's Country availability. Per ADR-0019 the read carries an
// explicit verb: the bare `availability` group prints help.
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
clean ADR-0003 pass-through: a single endpoint is read).`,
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

// NewCommand returns the cobra group for `tracks availability`: a pure
// grouping noun (ADR-0019) holding only `view` (the resource is read-only at
// the Developer API level, so there is no `set`). kernel.GroupRunE makes the
// bare command print help while a mistyped subcommand fails loudly as CLI
// misuse (exit 2) rather than silently printing help with exit 0.
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
