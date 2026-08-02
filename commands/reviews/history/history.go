// Package history implements `gplay reviews history`: the full-history channel
// for user reviews, beyond `reviews list`'s 7-day API window. It reads the
// monthly reviews CSV report Google deposits in the developer's reporting
// bucket (gs://pubsite_prod_rev_<developerId>/reviews/reviews_<pkg>_YYYYMM.csv)
// over the GCS JSON API and renders the parsed rows (#94 / ADR-0037).
//
// A different data channel than `reviews list` (GCS objects vs Android
// Publisher), a different OAuth scope (devstorage.read_only, requested via
// kernel.WithScope), a different axis (--month) — so it is a sibling command,
// not a flag. Read-only by scope construction. Ships [experimental] (ADR-0010).
package history

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/gcs"
	"github.com/PollyGlot/google-play-cli/internal/reviews/history"
	"github.com/PollyGlot/google-play-cli/internal/team/addressing"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package     string
	Month       string // --month YYYY-MM; "" = the latest month present in the bucket
	From        string // --from YYYY-MM; range start (mutually exclusive with --month)
	To          string // --to YYYY-MM; range end (mutually exclusive with --month)
	Bucket      string // --bucket override; "" = derive from the developer-id
	DeveloperID string // --developer-id (feeds the default bucket name only)
	Columns     string // --columns override; "" = the default set
}

// Canonical --columns keys. They name the CSV report's documented columns
// (lowerCamel, matching the `--output json` field names) so an operator who has
// seen the JSON can select the same columns.
const (
	colDate    = "date"
	colStars   = "stars"
	colLocale  = "locale"
	colDevice  = "device"
	colVersion = "version"
	colTitle   = "title"
	colSummary = "summary"
	colReply   = "reply"
)

// columns is the single source of truth for the history table: its declaration
// order is both the set of valid --columns keys and the default column order.
var columns = output.NewColumnSet(
	output.Column[history.Row]{Key: colDate, Header: "DATE", Value: func(r history.Row) string { return r.ReviewSubmitDateAndTime }},
	output.Column[history.Row]{Key: colStars, Header: "STARS", Value: func(r history.Row) string { return r.StarRating }},
	output.Column[history.Row]{Key: colLocale, Header: "LOCALE", Value: func(r history.Row) string { return r.ReviewerLanguage }},
	output.Column[history.Row]{Key: colVersion, Header: "VERSION", Value: version},
	output.Column[history.Row]{Key: colTitle, Header: "TITLE", Value: func(r history.Row) string { return summary(r.ReviewTitle) }},
	output.Column[history.Row]{Key: colSummary, Header: "SUMMARY", Value: func(r history.Row) string { return summary(r.ReviewText) }},
	output.Column[history.Row]{Key: colDevice, Header: "DEVICE", Value: func(r history.Row) string { return r.Device }},
	output.Column[history.Row]{Key: colReply, Header: "REPLY", Value: func(r history.Row) string { return summary(r.DeveloperReplyText) }},
)

// defaultColumns is the subset shown when --columns is not given (the full set
// above is wide; the default keeps the table readable).
const defaultColumns = colDate + "," + colStars + "," + colLocale + "," + colVersion + "," + colTitle + "," + colSummary

// maxSummaryLen caps a free-text cell (title/body/reply) so a long comment does
// not blow out the table.
const maxSummaryLen = 60

// version renders the app version a review targets as "name (code)", "name",
// "(code)", or "".
func version(r history.Row) string {
	switch {
	case r.AppVersionName != "" && r.AppVersionCode != "":
		return r.AppVersionName + " (" + r.AppVersionCode + ")"
	case r.AppVersionName != "":
		return r.AppVersionName
	case r.AppVersionCode != "":
		return "(" + r.AppVersionCode + ")"
	default:
		return ""
	}
}

// summary is a one-line, table-friendly digest of free text: the first
// non-blank line, interior whitespace collapsed to single spaces, truncated to
// maxSummaryLen runes with an ellipsis.
func summary(text string) string {
	var line string
	for _, ln := range strings.Split(text, "\n") {
		if strings.TrimSpace(ln) != "" {
			line = ln
			break
		}
	}
	line = strings.Join(strings.Fields(line), " ")
	runes := []rune(line)
	if len(runes) > maxSummaryLen {
		return string(runes[:maxSummaryLen]) + "…"
	}
	return line
}

// Payload satisfies output.Renderable. Rows is the parsed report; Columns the
// resolved display order.
type Payload struct {
	Rows    []history.Row
	Columns []output.Column[history.Row]
}

