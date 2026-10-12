package familycmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/reports"
	"github.com/PollyGlot/google-play-cli/internal/reports/reportstest"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const (
	pkg    = "com.example.app"
	prefix = "stats/installs/installs_com.example.app_"
)

// Synthetic, format-faithful fixtures: the header of Play's installs export.
const (
	countrySep = "Date,Package Name,Country,Daily Device Installs,Daily Device Uninstalls,Daily User Installs,Daily User Uninstalls,Active Device Installs,Install events\n" +
		"2026-09-01,com.example.app,FR,12,3,11,2,1840,14\n" +
		"2026-09-01,com.example.app,DE,7,0,7,0,912,8\n"
	overviewSep = "Date,Package Name,Daily Device Installs,Daily Device Uninstalls,Active Device Installs\n" +
		"2026-09-01,com.example.app,19,3,2752\n"
	countryAug = "Date,Package Name,Country,Daily Device Installs\n" +
		"2026-08-31,com.example.app,FR,9\n"
)

func installsBucket() reportstest.Bucket {
	return reportstest.Bucket{Objects: map[string][]byte{
		prefix + "202608_country.csv":  reportstest.UTF16LE(countryAug),
		prefix + "202609_country.csv":  reportstest.UTF16LE(countrySep),
		prefix + "202609_overview.csv": reportstest.UTF16LE(overviewSep),
		// Another package whose name the com.example.app prefix also matches.
		"stats/installs/installs_com.example.app_pro_202610_overview.csv": reportstest.UTF16LE(overviewSep),
	}}
}

