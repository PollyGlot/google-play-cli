// Package list implements `gplay reviews list`: a read-only listing of the
// user reviews the Google Play API exposes for a package. The API serves
// only the last 7 days (docs/DESIGN.md §5), so the command always prints a
// stderr warning to that effect. It is thin glue: resolve --package, build
// an authenticated client, consume internal/play/reviews.List (which owns
// auto-pagination), apply the client-side --stars filter and --limit cap,
// then render. No Edit, no mutation.
package list

import (
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/reviews/reviewerr"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/reviews"
	"github.com/PollyGlot/google-play-cli/internal/reviews/filter"
)

// sevenDayWarning is printed to stderr on every successful invocation: the
// reviews API only exposes the last 7 days, so a quiet empty result would
// otherwise read as "no reviews" when it really means "none in the window".
const sevenDayWarning = "WARN: the Google Play reviews API only returns reviews from the last 7 days; older reviews are not available here: use `gplay reviews history` for the full history from the monthly CSV reports."

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package string
	Stars   string // --stars selector (e.g. "1", "1-2", "1,3,5"); "" = no filter
	Limit   int    // --limit cap on the final count; 0 = no cap
	Columns string // --columns override; "" = the default set
}

// Canonical --columns keys. reviewId and stars/locale match the API
// vocabulary (per ADR-0003 the JSON view is pass-through) so an operator who
// has seen the JSON can name the same columns; date and summary are
// gplay-derived presentation of the latest user comment.
const (
	colDate     = "date"
	colStars    = "stars"
	colLocale   = "locale"
	colReviewID = "reviewId"
	colSummary  = "summary"
)

// columns is the single source of truth for the reviews table: its
// declaration order is both the set of valid --columns keys and the default
// column order. The shared output.ColumnSet owns selection (--columns
// parsing) and the table/markdown rendering once shared across list commands
// (see docs/adr/0018-shared-list-table-machinery.md).
var columns = output.NewColumnSet(
	output.Column[reviews.Review]{Key: colDate, Header: "DATE", Value: func(r reviews.Review) string { return formatDate(r.LastModified()) }},
	output.Column[reviews.Review]{Key: colStars, Header: "STARS", Value: func(r reviews.Review) string { return strconv.Itoa(r.Stars()) }},
	output.Column[reviews.Review]{Key: colLocale, Header: "LOCALE", Value: func(r reviews.Review) string { return r.Locale() }},
	output.Column[reviews.Review]{Key: colReviewID, Header: "REVIEW_ID", Value: func(r reviews.Review) string { return r.ReviewID }},
	output.Column[reviews.Review]{Key: colSummary, Header: "SUMMARY", Value: func(r reviews.Review) string { return summary(r.Text()) }},
)

// maxSummaryLen caps the SUMMARY cell so a long comment does not blow out the
// table; the first line is taken, then truncated with an ellipsis.
const maxSummaryLen = 60

// dateLayout renders a review's last-modified instant compactly in UTC.
const dateLayout = "2006-01-02 15:04"

// formatDate renders t in UTC, or "" for the zero Time (no user comment / no
// timestamp).
func formatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(dateLayout)
}

// summary is a one-line, table-friendly digest of a comment: the first line
// that carries visible text (so a comment opening with a blank line is not
// summarized as empty), with interior whitespace: tabs, stray carriage
// returns, runs of spaces: collapsed to single spaces so the cell cannot
// inject an extra tabwriter column or break a Markdown row, then truncated
// to maxSummaryLen runes with an ellipsis.
func summary(text string) string {
	line := strings.Join(strings.Fields(firstNonEmptyLine(text)), " ")
	runes := []rune(line)
	if len(runes) > maxSummaryLen {
		return string(runes[:maxSummaryLen]) + "…"
	}
	return line
}

// firstNonEmptyLine returns the first line of text whose content is not
// blank/whitespace-only, or "" when every line is blank.
func firstNonEmptyLine(text string) string {
	for _, ln := range strings.Split(text, "\n") {
		if strings.TrimSpace(ln) != "" {
			return ln
		}
	}
	return ""
}

