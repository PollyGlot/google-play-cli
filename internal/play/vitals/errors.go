package vitals

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
)

// ErrorCountSet is the metric set behind `vitals errors counts`: a queryable
// metric set (errorReportCount / distinctUsers) like the rate sets, but reached
// under the `errors` resource group. Its Name yields the native query method id
// `playdeveloperreporting.vitals.errors.counts.query`, so the same Query +
// validation machinery applies. It is deliberately NOT in the metricSets
// registry: it is surfaced under `vitals errors counts`, not `vitals query`.
func ErrorCountSet() MetricSet {
	return MetricSet{Name: "errors.counts", Resource: "errorCountMetricSet"}
}

// SearchOptions bounds an errors `:search` (issues or reports): an AIP-160
// Filter, the [Start,End) interval, an OrderBy, and a total Limit (0 = every
// page). The interval is sent as date-only datapoints, or with their hour when
// Hourly is set (minutes are never sent, so a bound is read on its hour). The
// call auto-paginates to Limit (or to exhaustion). SampleReports asks
// errors.issues.search to attach that many sample report names to each issue
// (0 = none, not sent); errors.reports.search has no such parameter and
// ignores it.
type SearchOptions struct {
	Filter        string
	Start         time.Time
	End           time.Time
	Hourly        bool
	Limit         int
	OrderBy       string
	SampleReports int
}

// MaxSampleReports is the largest sampleErrorReportLimit Play accepts:
// Discovery documents "currently only 0 and 1 are supported".
const MaxSampleReports = 1

const (
	opSearchIssues  = "playdeveloperreporting.vitals.errors.issues.search"
	opSearchReports = "playdeveloperreporting.vitals.errors.reports.search"

	// Per-page sizes: the documented maxima, to minimise round-trips. The API
	// caps to these regardless, and the loop follows nextPageToken to Limit.
	pageMaxIssues  = 1000
	pageMaxReports = 100
)

// Registry entries behind the two `:search` reads (#513, batch 3).
var (
	mSearchIssues  = apiregistry.MustResolve("playdeveloperreporting.vitals.errors.issues.search")
	mSearchReports = apiregistry.MustResolve("playdeveloperreporting.vitals.errors.reports.search")
)

// SearchErrorIssues issues errors.issues.search (GET) for pkg, following
// nextPageToken to opts.Limit (0 = all), and returns the rebuilt
// {errorIssues:[...]} envelope (items verbatim) for the JSON pass-through. The
// second return value reports that opts.Limit cut the list short while the
// server still had pages (PRD #446).
func SearchErrorIssues(ctx context.Context, hc *http.Client, pkg string, opts SearchOptions) (json.RawMessage, bool, error) {
	q := searchQuery(opts)
	if opts.SampleReports > 0 {
		q.Set("sampleErrorReportLimit", strconv.Itoa(opts.SampleReports))
	}
	return paginateGET(ctx, hc, mSearchIssues, opSearchIssues, pkg, q, "errorIssues", pageMaxIssues, opts.Limit)
}

// SearchErrorReports issues errors.reports.search (GET) for pkg, following
// nextPageToken to opts.Limit, and returns the rebuilt {errorReports:[...]}
// envelope (individual reports / stack traces), plus the same truncation signal
// as SearchErrorIssues.
func SearchErrorReports(ctx context.Context, hc *http.Client, pkg string, opts SearchOptions) (json.RawMessage, bool, error) {
	return paginateGET(ctx, hc, mSearchReports, opSearchReports, pkg, searchQuery(opts), "errorReports", pageMaxReports, opts.Limit)
}

func searchQuery(opts SearchOptions) url.Values {
	base := url.Values{}
	if opts.Filter != "" {
		base.Set("filter", opts.Filter)
	}
	if opts.OrderBy != "" {
		base.Set("orderBy", opts.OrderBy)
	}
	if !opts.Start.IsZero() {
		setIntervalDate(base, "interval.startTime", opts.Start, opts.Hourly)
	}
	if !opts.End.IsZero() {
		setIntervalDate(base, "interval.endTime", opts.End, opts.Hourly)
	}
	return base
}

