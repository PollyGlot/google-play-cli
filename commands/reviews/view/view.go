package view

import (
	"encoding/json"
	"fmt"
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
)

type Input struct {
	Package  string
	ReviewID string
}

const dateLayout = "2006-01-02 15:04"

func formatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(dateLayout)
}

type Payload struct {
	Review reviews.Review  `json:"-"`
	Raw    json.RawMessage `json:"-"`
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p.Review) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p.Review) },
	}
}

func headerRows(r reviews.Review) [][2]string {
	return [][2]string{
		{"AUTHOR", output.SanitizeCell(r.Author())},
		{"STARS", strconv.Itoa(r.Stars())},
		{"DATE", formatDate(r.LastModified())},
		{"LOCALE", output.SanitizeCell(r.Locale())},
		{"DEVICE", output.SanitizeCell(r.Device())},
		{"APP_VERSION", output.SanitizeCell(r.AppVersion())},
		{"REVIEW_ID", output.SanitizeCell(r.ReviewID)},
	}
}

func renderTable(w io.Writer, r reviews.Review) error {
	for _, row := range headerRows(r) {
		if _, err := fmt.Fprintf(w, "%s\t%s\n", row[0], row[1]); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "\nReview:\n%s\n", output.SanitizeText(r.Text())); err != nil {
		return err
	}
	for _, dc := range r.DeveloperReplies() {
		when := formatDate(dc.LastModified.Time())
		label := "Developer reply:"
		if when != "" {
			label = fmt.Sprintf("Developer reply (%s):", when)
		}
		if _, err := fmt.Fprintf(w, "\n%s\n%s\n", label, output.SanitizeText(dc.Text)); err != nil {
			return err
		}
	}
	return nil
}

func renderJSON(w io.Writer, p Payload) error {
	_, err := w.Write(p.Raw)
	return err
}

func renderMarkdown(w io.Writer, r reviews.Review) error {
	heading := "Review"
	if author := output.SanitizeCell(r.Author()); author != "" {
		heading = "Review by " + author
	}
	if _, err := fmt.Fprintf(w, "## %s\n\n", heading); err != nil {
		return err
	}
	for _, row := range headerRows(r)[1:] {
		if _, err := fmt.Fprintf(w, "- **%s**: %s\n", mdLabel(row[0]), row[1]); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "\n%s\n", blockquote(output.SanitizeText(r.Text()))); err != nil {
		return err
	}
	for _, dc := range r.DeveloperReplies() {
		when := formatDate(dc.LastModified.Time())
		hdr := "**Developer reply**"
		if when != "" {
			hdr = fmt.Sprintf("**Developer reply** (%s)", when)
		}
		if _, err := fmt.Fprintf(w, "\n%s\n\n%s\n", hdr, blockquote(output.SanitizeText(dc.Text))); err != nil {
			return err
		}
	}
	return nil
}

func mdLabel(key string) string {
	words := strings.Split(strings.ToLower(key), "_")
	if len(words) > 0 && words[0] != "" {
		words[0] = strings.ToUpper(words[0][:1]) + words[0][1:]
	}
	return strings.Join(words, " ")
}

func blockquote(text string) string {
	lines := strings.Split(text, "\n")
	for i, ln := range lines {
		lines[i] = "> " + ln
	}
	return strings.Join(lines, "\n")
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	reviewID := strings.TrimSpace(in.ReviewID)
	if reviewID == "" {
		return nil, exit.Usagef("no review: pass a reviewId: gplay reviews view <reviewId>")
	}

	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	review, err := reviews.Get(rc.Ctx, httpClient, pkg, reviewID)
	if err != nil {
		return nil, reviewerr.ClassifyView(pkg, reviewID, err)
	}

	return Payload{Review: review, Raw: review.Raw}, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "view <reviewId>",
		Short: "Show one user review: header plus the user↔developer thread",
		Long: `Show a single user review, addressed by reviewId: a scalar header (author,
star rating, date, locale, device, app version, reviewId) then the
conversation thread: the review body followed by any developer replies with
their last-modified date.

The reviewId comes from the REVIEW_ID column of ` + "`gplay reviews list`" + ` and is
the same id ` + "`gplay reviews reply --review-id`" + ` takes. The package defaults to the
repo's .gplay/config.json pin when --package is omitted.

The reviews API only exposes the LAST 7 DAYS, so an unknown OR expired
reviewId fails with exit 30 (a valid id can become unfetchable once its review
ages out of the window). There is no --translate flag: ` + "`reviews.get`" + ` exposes a
translationLanguage param, but ` + "`reviews list`" + ` does not wire it either, so it is
omitted here for symmetry (deferred: may be added later if requested).

--output json is the Review object verbatim; --output
markdown renders a record plus the thread as blockquotes.`,
		Example: `  gplay reviews view gp:AOqpTOGx1bY2kLm
  gplay reviews view gp:AOqpTOGx1bY2kLm --output markdown
  gplay reviews view gp:AOqpTOGx1bY2kLm --output json`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in.ReviewID = args[0]
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	return cmd
}