// Payload satisfies output.Renderable. Reviews is the (filtered, capped) set
// to render; each review keeps its verbatim JSON for the ADR-0003 JSON
// pass-through. Columns is the resolved, validated display order.
type Payload struct {
	Reviews []reviews.Review
	Columns []output.Column[reviews.Review]
}

// Renderers satisfies output.Renderable with one renderer per Format. The
// JSON form re-emits the surviving reviews under the API's {"reviews":[...]}
// envelope (ADR-0003); table and markdown are the human-shaped views drawn
// by the shared column helper.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, p.Columns, p.Reviews) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, p.Columns, p.Reviews) },
	}
}

// renderJSON wraps the surviving reviews' verbatim JSON objects in the
// {"reviews":[...]} envelope. After pagination + filtering there is no single
// upstream body to echo, so we rebuild the envelope from each review's
// preserved Raw rather than reshaping fields.
func renderJSON(w io.Writer, p Payload) error {
	raws := make([]json.RawMessage, 0, len(p.Reviews))
	for _, r := range p.Reviews {
		raws = append(raws, r.Raw)
	}
	return output.WriteJSON(w, struct {
		Reviews []json.RawMessage `json:"reviews"`
	}{Reviews: raws})
}

// Run is the business function the kernel invokes. It resolves the package,
// builds an authenticated client, lists every review (auto-paginated), and
// always warns about the 7-day window.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg := in.Package
	if pkg == "" && rc.Resolved != nil {
		pkg = rc.Resolved.Pin
	}
	if pkg == "" {
		return nil, exit.Usagef("no package: pass --package <pkg> or run gplay init in your repo")
	}

	sel, err := filter.Parse(in.Stars)
	if err != nil {
		return nil, exit.Usagef("%s; %s", err, filter.Hint)
	}

	if in.Limit < 0 {
		return nil, exit.Usagef("invalid --limit: must be >= 0 (0 means no cap)")
	}

	cols, err := columns.Resolve(in.Columns)
	if err != nil {
		return nil, err
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	all, err := reviews.List(rc.Ctx, httpClient, pkg)
	if err != nil {
		return nil, reviewerr.Classify(pkg, err)
	}

	kept := make([]reviews.Review, 0, len(all))
	for _, r := range all {
		if sel.Matches(r.Stars()) {
			kept = append(kept, r)
		}
	}
	// --limit caps the final count, applied after pagination and filtering.
	if in.Limit > 0 && len(kept) > in.Limit {
		kept = kept[:in.Limit]
	}

	// Always warn: an empty result inside the 7-day window must not read as
	// "this app has no reviews".
	warn(rc)

	return Payload{Reviews: kept, Columns: cols}, nil
}

// warn prints the 7-day window notice to stderr.
func warn(rc *kernel.RunContext) {
	if rc.Stderr != nil {
		_, _ = io.WriteString(rc.Stderr, sevenDayWarning+"\n")
	}
}

// NewCommand returns the cobra command for `gplay reviews list`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent user reviews for a package (last 7 days)",
		Long: `List the user reviews the Google Play API exposes for --package.

The API only returns reviews from the LAST 7 DAYS; a WARN line to that
effect is always printed to stderr (long-history retrieval via GCS CSV
reports is on the backlog). Results are auto-paginated until exhausted.

--stars filters client-side and accepts a single rating (1), an inclusive
range (1-2), or a set (1,3,5); each rating must be 1..5. --limit N caps the
final count after filtering (0 = no cap).

Default table columns: date, stars, locale, reviewId, summary. Override with
--columns stars,reviewId,...  (--output json is the {"reviews":[...]}
pass-through reflecting the filtered set; --output markdown renders a
Markdown table.)`,
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
	cmd.Flags().StringVar(&in.Stars, "stars", "", "client-side star filter: 1, 1-2, or 1,3,5 (each rating 1..5)")
	cmd.Flags().IntVar(&in.Limit, "limit", 0, "cap the result count after filtering (0 = no cap)")
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns to show (default: "+strings.Join(columns.DefaultKeys(), ",")+")")
	return cmd
}
