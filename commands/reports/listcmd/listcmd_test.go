package listcmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/reports"
	"github.com/PollyGlot/google-play-cli/internal/reports/bucket"
	"github.com/PollyGlot/google-play-cli/internal/reports/reportstest"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func fixture() reportstest.Bucket {
	csv := reportstest.UTF16LE("Date,Daily Device Installs\n2026-09-01,1\n")
	return reportstest.Bucket{Objects: map[string][]byte{
		"stats/installs/installs_com.example.app_202608_country.csv":  csv,
		"stats/installs/installs_com.example.app_202609_country.csv":  csv,
		"stats/installs/installs_com.example.app_202609_overview.csv": csv,
		"stats/installs/installs_com.other.app_202609_overview.csv":   csv,
		"stats/installs/notes.txt":                                    []byte("not an export"),
		"earnings/earnings_202609_123-1.zip":                          []byte("PK"),
	}}
}

// earnings is a finance family the way #696 will declare it: in this slice
// it only exists in tests, to drive the per-family 403 path.
var earnings = reports.Family{
	Name:       "earnings",
	Prefix:     "earnings/earnings_",
	Scope:      reports.AccountWide,
	Permission: bucket.ViewFinancialData,
	Page:       "Financial",
}

func run(t *testing.T, b reportstest.Bucket, families []reports.Family, in Input) (output.Renderable, *testkit.Fake, string, error) {
	t.Helper()
	if families != nil {
		saved := catalogue
		catalogue = func() []reports.Family { return families }
		t.Cleanup(func() { catalogue = saved })
	}
	fake := testkit.NewFake(b.Responder())
	rc, _, stderr := reportstest.NewRC(t, fake)
	r, err := Run(rc, in)
	return r, fake, stderr.String(), err
}

func renderJSON(t *testing.T, r output.Renderable) string {
	t.Helper()
	var buf bytes.Buffer
	if err := r.Renderers().JSON(&buf); err != nil {
		t.Fatalf("render JSON: %v", err)
	}
	return buf.String()
}

func exitCodeOf(t *testing.T, err error) int {
	t.Helper()
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one carrying an exit code", err, err)
	}
	return coder.ExitCode()
}

