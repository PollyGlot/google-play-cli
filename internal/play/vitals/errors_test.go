package vitals_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

// TestErrorCountSet_isAQueryableMetricSet asserts the errors.counts set resolves
// to the errorCountMetricSet resource and its query method id exists in the
// index, so the metric-set Query/validation machinery applies to it.
func TestErrorCountSet_isAQueryableMetricSet(t *testing.T) {
	set := vitals.ErrorCountSet()
	if set.Resource != "errorCountMetricSet" {
		t.Errorf("resource = %q", set.Resource)
	}
	if set.QueryMethodID() != "playdeveloperreporting.vitals.errors.counts.query" {
		t.Errorf("method id = %q", set.QueryMethodID())
	}
	idx, err := schemaindex.Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	if _, ok := idx.Methods[set.QueryMethodID()]; !ok {
		t.Error("errorCount query method missing from index")
	}
	if !containsStr(vitals.SupportedMetrics(idx, set), "errorReportCount") {
		t.Errorf("errorCount supported metrics missing errorReportCount: %v", vitals.SupportedMetrics(idx, set))
	}
}

func TestSearchErrorIssues_issuesGET(t *testing.T) {
	var gotURL, gotMethod string
	hc := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		gotMethod = r.Method
		return jsonResp(200, `{"errorIssues":[{"type":"CRASH","cause":"NullPointerException","location":"MainActivity.onCreate","errorReportCount":"42","distinctUsers":"30","lastErrorReportTime":"2026-06-15T10:00:00Z","issueUri":"https://play/x"}]}`), nil
	})}
	raw, _, err := vitals.SearchErrorIssues(context.Background(), hc, "com.example.app", vitals.SearchOptions{
		Start: time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("SearchErrorIssues: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if !strings.Contains(gotURL, "/apps/com.example.app/errorIssues:search") {
		t.Errorf("URL = %q", gotURL)
	}
	if !strings.Contains(gotURL, "interval.startTime.year=2026") {
		t.Errorf("URL missing interval params: %q", gotURL)
	}
	issues, err := vitals.ParseErrorIssues(raw)
	if err != nil {
		t.Fatalf("ParseErrorIssues: %v", err)
	}
	if len(issues) != 1 || issues[0].Type != "CRASH" || issues[0].Cause != "NullPointerException" {
		t.Errorf("issues = %+v", issues)
	}
	if issues[0].ReportCount != "42" {
		t.Errorf("reportCount = %q", issues[0].ReportCount)
	}
}

// TestSearchErrorIssues_encodesFilterOrderByPageSize pins the search query
// shaping: --filter, --order-by and the per-page size must reach the wire under
// their exact API param names (a refactor emitting page_size would silently
// break the call otherwise).
func TestSearchErrorIssues_encodesFilterOrderByPageSize(t *testing.T) {
	var gotURL string
	hc := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		return jsonResp(200, `{"errorIssues":[]}`), nil
	})}
	_, _, err := vitals.SearchErrorIssues(context.Background(), hc, "com.example.app", vitals.SearchOptions{
		Filter:  "errorIssueType = CRASH",
		OrderBy: "errorReportCount desc",
		Limit:   25,
	})
	if err != nil {
		t.Fatalf("SearchErrorIssues: %v", err)
	}
	for _, want := range []string{"filter=", "orderBy=", "pageSize=25"} {
		if !strings.Contains(gotURL, want) {
			t.Errorf("URL %q missing %q", gotURL, want)
		}
	}
}

func TestSearchErrorReports_reportsGETAndParse(t *testing.T) {
	hc := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "/errorReports:search") {
			t.Errorf("path = %q", r.URL.Path)
		}
		return jsonResp(200, `{"errorReports":[{"type":"CRASH","eventTime":"2026-06-15T09:00:00Z","reportText":"java.lang.NullPointerException\n\tat a.b.c(Unknown Source)","appVersion":{"versionCode":"123"},"osVersion":{"apiLevel":"31"},"deviceModel":{"marketingName":"Pixel 7"}}]}`), nil
	})}
	raw, _, err := vitals.SearchErrorReports(context.Background(), hc, "com.example.app", vitals.SearchOptions{})
	if err != nil {
		t.Fatalf("SearchErrorReports: %v", err)
	}
	reports, err := vitals.ParseErrorReports(raw)
	if err != nil {
		t.Fatalf("ParseErrorReports: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("reports = %d", len(reports))
	}
	r := reports[0]
	if r.AppVersion != "123" || r.Device != "Pixel 7" || r.OsAPILevel != "31" {
		t.Errorf("report meta = %+v", r)
	}
	if !strings.Contains(r.Report, "NullPointerException") {
		t.Errorf("report text lost: %q", r.Report)
	}
}

func TestSearchErrors_nonOKIsAPIError(t *testing.T) {
	hc := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return jsonResp(403, `{"error":{"message":"no"}}`), nil
	})}
	_, _, err := vitals.SearchErrorIssues(context.Background(), hc, "com.example.app", vitals.SearchOptions{})
	var apiErr *api.Error
	if err == nil || !asAPIErr(err, &apiErr) || apiErr.StatusCode != 403 {
		t.Fatalf("want *api.Error 403, got %v", err)
	}
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func asAPIErr(err error, target **api.Error) bool {
	if e, ok := err.(*api.Error); ok {
		*target = e
		return true
	}
	return false
}
