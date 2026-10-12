package familycmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/reports"
	"github.com/PollyGlot/google-play-cli/internal/reports/reportstest"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// Synthetic, format-faithful fixtures of the finance exports: the column
// names of Play's estimated sales and earnings reports, invented values. The
// buyer postcode keeps a leading zero to prove values stay verbatim.
const (
	salesSep = "Order Number,Order Charged Date,Product Title,Product ID,Product Type,SKU ID,Currency of Sale,Item Price,Charged Amount,City of Buyer,State of Buyer,Postal Code of Buyer,Country of Buyer\n" +
		"GPA.3300-0000-0000-00001,2026-09-02,Example Pro (Example App),com.example.app,subscription,premium_monthly,EUR,4.99,4.99,Lyon,ARA,69001,FR\n" +
		"GPA.3300-0000-0000-00002,2026-09-03,Coins (Example Other),com.example.other,inapp,coins_100,USD,0.99,0.99,Springfield,MA,01101,US\n"
	earningsSep = "Description,Transaction Date,Transaction Type,Product Title,Product id,Sku Id,Buyer Country,Buyer State,Buyer Postal Code,Buyer Currency,Amount (Buyer Currency),Merchant Currency,Amount (Merchant Currency)\n" +
		"GPA.3300-0000-0000-00001,Sep 2 2026,Charge,Example Pro (Example App),com.example.app,premium_monthly,FR,,69001,EUR,4.99,EUR,4.99\n" +
		"GPA.3300-0000-0000-00001,Sep 2 2026,Google fee,Example Pro (Example App),com.example.app,premium_monthly,FR,,69001,EUR,-0.75,EUR,-0.75\n" +
		"GPA.3300-0000-0000-00002,Sep 3 2026,Charge,Coins (Example Other),com.example.other,coins_100,US,MA,01101,USD,0.99,EUR,0.91\n"
	earningsAug = "Description,Transaction Date,Transaction Type,Product id,Buyer Country,Amount (Merchant Currency)\n" +
		"GPA.3300-0000-0000-00000,Aug 30 2026,Charge,com.example.app,DE,1.99\n"
)

const (
	salesObject    = "sales/salesreport_202609.zip"
	earningsObject = "earnings/earnings_202609_1234567890123456789-1.zip"
)

func financeBucket(t *testing.T) reportstest.Bucket {
	t.Helper()
	return reportstest.Bucket{Objects: map[string][]byte{
		salesObject:    reportstest.Zip(t, "salesreport_202609.csv", []byte(salesSep)),
		earningsObject: reportstest.Zip(t, "earnings_202609_1234567890123456789-1.csv", reportstest.UTF16LE(earningsSep)),
		"earnings/earnings_202608_1234567890123456789-1.zip": reportstest.Zip(t, "earnings_202608.csv", []byte(earningsAug)),
	}}
}

// runFinance runs a finance family with the Project pinned to another app:
// an account-wide report must not pick the pin up as a row filter.
func runFinance(t *testing.T, f reports.Family, b reportstest.Bucket, in Input) (string, *testkit.Fake, string, error) {
	t.Helper()
	fake := testkit.NewFake(b.Responder())
	rc, _, stderr := reportstest.NewRC(t, fake)
	rc.Resolved = &config.Resolved{DeveloperID: reportstest.DeveloperID, Pin: "com.example.pinned"}
	r, err := Run(rc, f, in)
	if err != nil {
		return "", fake, stderr.String(), err
	}
	return renderJSON(t, r), fake, stderr.String(), nil
}

func fetches(fake *testkit.Fake) []string {
	var out []string
	for _, c := range fake.Calls() {
		if strings.Contains(c.URL, "alt=media") {
			out = append(out, c.URL)
		}
	}
	return out
}

