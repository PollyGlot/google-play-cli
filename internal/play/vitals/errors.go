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
	"github.com/PollyGlot/google-play-cli/internal/play/api"
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
// page). The interval is sent as date-only datapoints in the metric set's
// default timezone. The call auto-paginates to Limit (or to exhaustion).
type SearchOptions struct {
	Filter  string
	Start   time.Time
	End     time.Time
	Limit   int
	OrderBy string
}

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
	return searchErrors(ctx, hc, mSearchIssues, opSearchIssues, "errorIssues", pageMaxIssues, pkg, opts)
}

// SearchErrorReports issues errors.reports.search (GET) for pkg, following
// nextPageToken to opts.Limit, and returns the rebuilt {errorReports:[...]}
// envelope (individual reports / stack traces), plus the same truncation signal
// as SearchErrorIssues.
func SearchErrorReports(ctx context.Context, hc *http.Client, pkg string, opts SearchOptions) (json.RawMessage, bool, error) {
	return searchErrors(ctx, hc, mSearchReports, opSearchReports, "errorReports", pageMaxReports, pkg, opts)
}

// searchErrors is the shared, auto-paginating `:search` for the errors
// sub-resources. m carries the endpoint and verb, itemsKey only the envelope
// key of the response: the two used to be the same string because the path
// segment was hand-built from it, which is exactly the coupling #513 removes.
func searchErrors(ctx context.Context, hc *http.Client, m apiregistry.Method, op, itemsKey string, pageSize int, pkg string, opts SearchOptions) (json.RawMessage, bool, error) {
	base := url.Values{}
	if opts.Filter != "" {
		base.Set("filter", opts.Filter)
	}
	if opts.OrderBy != "" {
		base.Set("orderBy", opts.OrderBy)
	}
	if !opts.Start.IsZero() {
		setIntervalDate(base, "interval.startTime", opts.Start)
	}
	if !opts.End.IsZero() {
		setIntervalDate(base, "interval.endTime", opts.End)
	}
	baseURL, err := m.URL(map[string]string{"appsId": pkg})
	if err != nil {
		return nil, false, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	return paginateGET(ctx, hc, m.Verb, op, pkg, baseURL, base, itemsKey, pageSize, opts.Limit)
}

// setIntervalDate writes the date-only google.type.DateTime query params for an
// interval endpoint (e.g. "interval.startTime.year=2026").
func setIntervalDate(q url.Values, prefix string, t time.Time) {
	q.Set(prefix+".year", strconv.Itoa(t.Year()))
	q.Set(prefix+".month", strconv.Itoa(int(t.Month())))
	q.Set(prefix+".day", strconv.Itoa(t.Day()))
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
