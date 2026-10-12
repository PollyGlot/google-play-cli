package familycmd

import (
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/reports"
	"github.com/PollyGlot/google-play-cli/internal/reports/reportstest"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// Synthetic, format-faithful fixtures of the #695 families: the headers of
// Play's exports, invented values.
const (
	ratingsOverview = "Date,Package Name,Daily Average Rating,Total Average Rating\n" +
		"2026-09-01,com.example.app,4.50,4.31\n"
	crashesAppVersion = "Date,Package Name,App Version Code,Daily Crashes,Daily ANRs\n" +
		"2026-09-01,com.example.app,0042,3,1\n"
	// Store performance quotes its fields; a search term may hold a comma, a
	// tab or a line break.
	storeTraffic = "\"Date\",\"Package name\",\"Traffic source\",\"Search term\",\"Store listing acquisitions\",\"Store listing visitors\"\n" +
		"\"2026-09-01\",\"com.example.app\",\"Google Play search\",\"photo, editor\",\"12\",\"40\"\n" +
		"\"2026-09-01\",\"com.example.app\",\"Google Play search\",\"tab\there\",\"3\",\"9\"\n" +
		"\"2026-09-01\",\"com.example.app\",\"Google Play search\",\"two\nlines \"\"quoted\"\"\",\"1\",\"2\"\n"
	subsCountry = "Date,Package Name,Product ID,Country,New Subscriptions,Cancelled Subscriptions,Active Subscriptions\n" +
		"2026-09-01,com.example.app,premium_monthly_v2,FR,5,1,120\n"
)

const subsPrefix = "financial-stats/subscriptions/subscriptions_com.example.app_"

func familiesBucket() reportstest.Bucket {
	return reportstest.Bucket{Objects: map[string][]byte{
		"stats/ratings/ratings_com.example.app_202609_overview.csv":                           reportstest.UTF16LE(ratingsOverview),
		"stats/crashes/crashes_com.example.app_202609_app_version.csv":                        reportstest.UTF16LE(crashesAppVersion),
		"stats/store_performance/store_performance_com.example.app_202609_traffic_source.csv": []byte(storeTraffic),
		"stats/store_performance/store_performance_com.example.app_202609_country.csv":        []byte("\"Date\",\"Country / region\"\n\"2026-09-01\",\"FR\"\n"),
		subsPrefix + "premium_monthly_v2_202609_country.csv":                                  []byte(subsCountry),
		// A product whose id starts with the one asked: the listing prefix
		// matches it, the parsed product does not.
		subsPrefix + "premium_monthly_v2_trial_202610_country.csv": []byte(subsCountry),
	}}
}

func runFamily(t *testing.T, f reports.Family, b reportstest.Bucket, in Input) (string, *testkit.Fake, string, error) {
	t.Helper()
	fake := testkit.NewFake(b.Responder())
	rc, _, stderr := reportstest.NewRC(t, fake)
	r, err := Run(rc, f, in)
	if err != nil {
		return "", fake, stderr.String(), err
	}
	return renderJSON(t, r), fake, stderr.String(), nil
}

func TestRun_ratingsDefaultsToOverview(t *testing.T) {
	got, _, _, err := runFamily(t, reports.Ratings, familiesBucket(), Input{Package: pkg})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{`"family": "ratings"`, `"split": "overview"`, `"dailyAverageRating": "4.50"`, `"totalAverageRating": "4.31"`} {
		if !strings.Contains(got, want) {
			t.Errorf("JSON lacks %s:\n%s", want, got)
		}
	}
}

func TestRun_crashesKeepVersionCodesVerbatim(t *testing.T) {
	got, _, _, err := runFamily(t, reports.Crashes, familiesBucket(), Input{Package: pkg, Month: "2026-09", Dimension: "app_version"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{`"family": "crashes"`, `"appVersionCode": "0042"`, `"dailyAnrs": "1"`} {
		if !strings.Contains(got, want) {
			t.Errorf("JSON lacks %s:\n%s", want, got)
		}
	}
}

// TestRun_storePerformanceQuotedCellsRoundTrip is the acceptance criterion:
// commas, tabs, line breaks and escaped quotes inside quoted cells come out
// as the cell's exact text.
func TestRun_storePerformanceQuotedCellsRoundTrip(t *testing.T) {
	fake := testkit.NewFake(familiesBucket().Responder())
	rc, _, _ := reportstest.NewRC(t, fake)
	r, err := Run(rc, reports.StorePerformance, Input{Package: pkg, Dimension: "traffic_source"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := r.(Payload).Report.Table
	terms := make([]string, len(got.Rows))
	for i, row := range got.Rows {
		terms[i] = row.Value("searchTerm")
	}
	want := []string{"photo, editor", "tab\there", "two\nlines \"quoted\""}
	if strings.Join(terms, "|") != strings.Join(want, "|") {
		t.Errorf("search terms = %q, want %q", terms, want)
	}
	js := renderJSON(t, r)
	for _, w := range []string{`"searchTerm": "photo, editor"`, `"searchTerm": "tab\there"`, `"searchTerm": "two\nlines \"quoted\""`, `"storeListingAcquisitions": "12"`} {
		if !strings.Contains(js, w) {
			t.Errorf("JSON lacks %s:\n%s", w, js)
		}
	}
}

func TestRun_storePerformanceNeedsADimension(t *testing.T) {
	_, fake, _, err := runFamily(t, reports.StorePerformance, familiesBucket(), Input{Package: pkg})
	if code := exitCodeOf(t, err); code != 2 {
		t.Fatalf("exit = %d, want 2 (err: %v)", code, err)
	}
	if !strings.Contains(err.Error(), "country, traffic_source") || !strings.Contains(err.Error(), "--dimension") {
		t.Errorf("message should name --dimension and the breakdowns present: %q", err)
	}
	for _, c := range fake.Calls() {
		if strings.Contains(c.URL, "alt=media") {
			t.Errorf("nothing should be fetched: %s", c.URL)
		}
	}
}

func TestRun_subscriptionStatsReadsOneProduct(t *testing.T) {
	got, fake, _, err := runFamily(t, reports.SubscriptionStats, familiesBucket(), Input{Package: pkg, Product: "premium_monthly_v2"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{`"family": "subscription-stats"`, `"product": "premium_monthly_v2"`, `"split": "country"`, `"2026-09"`, `"activeSubscriptions": "120"`} {
		if !strings.Contains(got, want) {
			t.Errorf("JSON lacks %s:\n%s", want, got)
		}
	}
	// The longer product's later month must not count as the latest.
	if strings.Contains(got, "2026-10") || strings.Contains(got, "_trial") {
		t.Errorf("another product's export leaked in:\n%s", got)
	}
	calls := fake.Calls()
	if len(calls) == 0 || !strings.Contains(calls[0].Query, "prefix=financial-stats%2Fsubscriptions%2Fsubscriptions_com.example.app_premium_monthly_v2_") {
		t.Errorf("the listing should be narrowed to the product on the wire: %+v", calls)
	}
}

func TestRun_subscriptionStatsNeedsProduct(t *testing.T) {
	_, fake, _, err := runFamily(t, reports.SubscriptionStats, familiesBucket(), Input{Package: pkg})
	if code := exitCodeOf(t, err); code != 2 {
		t.Fatalf("exit = %d, want 2 (err: %v)", code, err)
	}
	if !strings.Contains(err.Error(), "--product") || !strings.Contains(err.Error(), "reports list --family subscription-stats") {
		t.Errorf("message should name --product and how to find the ids: %q", err)
	}
	if n := len(fake.Calls()); n != 0 {
		t.Errorf("a usage error reached the network (%d calls)", n)
	}
}

func TestRun_subscriptionStatsNoReportNamesProduct(t *testing.T) {
	_, _, _, err := runFamily(t, reports.SubscriptionStats, familiesBucket(), Input{Package: pkg, Product: "premium_monthly"})
	if code := exitCodeOf(t, err); code != 30 {
		t.Fatalf("exit = %d, want 30 (err: %v)", code, err)
	}
	for _, want := range []string{"subscription-stats", pkg, "premium_monthly", "pubsite_prod_rev_123456"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message should name %q: %q", want, err)
		}
	}
}

func TestRun_forbiddenNamesEachFamilysPermission(t *testing.T) {
	for _, tc := range []struct {
		f          reports.Family
		in         Input
		perm, page string
	}{
		{reports.Ratings, Input{Package: pkg}, `"View app information" (global)`, "Download reports > Statistics"},
		{reports.Crashes, Input{Package: pkg}, `"View app information" (global)`, "Download reports > Statistics"},
		{reports.StorePerformance, Input{Package: pkg, Dimension: "country"}, `"View app information" (global)`, "Download reports > Statistics"},
		{reports.SubscriptionStats, Input{Package: pkg, Product: "premium_monthly_v2"}, `"View financial data" (global)`, "Download reports > Financial"},
	} {
		t.Run(tc.f.Name, func(t *testing.T) {
			b := familiesBucket()
			b.Deny = map[string]int{"stats/": 403, "financial-stats/": 403}
			_, _, _, err := runFamily(t, tc.f, b, tc.in)
			if code := exitCodeOf(t, err); code != 11 {
				t.Fatalf("exit = %d, want 11 (err: %v)", code, err)
			}
			for _, want := range []string{tc.perm, tc.page, "Copy Cloud Storage URI"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("403 message should contain %q: %q", want, err)
				}
			}
		})
	}
}