// TestRun_sales_buyerLocationHiddenByDefault pins the whole JSON document of
// a sales month read with no flag: the city, state and postcode are gone
// from the columns and the rows, the country stays, and stderr says how
// many columns were hidden and which flag shows them.
func TestRun_sales_buyerLocationHiddenByDefault(t *testing.T) {
	got, fake, stderr, err := runFinance(t, reports.Sales, financeBucket(t), Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	testkit.Golden(t, "sales.json.golden", []byte(got))
	for _, gone := range []string{"Lyon", "ARA", "69001", "cityOfBuyer", "stateOfBuyer", "postalCodeOfBuyer"} {
		if strings.Contains(got, gone) {
			t.Errorf("JSON leaks %q:\n%s", gone, got)
		}
	}
	if !strings.Contains(got, `"countryOfBuyer": "FR"`) {
		t.Errorf("the buyer country must stay:\n%s", got)
	}
	want := "NOTE: 3 buyer-location columns hidden (City of Buyer, State of Buyer, Postal Code of Buyer): pass --include-buyer-location to show them\n"
	if stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
	if f := fetches(fake); len(f) != 1 || !strings.Contains(f[0], "/b/pubsite_prod_rev_123456/o/sales%2Fsalesreport_202609.zip") {
		t.Errorf("fetches = %v", f)
	}
}

func TestRun_sales_includeBuyerLocation(t *testing.T) {
	got, _, stderr, err := runFinance(t, reports.Sales, financeBucket(t), Input{IncludeBuyerLocation: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	testkit.Golden(t, "sales_buyer_location.json.golden", []byte(got))
	if !strings.Contains(got, `"postalCodeOfBuyer": "01101"`) || !strings.Contains(got, `"cityOfBuyer": "Lyon"`) {
		t.Errorf("--include-buyer-location should show the columns verbatim:\n%s", got)
	}
	if stderr != "" {
		t.Errorf("nothing hidden, so no notice: %q", stderr)
	}
}

func TestRun_earnings_lenientNameUTF16InsideTheZip(t *testing.T) {
	got, _, stderr, err := runFinance(t, reports.Earnings, financeBucket(t), Input{Month: "2026-09"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{
		`"family": "earnings"`, `"2026-09"`, `"` + earningsObject + `"`,
		`"amountMerchantCurrency": "-0.75"`, `"buyerCountry": "US"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("JSON lacks %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"package"`) || strings.Contains(got, `"split"`) {
		t.Errorf("an account-wide report has no package nor split in its identity:\n%s", got)
	}
	if strings.Contains(got, "buyerPostalCode") || strings.Contains(got, "buyerState") {
		t.Errorf("earnings buyer state and postcode must be hidden:\n%s", got)
	}
	if !strings.Contains(stderr, "2 buyer-location columns hidden (Buyer State, Buyer Postal Code)") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRun_earnings_packageFiltersRowsOnTheHeaderColumn(t *testing.T) {
	got, _, _, err := runFinance(t, reports.Earnings, financeBucket(t), Input{Package: "com.example.app", Month: "2026-09"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Count(got, `"description": "GPA`) != 2 || strings.Contains(got, "com.example.other") {
		t.Errorf("--package should keep the two com.example.app rows:\n%s", got)
	}
	if !strings.Contains(got, `"package": "com.example.app"`) {
		t.Errorf("the identity should record the package filter:\n%s", got)
	}
	// Without --package, the pin does not filter: every app's rows come back.
	got, _, _, err = runFinance(t, reports.Earnings, financeBucket(t), Input{Month: "2026-09"})
	if err != nil {
		t.Fatalf("Run without --package: %v", err)
	}
	if strings.Count(got, `"description": "GPA`) != 3 {
		t.Errorf("an account-wide read without --package returns every row:\n%s", got)
	}
}

func TestRun_earnings_rangeMergesAndWarnsOnGaps(t *testing.T) {
	got, _, stderr, err := runFinance(t, reports.Earnings, financeBucket(t), Input{Since: "2026-07", Until: "2026-09", IncludeBuyerLocation: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Count(got, `"description": "GPA`) != 4 || !strings.Contains(got, "\"months\": [\n      \"2026-08\",\n      \"2026-09\"\n    ]") {
		t.Errorf("range read:\n%s", got)
	}
	if !strings.Contains(stderr, "no earnings report for 2026-07: skipped") {
		t.Errorf("stderr = %q", stderr)
	}
}

// TestRun_earnings_packageFiltersEachMonthOnItsOwnColumn covers a range across
// a rename of the package column: August names it Product id, September
// Package Name. Each month is filtered on the column its own header declares,
// so August's rows are not lost to the column September introduced.
func TestRun_earnings_packageFiltersEachMonthOnItsOwnColumn(t *testing.T) {
	b := financeBucket(t)
	b.Objects["earnings/earnings_202609_1234567890123456789-1.zip"] = reportstest.Zip(t, "earnings_202609.csv", []byte(
		"Description,Transaction Date,Package Name,Product id,Amount (Merchant Currency)\n"+
			"GPA.3300-0000-0000-00001,Sep 2 2026,com.example.app,premium_monthly,4.99\n"+
			"GPA.3300-0000-0000-00002,Sep 3 2026,com.example.other,coins_100,0.91\n"))
	got, _, _, err := runFinance(t, reports.Earnings, b, Input{Since: "2026-08", Until: "2026-09", Package: "com.example.app"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{"GPA.3300-0000-0000-00000", "GPA.3300-0000-0000-00001"} {
		if !strings.Contains(got, want) {
			t.Errorf("--package dropped %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, "com.example.other") {
		t.Errorf("--package kept another app's row:\n%s", got)
	}
}

func TestRun_finance_packageWithoutPackageColumn(t *testing.T) {
	b := financeBucket(t)
	b.Objects[salesObject] = reportstest.Zip(t, "salesreport_202609.csv", []byte("Order Number,Charged Amount\nGPA.1,1\n"))
	_, _, _, err := runFinance(t, reports.Sales, b, Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 2 || !strings.Contains(err.Error(), "package column") {
		t.Fatalf("exit %d, err %v", code, err)
	}
}

// TestRun_finance_emptyExportWithPackageIsAnEmptyReport covers an export with
// no header at all: there is no column to miss, so --package reads an empty
// report (exit 0). Exit 2 stays for a header that names no package column.
func TestRun_finance_emptyExportWithPackageIsAnEmptyReport(t *testing.T) {
	b := financeBucket(t)
	b.Objects[salesObject] = reportstest.Zip(t, "salesreport_202609.csv", nil)
	got, _, _, err := runFinance(t, reports.Sales, b, Input{Package: "com.example.app"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(got, `"columns": []`) || !strings.Contains(got, `"rows": []`) {
		t.Errorf("want an empty report:\n%s", got)
	}
}

func TestRun_finance_forbiddenNamesViewFinancialData(t *testing.T) {
	b := financeBucket(t)
	b.Deny = map[string]int{"sales/": 403}
	_, _, _, err := runFinance(t, reports.Sales, b, Input{})
	if code := exitCodeOf(t, err); code != 11 {
		t.Fatalf("exit = %d, want 11 (err: %v)", code, err)
	}
	for _, want := range []string{`"View financial data" (global)`, "Download reports > Financial", "Copy Cloud Storage URI", "--bucket"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("403 message should contain %q: %q", want, err)
		}
	}
}

func TestRun_finance_noReportNamesFamilyAndBucket(t *testing.T) {
	for name, in := range map[string]Input{
		"month absent": {Month: "2025-01"},
		"range absent": {Since: "2025-01", Until: "2025-02"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, _, err := runFinance(t, reports.Sales, financeBucket(t), in)
			if code := exitCodeOf(t, err); code != 30 {
				t.Fatalf("exit = %d, want 30 (err: %v)", code, err)
			}
			msg := err.Error()
			for _, want := range []string{"no sales report for 2025-01", "pubsite_prod_rev_123456", "gplay reports list --family sales"} {
				if !strings.Contains(msg, want) {
					t.Errorf("message should contain %q: %q", want, msg)
				}
			}
			if strings.Contains(msg, "--package") {
				t.Errorf("an account-wide family needs no --package: %q", msg)
			}
		})
	}
	_, _, _, err := runFinance(t, reports.Sales, reportstest.Bucket{}, Input{})
	if code := exitCodeOf(t, err); code != 30 {
		t.Fatalf("empty bucket: exit = %d, want 30 (err: %v)", code, err)
	}
}

func TestRun_finance_oversizedExportRefusedBeforeTheFetch(t *testing.T) {
	b := financeBucket(t)
	b.Sizes = map[string]string{salesObject: "134217729"}
	_, fake, _, err := runFinance(t, reports.Sales, b, Input{})
	if code := exitCodeOf(t, err); code != 20 {
		t.Fatalf("exit = %d, want 20 (err: %v)", code, err)
	}
	if !strings.Contains(err.Error(), salesObject) || !strings.Contains(err.Error(), "134217728") {
		t.Errorf("message should name the object and the cap: %q", err)
	}
	if f := fetches(fake); len(f) != 0 {
		t.Errorf("an export over the cap must not be fetched: %v", f)
	}
}

func TestRun_finance_corruptArchiveIsExit20(t *testing.T) {
	b := financeBucket(t)
	b.Objects[salesObject] = []byte("Order Number\nGPA.1\n")
	_, _, _, err := runFinance(t, reports.Sales, b, Input{})
	if code := exitCodeOf(t, err); code != 20 || !strings.Contains(err.Error(), salesObject) {
		t.Fatalf("exit %d, err %v", code, err)
	}
}

func TestNewCommand_flagsFollowTheFamily(t *testing.T) {
	has := func(c *cobra.Command, name string) bool { return c.Flags().Lookup(name) != nil }
	for _, f := range []reports.Family{reports.Earnings, reports.Sales} {
		c := NewCommand(kernel.Boot{}, f)
		if !has(c, "include-buyer-location") || has(c, "dimension") || !has(c, "package") {
			t.Errorf("%s: want --include-buyer-location and --package, no --dimension", f.Name)
		}
		for _, want := range []string{`"View financial data"`, "--include-buyer-location", "--package", "Download reports > Financial"} {
			if !strings.Contains(c.Long, want) {
				t.Errorf("%s --help lacks %s", f.Name, want)
			}
		}
		if strings.Contains(c.Long, "dimension") || strings.Contains(c.Example, "--dimension") {
			t.Errorf("%s --help talks about dimensions it does not have", f.Name)
		}
	}
	if c := NewCommand(kernel.Boot{}, reports.Installs); has(c, "include-buyer-location") || !has(c, "dimension") {
		t.Error("installs: --dimension, no --include-buyer-location")
	}
}
