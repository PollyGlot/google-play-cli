// Package list implements `gplay reviews list`: a read-only listing of the
// user reviews the Google Play API exposes for a package. The API serves
// only the last 7 days (docs/DESIGN.md §5), so the command always prints a
// stderr warning to that effect. It is thin glue — resolve --package, build
// an authenticated client, consume internal/play/reviews.List (which owns
// auto-pagination), apply the client-side --stars filter and --limit cap,
// then render. No Edit, no mutation.
package list

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/reviews/reviewerr"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/reviews"
	"github.com/PollyGlot/google-play-cli/internal/reviews/filter"
)

// sevenDayWarning is printed to stderr on every successful invocation: the
// reviews API only exposes the last 7 days, so a quiet empty result would
// otherwise read as "no reviews" when it really means "none in the window".
const sevenDayWarning = "WARN: the Google Play reviews API only returns reviews from the last 7 days; older reviews are not available here (see docs/BACKLOG.md for long-history CSV reports)."

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package string
	Stars   string // --stars selector (e.g. "1", "1-2", "1,3,5"); "" = no filter
	Limit   int    // --limit cap on the final count; 0 = no cap
	Columns string // --columns override; "" = DefaultColumns
}

// usageError is a CLI-misuse error (no package, bad --stars, unknown
// column); ExitCode()=2 per docs/DESIGN.md §9.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func (e *usageError) ExitCode() int { return 2 }

// authError signals that no credential resolved for a call that needs one;
// ExitCode()=10 per docs/DESIGN.md §9.
type authError struct{ msg string }

func (e *authError) Error() string { return e.msg }

func (e *authError) ExitCode() int { return 10 }

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

// DefaultColumns is the table/markdown column order applied when --columns is
// unset. Documented in the command's --help.
var DefaultColumns = []string{colDate, colStars, colLocale, colReviewID, colSummary}

// maxSummaryLen caps the SUMMARY cell so a long comment does not blow out the
// table; the first line is taken, then truncated with an ellipsis.
const maxSummaryLen = 60

// dateLayout renders a review's last-modified instant compactly in UTC.
const dateLayout = "2006-01-02 15:04"

// columnDef pairs a column's header with the extractor that turns a review
// into that column's cell value.
type columnDef struct {
	header string
	value  func(reviews.Review) string
}

// columnRegistry is the single source of truth for which columns exist.
var columnRegistry = map[string]columnDef{
	colDate:     {"DATE", func(r reviews.Review) string { return formatDate(r.LastModified()) }},
	colStars:    {"STARS", func(r reviews.Review) string { return strconv.Itoa(r.Stars()) }},
	colLocale:   {"LOCALE", func(r reviews.Review) string { return r.Locale() }},
	colReviewID: {"REVIEW_ID", func(r reviews.Review) string { return r.ReviewID }},
	colSummary:  {"SUMMARY", func(r reviews.Review) string { return summary(r.Text()) }},
}

// formatDate renders t in UTC, or "" for the zero Time (no user comment / no
// timestamp).
func formatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(dateLayout)
}

// summary is the first line of a comment, truncated to maxSummaryLen runes
// with an ellipsis — the table-friendly digest of the latest comment.
func summary(text string) string {
	line := text
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	runes := []rune(line)
	if len(runes) > maxSummaryLen {
		return string(runes[:maxSummaryLen]) + "…"
	}
	return line
}

// Payload satisfies output.Renderable. Reviews is the (filtered, capped) set
// to render; each review keeps its verbatim JSON for the ADR-0003 JSON
// pass-through. Columns is the resolved, validated display order.
type Payload struct {
	Reviews []reviews.Review
	Columns []string
}

// Renderers satisfies output.Renderable with one renderer per Format. The
// JSON form re-emits the surviving reviews under the API's {"reviews":[...]}
// envelope (ADR-0003); table and markdown are the human-shaped views.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

// headers returns the selected columns' display headers, in order.
func (p Payload) headers() []string {
	h := make([]string, len(p.Columns))
	for i, k := range p.Columns {
		h[i] = columnRegistry[k].header
	}
	return h
}

// row extracts the selected columns' cells for one review. An unknown key
// (possible only via a directly-constructed Payload — Run validates
// --columns) renders an empty cell rather than calling a nil value func.
func (p Payload) row(r reviews.Review) []string {
	cells := make([]string, len(p.Columns))
	for i, k := range p.Columns {
		if def, ok := columnRegistry[k]; ok {
			cells[i] = def.value(r)
		}
	}
	return cells
}

// renderTable writes a tab-aligned table of the selected columns, one row per
// review.
func renderTable(w io.Writer, p Payload) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, strings.Join(p.headers(), "\t")); err != nil {
		return err
	}
	for _, r := range p.Reviews {
		if _, err := fmt.Fprintln(tw, strings.Join(p.row(r), "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// renderMarkdown writes the selected columns as a GitHub-Flavored Markdown
// table via output.MarkdownTable.
func renderMarkdown(w io.Writer, p Payload) error {
	rows := make([][]string, 0, len(p.Reviews))
	for _, r := range p.Reviews {
		rows = append(rows, p.row(r))
	}
	return output.MarkdownTable(w, p.headers(), rows)
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

// resolveColumns turns the --columns spec into a validated, ordered list of
// canonical column keys. An empty spec yields DefaultColumns; an unknown key
// is a CLI misuse (exit 2).
func resolveColumns(spec string) ([]string, error) {
	if strings.TrimSpace(spec) == "" {
		return DefaultColumns, nil
	}
	parts := strings.Split(spec, ",")
	cols := make([]string, 0, len(parts))
	for _, p := range parts {
		k := strings.TrimSpace(p)
		if k == "" {
			continue
		}
		if _, ok := columnRegistry[k]; !ok {
			return nil, &usageError{msg: fmt.Sprintf("unknown column %q (valid: %s)", k, strings.Join(DefaultColumns, ", "))}
		}
		cols = append(cols, k)
	}
	if len(cols) == 0 {
		return nil, &usageError{msg: "no valid columns in --columns"}
	}
	return cols, nil
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
		return nil, &usageError{msg: "no package — pass --package <pkg> or run gplay init in your repo"}
	}

	sel, err := filter.Parse(in.Stars)
	if err != nil {
		return nil, &usageError{msg: err.Error() + "; " + filter.Hint}
	}

	if in.Limit < 0 {
		return nil, &usageError{msg: "invalid --limit: must be >= 0 (0 means no cap)"}
	}

	cols, err := resolveColumns(in.Columns)
	if err != nil {
		return nil, err
	}

	if rc.Account == nil {
		return nil, &authError{msg: "no Account resolved; run gplay auth login or set GPLAY_SERVICE_ACCOUNT"}
	}
	ts, err := token.Source(rc.Ctx, rc.Account)
	if err != nil {
		return nil, &authError{msg: "could not build token source: " + err.Error()}
	}
	base := baseHTTP(rc)
	ctx := context.WithValue(rc.Ctx, oauth2.HTTPClient, base)
	httpClient := oauth2.NewClient(ctx, ts)

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

// baseHTTP exposes the test seam (ctx's oauth2.HTTPClient) so a single
// injected RoundTripper covers both the /token exchange and the
// androidpublisher calls. Mirrors tracks/releases list.
func baseHTTP(rc *kernel.RunContext) *http.Client {
	if v := rc.Ctx.Value(oauth2.HTTPClient); v != nil {
		if c, ok := v.(*http.Client); ok && c != nil {
			return c
		}
	}
	return http.DefaultClient
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
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns to show (default: date,stars,locale,reviewId,summary)")
	return cmd
}