// setIntervalDate writes the google.type.DateTime query params for an interval
// endpoint (e.g. "interval.startTime.year=2026"). An hourly bound carries its
// hour, hour 0 included: the API reads a date-only bound as that day's midnight,
// which turned `--since 6h` into the previous whole day (#775).
func setIntervalDate(q url.Values, prefix string, t time.Time, hourly bool) {
	q.Set(prefix+".year", strconv.Itoa(t.Year()))
	q.Set(prefix+".month", strconv.Itoa(int(t.Month())))
	q.Set(prefix+".day", strconv.Itoa(t.Day()))
	if hourly {
		q.Set(prefix+".hours", strconv.Itoa(t.Hour()))
	}
}

// ErrorIssue is the render-ready subset of a clustered error issue.
type ErrorIssue struct {
	Type         string
	Cause        string
	Location     string
	ReportCount  string
	DistinctUser string
	LastSeen     string
	IssueURI     string
}

// ParseErrorIssues projects an errors.issues.search response into ErrorIssues.
func ParseErrorIssues(body []byte) ([]ErrorIssue, error) {
	var resp struct {
		ErrorIssues []struct {
			Type                string `json:"type"`
			Cause               string `json:"cause"`
			Location            string `json:"location"`
			ErrorReportCount    string `json:"errorReportCount"`
			DistinctUsers       string `json:"distinctUsers"`
			LastErrorReportTime string `json:"lastErrorReportTime"`
			IssueURI            string `json:"issueUri"`
		} `json:"errorIssues"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode error issues: %w", err)
	}
	out := make([]ErrorIssue, 0, len(resp.ErrorIssues))
	for _, i := range resp.ErrorIssues {
		out = append(out, ErrorIssue{
			Type:         shortErrorType(i.Type),
			Cause:        i.Cause,
			Location:     i.Location,
			ReportCount:  i.ErrorReportCount,
			DistinctUser: i.DistinctUsers,
			LastSeen:     i.LastErrorReportTime,
			IssueURI:     i.IssueURI,
		})
	}
	return out, nil
}

// ErrorReport is the render-ready subset of an individual error report. Report
// is the platform-produced textual stack trace: obfuscated until ProGuard/R8
// mappings are uploaded (#250).
type ErrorReport struct {
	EventTime  string
	Type       string
	AppVersion string
	Device     string
	OsAPILevel string
	Report     string
}

// ParseErrorReports projects an errors.reports.search response into ErrorReports.
func ParseErrorReports(body []byte) ([]ErrorReport, error) {
	var resp struct {
		ErrorReports []struct {
			Type       string `json:"type"`
			EventTime  string `json:"eventTime"`
			ReportText string `json:"reportText"`
			AppVersion struct {
				VersionCode string `json:"versionCode"`
			} `json:"appVersion"`
			OsVersion struct {
				APILevel string `json:"apiLevel"`
			} `json:"osVersion"`
			DeviceModel struct {
				MarketingName string `json:"marketingName"`
			} `json:"deviceModel"`
		} `json:"errorReports"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode error reports: %w", err)
	}
	out := make([]ErrorReport, 0, len(resp.ErrorReports))
	for _, r := range resp.ErrorReports {
		out = append(out, ErrorReport{
			EventTime:  r.EventTime,
			Type:       shortErrorType(r.Type),
			AppVersion: r.AppVersion.VersionCode,
			Device:     r.DeviceModel.MarketingName,
			OsAPILevel: r.OsVersion.APILevel,
			Report:     r.ReportText,
		})
	}
	return out, nil
}

// shortErrorType trims the API's ERROR_TYPE enum to a compact label (CRASH, ANR,
// NON_FATAL); unknown values pass through verbatim.
func shortErrorType(t string) string {
	switch t {
	case "APPLICATION_NOT_RESPONDING":
		return "ANR"
	case "CRASH":
		return "CRASH"
	case "NON_FATAL":
		return "NON_FATAL"
	case "ERROR_TYPE_UNSPECIFIED", "":
		return ""
	default:
		return t
	}
}