// Renderers renders the parsed rows. table/markdown draw the selected columns;
// JSON emits the rows under a {"reviews":[...]} envelope. This JSON is the
// ADR-0037 documented deviation from the ADR-0003 pass-through — the upstream is
// a CSV file, so there is no API response to mirror; the rows carry stable
// lowerCamel field names instead.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, p.Columns, p.Rows) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, p.Columns, p.Rows) },
	}
}

func renderJSON(w io.Writer, p Payload) error {
	rows := p.Rows
	if rows == nil {
		rows = []history.Row{}
	}
	return output.WriteJSON(w, struct {
		Reviews []history.Row `json:"reviews"`
	}{Reviews: rows})
}

// bucketAccessError wraps a 403/404 from the reporting bucket with the two
// things an operator needs: where the authoritative bucket URI comes from (the
// Play Console's "Copy Cloud Storage URI" button) and which global permission
// the service account must hold ("View app information"). It carries no
// ExitCode of its own so the wrapped *api.Error stays authoritative (403 → exit
// 11, 404 → exit 30).
type bucketAccessError struct {
	bucket string
	cause  error
}

func (e *bucketAccessError) Error() string {
	return "cannot read the reporting bucket " + e.bucket +
		" — grant the service account the \"View app information\" (global) permission in the Play Console, " +
		"and if the bucket name differs, copy the authoritative gs:// URI from the Console's Download reports > Reviews page " +
		"(\"Copy Cloud Storage URI\" button) and pass its suffix via --bucket: " + e.cause.Error()
}

func (e *bucketAccessError) Unwrap() error { return e.cause }

// noReportsError signals that no monthly reviews report exists for the package
// in the bucket (an empty listing when --month was omitted). It is API-misuse
// shaped (exit 30) — the same class as an unknown package.
type noReportsError struct{ pkg, bucket string }

func (e *noReportsError) Error() string {
	return "no monthly reviews reports found for " + e.pkg + " in " + e.bucket +
		" — reports appear only after the app has reviews and Play has published a monthly CSV; " +
		"pass --month YYYY-MM once one exists, or --bucket if the derived name is wrong"
}
func (e *noReportsError) ExitCode() int { return 30 }

// classify adds the reporting-bucket hint to a 403/404 while leaving the
// wrapped *api.Error to drive the exit code; every other failure propagates
// verbatim.
func classify(bucket string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusForbidden, http.StatusNotFound:
			return &bucketAccessError{bucket: bucket, cause: err}
		}
	}
	return err
}

