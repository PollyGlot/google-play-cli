package vitals

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

func ErrorCountSet() MetricSet {
	return MetricSet{Name: "errors.counts", Resource: "errorCountMetricSet"}
}

type SearchOptions struct {
	Filter        string
	Start         time.Time
	End           time.Time
	Hourly        bool
	Limit         int
	OrderBy       string
	SampleReports int
}

const MaxSampleReports = 1

const (
	opSearchIssues  = "playdeveloperreporting.vitals.errors.issues.search"
	opSearchReports = "playdeveloperreporting.vitals.errors.reports.search"

	pageMaxIssues  = 1000
	pageMaxReports = 100
)

var (
	mSearchIssues  = apiregistry.MustResolve("playdeveloperreporting.vitals.errors.issues.search")
	mSearchReports = apiregistry.MustResolve("playdeveloperreporting.vitals.errors.reports.search")
)

func SearchErrorIssues(ctx context.Context, hc *http.Client, pkg string, opts SearchOptions) (json.RawMessage, bool, error) {
	q := searchQuery(opts)
	if opts.SampleReports > 0 {
		q.Set("sampleErrorReportLimit", strconv.Itoa(opts.SampleReports))
	}
	return paginateGET(ctx, hc, mSearchIssues, opSearchIssues, pkg, q, "errorIssues", pageMaxIssues, opts.Limit)
}

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

func setIntervalDate(q url.Values, prefix string, t time.Time, hourly bool) {
	q.Set(prefix+".year", strconv.Itoa(t.Year()))
	q.Set(prefix+".month", strconv.Itoa(int(t.Month())))
	q.Set(prefix+".day", strconv.Itoa(t.Day()))
	if hourly {
		q.Set(prefix+".hours", strconv.Itoa(t.Hour()))
	}
}

type ErrorIssue struct {
	Type         string
	Cause        string
	Location     string
	ReportCount  string
	DistinctUser string
	LastSeen     string
	IssueURI     string
}

func ParseErrorIssues(pkg string, body []byte) ([]ErrorIssue, error) {
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
		return nil, api.DecodeFailure(api.Error{Operation: opSearchIssues, Package: pkg, Message: "decode error issues: " + err.Error(), Cause: err})
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

type ErrorReport struct {
	EventTime  string
	Type       string
	AppVersion string
	Device     string
	OsAPILevel string
	Report     string
}

func ParseErrorReports(pkg string, body []byte) ([]ErrorReport, error) {
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
		return nil, api.DecodeFailure(api.Error{Operation: opSearchReports, Package: pkg, Message: "decode error reports: " + err.Error(), Cause: err})
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
