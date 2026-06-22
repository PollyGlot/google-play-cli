// Package view implements `gplay reviews view <reviewId>`: a read-only, deep
// view of ONE user review, addressed by reviewId — the `reviews` analogue of
// `apps view` / `team users view`. The human views show a scalar header
// (author, star rating, date, locale, device, app version, reviewId) then the
// user↔developer conversation thread: the review body followed by any developer
// replies with their last-modified instant. --output json is the matched Review
// object verbatim (ADR-0003 pass-through), not a gplay envelope.
//
// Like `reviews list`/`reply` it is thin glue — resolve --package, build an
// authenticated client, call internal/play/reviews.Get, and render. No Edit, no
// mutation. The reviewId comes from the REVIEW_ID column of `reviews list` and
// is symmetric with `reviews reply <reviewId>`. A 404 (unknown id, OR a valid
// id whose review has fallen out of the API's 7-day window) is surfaced as a
// not-found (exit 30) with a hint naming that window, via commands/reviews/
// reviewerr (the 403 grant hint is shared with `reviews list`/`reply`).
//
// `reviews.get` exposes a translationLanguage query param, but `reviews list`
// does not wire it either; it is omitted here to stay symmetric and minimal
// (deferred — noted in --help).
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

// Input is the request-shaped struct cobra builds from the positional reviewId
// plus the --package flag.
type Input struct {
	Package  string
	ReviewID string
}

// dateLayout renders an instant compactly in UTC, matching `reviews list`.
const dateLayout = "2006-01-02 15:04"

// formatDate renders t in UTC, or "" for the zero Time (no timestamp).
func formatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(dateLayout)
}

// Payload satisfies output.Renderable. Raw carries the matched Review's
// verbatim bytes for the ADR-0003 JSON pass-through; the typed fields drive the
// human-shaped table and markdown views. None of the typed fields are
// json-tagged — the JSON path is the raw pass-through, never the struct.
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

// headerRows is the scalar header shared by the table and markdown views: the
// review's identifying fields in a fixed order. Empty values are kept (an
// absent author/device reads as a blank cell, not a dropped row).
func headerRows(r reviews.Review) [][2]string {
	return [][2]string{
		{"AUTHOR", r.Author()},
		{"STARS", strconv.Itoa(r.Stars())},
		{"DATE", formatDate(r.LastModified())},
		{"LOCALE", r.Locale()},
		{"DEVICE", r.Device()},
		{"APP_VERSION", r.AppVersion()},
		{"REVIEW_ID", r.ReviewID},
	}
}

// renderTable writes the scalar header as `FIELD<TAB>VALUE` lines (like
// `apps view`/`team users view`), then the conversation thread: the review body
// under a "Review:" label, followed by each developer reply under a
// "Developer reply (<date>):" label. The thread is the point of a deep view, so
// the full bodies are printed verbatim (not truncated like the list summary).
func renderTable(w io.Writer, r reviews.Review) error {
	for _, row := range headerRows(r) {
		if _, err := fmt.Fprintf(w, "%s\t%s\n", row[0], row[1]); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "\nReview:\n%s\n", r.Text()); err != nil {
		return err
	}
	for _, dc := range r.DeveloperReplies() {
		when := formatDate(dc.LastModified.Time())
		label := "Developer reply:"
		if when != "" {
			label = fmt.Sprintf("Developer reply (%s):", when)
		}
		if _, err := fmt.Fprintf(w, "\n%s\n%s\n", label, dc.Text); err != nil {
			return err
		}
	}
	return nil
}

// renderJSON emits the matched Review's bytes verbatim (ADR-0003 pass-through).
// Raw is always populated on the Run path; an empty Raw would mean we never
// captured the API body (every Payload field is json:"-"), so we error rather
// than emit zero bytes.
func renderJSON(w io.Writer, p Payload) error {
	if len(p.Raw) == 0 {
		return fmt.Errorf("missing raw reviews.get payload for --output json")
	}
	_, err := w.Write(p.Raw)
	return err
}

// renderMarkdown renders the review as a record: a level-2 heading then a
// `- **Field**: value` list (docs/DESIGN.md §7), followed by the review body and
// any developer replies as blockquotes, so a pasted report stands alone.
func renderMarkdown(w io.Writer, r reviews.Review) error {
	heading := "Review"
	if r.Author() != "" {
		heading = "Review by " + r.Author()
	}
	if _, err := fmt.Fprintf(w, "## %s\n\n", heading); err != nil {
		return err
	}
	// Skip AUTHOR in the list (it is the heading); show the rest.
	for _, row := range headerRows(r)[1:] {
		if _, err := fmt.Fprintf(w, "- **%s**: %s\n", mdLabel(row[0]), row[1]); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "\n%s\n", blockquote(r.Text())); err != nil {
		return err
	}
	for _, dc := range r.DeveloperReplies() {
		when := formatDate(dc.LastModified.Time())
		hdr := "**Developer reply**"
		if when != "" {
			hdr = fmt.Sprintf("**Developer reply** (%s)", when)
		}
		if _, err := fmt.Fprintf(w, "\n%s\n\n%s\n", hdr, blockquote(dc.Text)); err != nil {
			return err
		}
	}
	return nil
}

// mdLabel turns a SCREAMING_SNAKE header key into a human "Title case" label
// for the markdown list (REVIEW_ID → "Review id", APP_VERSION → "App version").
func mdLabel(key string) string {
	words := strings.Split(strings.ToLower(key), "_")
	if len(words) > 0 && words[0] != "" {
		words[0] = strings.ToUpper(words[0][:1]) + words[0][1:]
	}
	return strings.Join(words, " ")
}

// blockquote renders text as a GFM blockquote, prefixing every line with "> "
// so multi-line review bodies stay inside the quote. An empty body becomes a
// single "> " marker rather than a dangling blank line.
func blockquote(text string) string {
	lines := strings.Split(text, "\n")
	for i, ln := range lines {
		lines[i] = "> " + ln
	}
	return strings.Join(lines, "\n")
}

// Run is the business function the kernel invokes. It validates the reviewId,
// resolves the package, builds an authenticated client, fetches the single
// review (reviews.get), and renders. A 404 is a not-found (exit 30) naming the
// 7-day window.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	reviewID := strings.TrimSpace(in.ReviewID)
	if reviewID == "" {
		return nil, exit.Usagef("no review — pass a reviewId: gplay reviews view <reviewId>")
	}

	pkg := in.Package
	if pkg == "" && rc.Resolved != nil {
		pkg = rc.Resolved.Pin
	}
	if pkg == "" {
		return nil, exit.Usagef("no package — pass --package <pkg> or run gplay init in your repo")
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

// NewCommand returns the cobra command for `gplay reviews view <reviewId>`.
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
conversation thread — the review body followed by any developer replies with
their last-modified date.

The reviewId comes from the REVIEW_ID column of ` + "`gplay reviews list`" + ` and is
the same id ` + "`gplay reviews reply <reviewId>`" + ` takes. The package defaults to the
repo's .gplay/config.json pin when --package is omitted.

The reviews API only exposes the LAST 7 DAYS, so an unknown OR expired
reviewId fails with exit 30 (a valid id can become unfetchable once its review
ages out of the window). There is no --translate flag: ` + "`reviews.get`" + ` exposes a
translationLanguage param, but ` + "`reviews list`" + ` does not wire it either, so it is
omitted here for symmetry (deferred — may be added later if requested).

--output json is the Review object verbatim (ADR-0003 pass-through); --output
markdown renders a record plus the thread as blockquotes.`,
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