// isNotFound reports whether err is a 404 from the reporting bucket — a month
// with no published report. In range mode that is a skipped WARN, not a failure.
func isNotFound(err error) bool {
	var apiErr *api.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// Run resolves the package, the reporting bucket, and the target month, fetches
// the monthly reviews CSV report, parses it (UTF-16 → UTF-8, header-driven), and
// returns the rendered rows.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg := in.Package
	if pkg == "" && rc.Resolved != nil {
		pkg = rc.Resolved.Pin
	}
	if pkg == "" {
		return nil, exit.Usagef("no package — pass --package <pkg> or run gplay init in your repo")
	}

	// Empty --columns yields the curated default subset, not every column: the
	// full set (with device/reply) is wide, so it stays opt-in.
	spec := in.Columns
	if strings.TrimSpace(spec) == "" {
		spec = defaultColumns
	}
	cols, err := columns.Resolve(spec)
	if err != nil {
		return nil, err
	}

	// Derive the bucket unless overridden. The developer-id is only needed for
	// the default name, so --bucket lets an operator skip the developer-id
	// cascade entirely.
	bucket := strings.TrimSpace(in.Bucket)
	if bucket == "" {
		devID, err := addressing.Resolve(in.DeveloperID, os.Getenv(addressing.EnvDeveloperID), rc.Resolved)
		if err != nil {
			return nil, err
		}
		bucket = "pubsite_prod_rev_" + devID
	}

	// --month (single) and --from/--to (range) are mutually exclusive addressing
	// axes: one names a month, the other a span. Both together is ambiguous.
	rangeMode := in.From != "" || in.To != ""
	if rangeMode && in.Month != "" {
		return nil, exit.Usagef("--month and --from/--to are mutually exclusive")
	}
	if rangeMode && (in.From == "" || in.To == "") {
		return nil, exit.Usagef("--from and --to must be given together")
	}

	// Resolve the months to read up front so a malformed flag fails before any
	// network I/O. An empty list means "default": list the bucket for the latest.
	var months []string
	switch {
	case rangeMode:
		if months, err = history.MonthRange(in.From, in.To); err != nil {
			return nil, exit.Usagef("%s", err)
		}
	case in.Month != "":
		var yyyymm string
		if yyyymm, err = history.NormalizeMonth(in.Month); err != nil {
			return nil, exit.Usagef("%s", err)
		}
		months = []string{yyyymm}
	}

	hc, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	// Neither --month nor a range: list the package's reports, pick the latest.
	if len(months) == 0 {
		objs, err := gcs.ListObjects(rc.Ctx, hc, bucket, history.ReviewsPrefix(pkg))
		if err != nil {
			return nil, classify(bucket, err)
		}
		names := make([]string, len(objs))
		for i, o := range objs {
			names[i] = o.Name
		}
		latest := history.LatestMonth(pkg, names)
		if latest == "" {
			return nil, &noReportsError{pkg: pkg, bucket: bucket}
		}
		months = []string{latest}
	}

	// Fetch each month. In range mode a missing month (404) is a skipped stderr
	// WARN — the range is a best-effort sweep over what Play actually published,
	// not a demand that every month exist. Any other failure (e.g. 403) is fatal.
	// read counts the months that actually yielded a report.
	var all []history.Row
	read := 0
	for _, m := range months {
		raw, err := gcs.FetchObject(rc.Ctx, hc, bucket, history.ObjectName(pkg, m))
		if err != nil {
			if rangeMode && isNotFound(err) {
				if rc.Stderr != nil {
					_, _ = fmt.Fprintf(rc.Stderr, "WARN: no reviews report for %s-%s — skipped\n", m[:4], m[4:])
				}
				continue
			}
			return nil, classify(bucket, err)
		}
		read++
		rows, err := history.Parse(raw)
		if err != nil {
			return nil, err
		}
		all = append(all, rows...)
	}

	// A range that matched no report at all — every month 404'd, typically a
	// wrong --package or --bucket — is the same "no reports" condition the
	// single-month and default paths raise (exit 30), not a silent empty success.
	if rangeMode && read == 0 {
		return nil, &noReportsError{pkg: pkg, bucket: bucket}
	}

	// Merge only when more than one report was actually read: multiple files can
	// repeat a review across the month boundary, so they need cross-file dedup
	// (latest update wins) and a submit-time order. A lone report — a single
	// month, a degenerate --from X --to X, or a range where only one month
	// existed — is one coherent file, left verbatim exactly like --month so the
	// two spellings of "one month" agree.
	if read > 1 {
		all = history.Merge(all)
	}
	return Payload{Rows: all, Columns: cols}, nil
}

// NewCommand returns the cobra command for `gplay reviews history`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Read the full review history for a package from the monthly CSV reports",
		Long: `Read the full review history for --package from Google's monthly CSV
reports in the developer's Reporting bucket — the only channel beyond
` + "`reviews list`" + `'s 7-day API window (ADR-0037).

The report is read from the Reporting bucket over the Cloud Storage API
with a distinct read-only scope (devstorage.read_only); the service
account needs the "View app information" (global) permission. The bucket
name defaults to pubsite_prod_rev_<developerId> from the developer-account
axis; override it with --bucket when the Console-issued URI differs (copy
it with the Console's "Copy Cloud Storage URI" button).

--month YYYY-MM selects one month; when omitted, the latest month present
for the package is used. --from YYYY-MM --to YYYY-MM instead read every
monthly report across the range and merge them into one result set (a
review edited across the month boundary appears once, latest update
winning; a month with no report is skipped with a WARN). --month and
--from/--to are mutually exclusive. Default table columns: date, stars,
locale, version, title, summary — override with --columns device,reply,...

--output json emits the parsed rows as {"reviews":[...]} with stable
lowerCamel field names (the documented ADR-0037 deviation: the upstream is
a CSV file, not a JSON API response); --output markdown renders a Markdown
table.`,
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
	cmd.Flags().StringVar(&in.Month, "month", "", "month to read as YYYY-MM (default: the latest month present in the bucket)")
	cmd.Flags().StringVar(&in.From, "from", "", "range start as YYYY-MM (with --to; mutually exclusive with --month)")
	cmd.Flags().StringVar(&in.To, "to", "", "range end as YYYY-MM (with --from; mutually exclusive with --month)")
	cmd.Flags().StringVar(&in.Bucket, "bucket", "", "Reporting bucket name override (default: pubsite_prod_rev_<developerId>)")
	cmd.Flags().StringVar(&in.DeveloperID, "developer-id", "", "Play Console Developer account id used to derive the bucket (overrides the active Account's, env, and project-local)")
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns to show (default: "+defaultColumns+")")
	return cmd
}
