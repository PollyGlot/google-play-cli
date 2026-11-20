package history

import (
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/gcs"
	reportbucket "github.com/PollyGlot/google-play-cli/internal/reports/bucket"
	"github.com/PollyGlot/google-play-cli/internal/reviews/history"
)

type Input struct {
	Package     string
	Month       string
	Since       string
	Until       string
	Bucket      string
	DeveloperID string
	Columns     string
}

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

const defaultColumns = colDate + "," + colStars + "," + colLocale + "," + colVersion + "," + colTitle + "," + colSummary

const maxSummaryLen = 60

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

type Payload struct {
	Rows    []history.Row
	Columns []output.Column[history.Row]
}

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

type noReportsError struct{ pkg, bucket string }

func (e *noReportsError) Error() string {
	return "no monthly reviews reports found for " + e.pkg + " in " + e.bucket +
		": reports appear only after the app has reviews and Play has published a monthly CSV; " +
		"pass --month YYYY-MM once one exists, or --bucket if the derived name is wrong"
}
func (e *noReportsError) ExitCode() int { return 30 }

func classify(bucket string, err error) error {
	return reportbucket.Classify(bucket, reportbucket.ViewAppInformation, "Reviews", err)
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}

	spec := in.Columns
	if strings.TrimSpace(spec) == "" {
		spec = defaultColumns
	}
	cols, err := columns.Resolve(spec)
	if err != nil {
		return nil, err
	}

	bucket, err := reportbucket.Resolve(rc, in.Bucket, in.DeveloperID)
	if err != nil {
		return nil, err
	}

	rangeMode := in.Since != "" || in.Until != ""
	if rangeMode && in.Month != "" {
		return nil, exit.Usagef("--month and --since/--until are mutually exclusive")
	}
	if rangeMode && (in.Since == "" || in.Until == "") {
		return nil, exit.Usagef("--since and --until must be given together")
	}

	var months []string
	switch {
	case rangeMode:
		if months, err = history.MonthRange(in.Since, in.Until); err != nil {
			return nil, exit.Usagef("%s", err)
		}
	case in.Month != "":
		var yyyymm string
		if yyyymm, err = history.NormalizeMonth(in.Month); err != nil {
			return nil, exit.Usagef("%s", err)
		}
		months = []string{yyyymm}
	}

	hc, err := rc.UploadClient()
	if err != nil {
		return nil, err
	}

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

	var all []history.Row
	read := 0
	for _, m := range months {
		raw, err := gcs.FetchObject(rc.Ctx, hc, bucket, history.ObjectName(pkg, m))
		if err != nil {
			if rangeMode && reportbucket.IsNotFound(err) {
				rc.Warnf("no reviews report for %s-%s: skipped", m[:4], m[4:])
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

	if rangeMode && read == 0 {
		return nil, &noReportsError{pkg: pkg, bucket: bucket}
	}

	if read > 1 {
		all = history.Merge(all)
	}
	return Payload{Rows: all, Columns: cols}, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Read the full review history for a package from the monthly CSV reports",
		Long: `Read the full review history for --package from Google's monthly CSV
reports in the developer's Reporting bucket: the only channel beyond
` + "`reviews list`" + `'s 7-day API window.

The report is read from the Reporting bucket over the Cloud Storage API
with a distinct read-only scope (devstorage.read_only); the service
account needs the "View app information" (global) permission. The bucket
name defaults to pubsite_prod_rev_<developerId> from the developer-account
axis; override it with --bucket when the Console-issued URI differs (copy
it with the Console's "Copy Cloud Storage URI" button).

--month YYYY-MM selects one month; when omitted, the latest month present
for the package is used. --since YYYY-MM --until YYYY-MM instead read every
monthly report across the range and merge them into one result set (a
review edited across the month boundary appears once, latest update
winning; a month with no report is skipped with a warning). --month and
--since/--until are mutually exclusive. Default table columns: date, stars,
locale, version, title, summary: override with --columns device,reply,...

--output json emits the parsed rows as {"reviews":[...]} with stable
lowerCamel field names (a deliberate exception to the verbatim rule: the
upstream is a CSV file, not a JSON API response); --output markdown renders
a Markdown table.`,
		Example: `  # The latest month present in the Reporting bucket
  gplay reviews history

  gplay reviews history --month 2026-08

  # Half a year merged into one result set, as JSON
  gplay reviews history --since 2026-01 --until 2026-06 --output json`,
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
	cmd.Flags().StringVar(&in.Since, "since", "", "range start as YYYY-MM (with --until; mutually exclusive with --month)")
	cmd.Flags().StringVar(&in.Until, "until", "", "range end as YYYY-MM (with --since; mutually exclusive with --month)")
	cmd.Flags().StringVar(&in.Bucket, "bucket", "", "Reporting bucket name override (default: pubsite_prod_rev_<developerId>)")
	cmd.Flags().StringVar(&in.DeveloperID, "developer-id", "", "Play Console Developer account id used to derive the bucket (overrides the active Account's, env, and project-local)")
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns to show (default: "+defaultColumns+")")
	return cmd
}
