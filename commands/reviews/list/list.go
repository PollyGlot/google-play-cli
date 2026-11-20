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

const sevenDayWarning = "the Google Play reviews API only returns reviews from the last 7 days; older reviews are not available here: use `gplay reviews history` for the full history from the monthly CSV reports."

type Input struct {
	Package string
	Stars   string
	Limit   int
	Columns string
}

const (
	colDate     = "date"
	colStars    = "stars"
	colLocale   = "locale"
	colReviewID = "reviewId"
	colSummary  = "summary"
)

var columns = output.NewColumnSet(
	output.Column[reviews.Review]{Key: colDate, Header: "DATE", Value: func(r reviews.Review) string { return formatDate(r.LastModified()) }},
	output.Column[reviews.Review]{Key: colStars, Header: "STARS", Value: func(r reviews.Review) string { return strconv.Itoa(r.Stars()) }},
	output.Column[reviews.Review]{Key: colLocale, Header: "LOCALE", Value: func(r reviews.Review) string { return r.Locale() }},
	output.Column[reviews.Review]{Key: colReviewID, Header: "REVIEW_ID", Value: func(r reviews.Review) string { return r.ReviewID }},
	output.Column[reviews.Review]{Key: colSummary, Header: "SUMMARY", Value: func(r reviews.Review) string { return summary(r.Text()) }},
)

const maxSummaryLen = 60

const dateLayout = "2006-01-02 15:04"

func formatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(dateLayout)
}

func summary(text string) string {
	line := strings.Join(strings.Fields(firstNonEmptyLine(text)), " ")
	runes := []rune(line)
	if len(runes) > maxSummaryLen {
		return string(runes[:maxSummaryLen]) + "…"
	}
	return line
}

func firstNonEmptyLine(text string) string {
	for _, ln := range strings.Split(text, "\n") {
		if strings.TrimSpace(ln) != "" {
			return ln
		}
	}
	return ""
}

type Payload struct {
	Reviews []reviews.Review
	Columns []output.Column[reviews.Review]
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, p.Columns, p.Reviews) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, p.Columns, p.Reviews) },
	}
}

func renderJSON(w io.Writer, p Payload) error {
	raws := make([]json.RawMessage, 0, len(p.Reviews))
	for _, r := range p.Reviews {
		raws = append(raws, r.Raw)
	}
	return output.WriteJSON(w, struct {
		Reviews []json.RawMessage `json:"reviews"`
	}{Reviews: raws})
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
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
	truncated := in.Limit > 0 && len(kept) > in.Limit
	if truncated {
		kept = kept[:in.Limit]
	}

	warn(rc)
	if truncated {
		rc.WarnTruncated(len(kept), "reviews", "limit")
	}

	return Payload{Reviews: kept, Columns: cols}, nil
}

func warn(rc *kernel.RunContext) {
	rc.Warnf("%s", sevenDayWarning)
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent user reviews for a package (last 7 days)",
		Long: `List the user reviews the Google Play API exposes for --package.

The API only returns reviews from the LAST 7 DAYS; a warning line to that
effect is always printed to stderr; for longer history use 'reviews history',
which reads the GCS CSV reports. Results are auto-paginated until exhausted.

--stars filters client-side and accepts a single rating (1), an inclusive
range (1-2), or a set (1,3,5); each rating must be 1..5. --limit N caps the
final count after filtering (0 = no cap).

Default table columns: date, stars, locale, reviewId, summary. Override with
--columns stars,reviewId,...  (--output json is the {"reviews":[...]}
pass-through reflecting the filtered set; --output markdown renders a
Markdown table.)`,
		Example: `  gplay reviews list

  # The worst reviews of the week, capped at 20
  gplay reviews list --stars 1-2 --limit 20

  gplay reviews list --stars 1,2,3 --output json`,
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
	cmd.Flags().IntVar(&in.Limit, "limit", 0, "cap the result count after filtering (0 = no cap); a capped list warns on stderr")
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns to show (default: "+strings.Join(columns.DefaultKeys(), ",")+")")
	return cmd
}