func TestRun_listsEveryExportWithMetadata(t *testing.T) {
	r, _, _, err := run(t, fixture(), nil, Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := `{
  "reports": [
    {
      "family": "installs",
      "package": "com.example.app",
      "month": "2026-08",
      "split": "country",
      "object": "stats/installs/installs_com.example.app_202608_country.csv",
      "size": "82",
      "updated": "2026-10-04T08:00:00.000Z"
    },
    {
      "family": "installs",
      "package": "com.example.app",
      "month": "2026-09",
      "split": "country",
      "object": "stats/installs/installs_com.example.app_202609_country.csv",
      "size": "82",
      "updated": "2026-10-04T08:00:00.000Z"
    },
    {
      "family": "installs",
      "package": "com.example.app",
      "month": "2026-09",
      "split": "overview",
      "object": "stats/installs/installs_com.example.app_202609_overview.csv",
      "size": "82",
      "updated": "2026-10-04T08:00:00.000Z"
    },
    {
      "family": "installs",
      "package": "com.other.app",
      "month": "2026-09",
      "split": "overview",
      "object": "stats/installs/installs_com.other.app_202609_overview.csv",
      "size": "82",
      "updated": "2026-10-04T08:00:00.000Z"
    }
  ]
}
`
	if got := renderJSON(t, r); got != want {
		t.Errorf("JSON =\n%s\nwant\n%s", got, want)
	}
}

func TestRun_filters(t *testing.T) {
	r, fake, _, err := run(t, fixture(), nil, Input{Family: "installs", Package: "com.example.app", Since: "2026-09"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := renderJSON(t, r)
	if strings.Count(got, `"object"`) != 2 || strings.Contains(got, "2026-08") || strings.Contains(got, "com.other.app") {
		t.Errorf("filters not applied:\n%s", got)
	}
	// --package narrows the listing on the wire, not only the output.
	if calls := fake.Calls(); len(calls) != 1 || !strings.Contains(calls[0].Query, "prefix=stats%2Finstalls%2Finstalls_com.example.app_") {
		t.Errorf("calls = %+v", calls)
	}

	r, _, _, err = run(t, fixture(), nil, Input{Until: "2026-08"})
	if err != nil {
		t.Fatalf("Run --until: %v", err)
	}
	if got := renderJSON(t, r); strings.Count(got, `"object"`) != 1 || !strings.Contains(got, "2026-08") {
		t.Errorf("--until not applied:\n%s", got)
	}
}

func TestRun_forbiddenFamilyIsAWarning(t *testing.T) {
	b := fixture()
	b.Deny = map[string]int{"earnings/": 403}
	r, _, stderr, err := run(t, b, []reports.Family{reports.Installs, earnings}, Input{})
	if err != nil {
		t.Fatalf("Run: %v (a 403 on one family must not fail the inventory)", err)
	}
	if got := renderJSON(t, r); strings.Count(got, `"object"`) != 4 {
		t.Errorf("the installs inventory should still print:\n%s", got)
	}
	for _, want := range []string{"earnings", `"View financial data" (global)`} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr should name %q: %q", want, stderr)
		}
	}
}

func TestRun_everyFamilyForbiddenFails(t *testing.T) {
	b := fixture()
	b.Deny = map[string]int{"stats/": 403, "financial-stats/": 403}
	_, _, _, err := run(t, b, nil, Input{})
	if code := exitCodeOf(t, err); code != 11 {
		t.Fatalf("exit = %d, want 11 (err: %v)", code, err)
	}
	if !strings.Contains(err.Error(), `"View app information" (global)`) || !strings.Contains(err.Error(), "Copy Cloud Storage URI") {
		t.Errorf("message = %q", err)
	}
}

func TestRun_accountWideFamilyListed(t *testing.T) {
	r, _, _, err := run(t, fixture(), []reports.Family{reports.Installs, earnings}, Input{Family: "earnings"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := renderJSON(t, r)
	if !strings.Contains(got, `"family": "earnings"`) || strings.Contains(got, `"package"`) || !strings.Contains(got, `"split": "123-1"`) {
		t.Errorf("account-wide row:\n%s", got)
	}
}

func TestRun_badFlags_usageWithoutNetwork(t *testing.T) {
	for name, in := range map[string]Input{
		"unknown family": {Family: "earning"},
		"bad since":      {Since: "2026-9"},
		"bad until":      {Until: "26-09"},
		"inverted":       {Since: "2026-09", Until: "2026-01"},
	} {
		t.Run(name, func(t *testing.T) {
			_, fake, _, err := run(t, fixture(), nil, in)
			if code := exitCodeOf(t, err); code != 2 {
				t.Fatalf("exit = %d, want 2 (err: %v)", code, err)
			}
			if len(fake.Calls()) != 0 {
				t.Error("a usage error reached the network")
			}
		})
	}
	_, _, _, err := run(t, fixture(), nil, Input{Family: "earning"})
	if !strings.Contains(err.Error(), "installs") {
		t.Errorf("unknown --family should list the known ones: %q", err)
	}
}

func TestRun_listsEveryStatisticsFamilyWithTheProduct(t *testing.T) {
	b := fixture()
	csv := []byte("Date\n2026-09-01\n")
	b.Objects["stats/ratings/ratings_com.example.app_202609_overview.csv"] = csv
	b.Objects["stats/crashes/crashes_com.example.app_202609_overview.csv"] = csv
	b.Objects["stats/store_performance/store_performance_com.example.app_202609_traffic_source.csv"] = csv
	b.Objects["financial-stats/subscriptions/subscriptions_com.example.app_premium_monthly_v2_202609_country.csv"] = csv
	r, _, _, err := run(t, b, nil, Input{Package: "com.example.app", Since: "2026-09"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := renderJSON(t, r)
	for _, want := range []string{
		`"family": "ratings"`, `"family": "crashes"`,
		`"family": "store-performance"`, `"split": "traffic_source"`,
		`"family": "subscription-stats"`, `"product": "premium_monthly_v2"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("inventory lacks %s:\n%s", want, got)
		}
	}
	if strings.Count(got, `"product"`) != 1 {
		t.Errorf("only the subscription-stats row carries a product:\n%s", got)
	}
	var tbl bytes.Buffer
	if err := r.Renderers().Table(&tbl); err != nil {
		t.Fatalf("table: %v", err)
	}
	if !strings.Contains(tbl.String(), "PRODUCT") || !strings.Contains(tbl.String(), "premium_monthly_v2") {
		t.Errorf("the default table should show the product id to pass to --product:\n%s", tbl.String())
	}
}

func TestRun_subscriptionStatsWithoutFinancialGrantIsAWarning(t *testing.T) {
	b := fixture()
	b.Deny = map[string]int{"financial-stats/": 403}
	r, _, stderr, err := run(t, b, nil, Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := renderJSON(t, r); strings.Count(got, `"object"`) != 4 {
		t.Errorf("the statistics inventory should still print:\n%s", got)
	}
	if !strings.Contains(stderr, "subscription-stats") || !strings.Contains(stderr, `"View financial data" (global)`) {
		t.Errorf("stderr = %q", stderr)
	}
}