func run(t *testing.T, b reportstest.Bucket, in Input) (output.Renderable, *testkit.Fake, string, error) {
	t.Helper()
	fake := testkit.NewFake(b.Responder())
	rc, _, stderr := reportstest.NewRC(t, fake)
	r, err := Run(rc, reports.Installs, in)
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

func renderTable(t *testing.T, r output.Renderable) string {
	t.Helper()
	var buf bytes.Buffer
	if err := r.Renderers().Table(&buf); err != nil {
		t.Fatalf("render table: %v", err)
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

// TestRun_monthAndDimension_endToEnd reads one month of one dimension and
// pins the whole JSON document: the envelope, the header-derived columns and
// the verbatim string values. The fixture's "Install events" column is not
// known to gplay anywhere: it flows through from the header.
func TestRun_monthAndDimension_endToEnd(t *testing.T) {
	r, fake, _, err := run(t, installsBucket(), Input{Package: pkg, Month: "2026-09", Dimension: "country"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	testkit.Golden(t, "installs_country.json.golden", []byte(renderJSON(t, r)))

	var fetched []string
	for _, c := range fake.Calls() {
		if strings.Contains(c.URL, "alt=media") {
			fetched = append(fetched, c.URL)
		}
	}
	if len(fetched) != 1 || !strings.Contains(fetched[0], "/b/pubsite_prod_rev_123456/o/stats%2Finstalls%2Finstalls_com.example.app_202609_country.csv") {
		t.Errorf("fetches = %v, want the one 2026-09 country export of the derived bucket", fetched)
	}
}

// TestRun_newColumnFlowsThrough is the acceptance criterion as a test: a
// column Google adds appears in the JSON, keyed by the header rule, with no
// code change.
func TestRun_newColumnFlowsThrough(t *testing.T) {
	b := installsBucket()
	b.Objects[prefix+"202609_country.csv"] = reportstest.UTF16LE(
		"Date,Country,Daily Device Installs,Brand New Metric (7-day)\n2026-09-01,FR,12,00042\n")
	r, _, _, err := run(t, b, Input{Package: pkg, Month: "2026-09", Dimension: "country"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := renderJSON(t, r)
	testkit.Golden(t, "installs_new_column.json.golden", []byte(got))
	for _, want := range []string{
		`"key": "brandNewMetric7Day"`,
		`"header": "Brand New Metric (7-day)"`,
		`"brandNewMetric7Day": "00042"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("JSON lacks %s:\n%s", want, got)
		}
	}
	if !strings.Contains(renderTable(t, r), "Brand New Metric (7-day)") {
		t.Error("the default table should show a column Google adds")
	}
}

func TestRun_latestMonthDefaultsToOverview(t *testing.T) {
	r, _, _, err := run(t, installsBucket(), Input{Package: pkg})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := renderJSON(t, r)
	if !strings.Contains(got, `"split": "overview"`) || !strings.Contains(got, `"2026-09"`) || !strings.Contains(got, `"dailyDeviceInstalls": "19"`) {
		t.Errorf("default read is not the 2026-09 overview:\n%s", got)
	}
	// com.example.app_pro's later month must not count as the latest.
	if strings.Contains(got, "2026-10") {
		t.Errorf("another package's export leaked in:\n%s", got)
	}
}

func TestRun_noDefaultDimension_usageNamesPresentOnes(t *testing.T) {
	b := installsBucket()
	delete(b.Objects, prefix+"202609_overview.csv")
	_, fake, _, err := run(t, b, Input{Package: pkg})
	if code := exitCodeOf(t, err); code != 2 {
		t.Fatalf("exit = %d, want 2 (err: %v)", code, err)
	}
	if !strings.Contains(err.Error(), "--dimension") || !strings.Contains(err.Error(), "country") {
		t.Errorf("message should name --dimension and the present dimensions: %q", err)
	}
	for _, c := range fake.Calls() {
		if strings.Contains(c.URL, "alt=media") {
			t.Errorf("nothing should be fetched: %s", c.URL)
		}
	}
}

func TestRun_unknownDimension_usage(t *testing.T) {
	_, _, _, err := run(t, installsBucket(), Input{Package: pkg, Month: "2026-09", Dimension: "carrier"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Fatalf("exit = %d, want 2 (err: %v)", code, err)
	}
	if !strings.Contains(err.Error(), "country, overview") {
		t.Errorf("message should list the dimensions of 2026-09: %q", err)
	}
}

func TestRun_rangeMergesWithColumnUnionAndWarnsOnGaps(t *testing.T) {
	r, _, stderr, err := run(t, installsBucket(), Input{Package: pkg, Since: "2026-07", Until: "2026-09", Dimension: "country"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := renderJSON(t, r)
	if !strings.Contains(got, "\"months\": [\n      \"2026-08\",\n      \"2026-09\"\n    ]") {
		t.Errorf("months read should be 2026-08 and 2026-09:\n%s", got)
	}
	// August predates "Install events": its row has no such key, September's has.
	aug := got[strings.Index(got, `"date": "2026-08-31"`):]
	aug = aug[:strings.Index(aug, "}")]
	if strings.Contains(aug, "installEvents") {
		t.Errorf("August row gained a cell:\n%s", aug)
	}
	if !strings.Contains(got, `"installEvents": "14"`) {
		t.Errorf("September's extra column is missing:\n%s", got)
	}
	if !strings.Contains(stderr, "2026-07") || !strings.Contains(stderr, "skipped") {
		t.Errorf("stderr should warn that 2026-07 was skipped: %q", stderr)
	}
}

func TestRun_noReport_usageNamesPackageFamilyBucket(t *testing.T) {
	for name, in := range map[string]Input{
		"empty bucket":   {Package: "com.other.app"},
		"month absent":   {Package: pkg, Month: "2025-01"},
		"range all gaps": {Package: pkg, Since: "2025-01", Until: "2025-03"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, _, err := run(t, installsBucket(), in)
			if code := exitCodeOf(t, err); code != 2 {
				t.Fatalf("exit = %d, want 2 (err: %v)", code, err)
			}
			for _, want := range []string{in.Package, "installs", "pubsite_prod_rev_123456"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message should name %q: %q", want, err)
				}
			}
		})
	}
}

func TestRun_forbidden_namesPermissionAndConsoleButton(t *testing.T) {
	b := installsBucket()
	b.Deny = map[string]int{"stats/": 403}
	_, _, _, err := run(t, b, Input{Package: pkg})
	if code := exitCodeOf(t, err); code != 11 {
		t.Fatalf("exit = %d, want 11 (err: %v)", code, err)
	}
	for _, want := range []string{`"View app information" (global)`, "Copy Cloud Storage URI", "--bucket", "Download reports > Statistics"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("403 message should contain %q: %q", want, err)
		}
	}
}

func TestRun_badFlags_usageWithoutNetwork(t *testing.T) {
	for name, in := range map[string]Input{
		"bad month":          {Package: pkg, Month: "2026-13"},
		"month and range":    {Package: pkg, Month: "2026-09", Since: "2026-01", Until: "2026-02"},
		"half range":         {Package: pkg, Since: "2026-01"},
		"inverted range":     {Package: pkg, Since: "2026-05", Until: "2026-01"},
		"no package, no pin": {},
	} {
		t.Run(name, func(t *testing.T) {
			_, fake, _, err := run(t, installsBucket(), in)
			if code := exitCodeOf(t, err); code != 2 {
				t.Fatalf("exit = %d, want 2 (err: %v)", code, err)
			}
			if n := len(fake.Calls()); n != 0 {
				t.Errorf("a usage error reached the network (%d calls)", n)
			}
		})
	}
}

func TestRun_bucketOverrideNeedsNoDeveloperID(t *testing.T) {
	fake := testkit.NewFake(installsBucket().Responder())
	rc, obs, _ := reportstest.NewRC(t, fake)
	rc.Resolved = &config.Resolved{}
	if _, err := Run(rc, reports.Installs, Input{Package: pkg, Bucket: "custom-bucket", Month: "2026-09"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, c := range fake.Calls() {
		if !strings.Contains(c.URL, "/b/custom-bucket/o") {
			t.Errorf("call outside the --bucket override: %s", c.URL)
		}
	}
	scopes := obs.Scopes()
	if len(scopes) != 1 || scopes[0] != token.StorageReadOnlyScope {
		t.Errorf("token scopes = %v, want only %s", scopes, token.StorageReadOnlyScope)
	}
}

func TestPayload_tableDefaultsAndColumns(t *testing.T) {
	r, _, _, err := run(t, installsBucket(), Input{Package: pkg, Month: "2026-09", Dimension: "country"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := renderTable(t, r)
	head := strings.SplitN(got, "\n", 2)[0]
	if !strings.Contains(head, "Country") || !strings.Contains(head, "Daily Device Installs") {
		t.Errorf("default table header = %q", head)
	}
	if strings.Contains(head, "Package Name") || strings.Contains(head, "Install events") {
		t.Errorf("default table should hide the constant package and the event counters: %q", head)
	}

	r, _, _, err = run(t, installsBucket(), Input{Package: pkg, Month: "2026-09", Dimension: "country", Columns: "country,installEvents"})
	if err != nil {
		t.Fatalf("Run with --columns: %v", err)
	}
	if head := strings.SplitN(renderTable(t, r), "\n", 2)[0]; strings.Join(strings.Fields(head), " ") != "Country Install events" {
		t.Errorf("--columns header = %q", head)
	}

	_, _, _, err = run(t, installsBucket(), Input{Package: pkg, Month: "2026-09", Dimension: "country", Columns: "nope"})
	if code := exitCodeOf(t, err); code != 2 || !strings.Contains(err.Error(), "dailyDeviceInstalls") {
		t.Errorf("unknown --columns key: exit %d, %v", code, err)
	}
}
