package reports

import (
	"errors"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/gcs"
)

func listing(names ...string) []gcs.Object {
	out := make([]gcs.Object, len(names))
	for i, n := range names {
		out[i] = gcs.Object{Name: n, Size: "10", Updated: "2026-10-04T08:00:00.000Z"}
	}
	return out
}

func TestCatalogue_installs(t *testing.T) {
	f := Installs
	if f.ListPrefix("com.example.app") != "stats/installs/installs_com.example.app_" {
		t.Errorf("ListPrefix = %q", f.ListPrefix("com.example.app"))
	}
	if f.ListPrefix("") != "stats/installs/installs_" {
		t.Errorf("ListPrefix(all) = %q", f.ListPrefix(""))
	}
	if names := Names(); len(names) == 0 || names[0] != "installs" {
		t.Errorf("Names() = %v", names)
	}
	if cat := Catalogue(); len(cat) == 0 || cat[0].Name != "installs" {
		t.Errorf("Catalogue() = %+v", cat)
	}
}

func TestObjects_parsesNamesLeniently(t *testing.T) {
	f := Installs
	objs, skipped := f.Objects(listing(
		"stats/installs/installs_com.example.app_202609_country.csv",
		"stats/installs/installs_com.example.app_202609_app_version.csv",
		"stats/installs/installs_com.ex_ample_202608_overview.CSV", // underscore in the package, upper-case extension
		"stats/installs/installs_com.example.app_202607.csv",       // no split segment
		"stats/installs/installs_com.example.app_2026_country.csv", // not a month
		"stats/installs/README.txt",
	), "")
	if len(skipped) != 2 {
		t.Errorf("skipped = %v, want the two unparseable names", skipped)
	}
	want := []Object{
		{Family: "installs", Package: "com.ex_ample", Month: "202608", Split: "overview"},
		{Family: "installs", Package: "com.example.app", Month: "202607", Split: ""},
		{Family: "installs", Package: "com.example.app", Month: "202609", Split: "app_version"},
		{Family: "installs", Package: "com.example.app", Month: "202609", Split: "country"},
	}
	if len(objs) != len(want) {
		t.Fatalf("objects = %+v", objs)
	}
	for i, w := range want {
		g := objs[i]
		if g.Family != w.Family || g.Package != w.Package || g.Month != w.Month || g.Split != w.Split {
			t.Errorf("object %d = %+v, want %+v", i, g, w)
		}
		if g.Size != "10" || g.Updated == "" || !strings.HasPrefix(g.Name, "stats/installs/") {
			t.Errorf("object %d lost its listing metadata: %+v", i, g)
		}
	}
}

func TestObjects_packageFilterIsExact(t *testing.T) {
	// The list prefix for com.x also matches com.x_y: the parsed package, not
	// the prefix, decides.
	f := Installs
	objs, _ := f.Objects(listing(
		"stats/installs/installs_com.x_202609_country.csv",
		"stats/installs/installs_com.x_y_202609_country.csv",
	), "com.x")
	if len(objs) != 1 || objs[0].Package != "com.x" {
		t.Fatalf("objects = %+v", objs)
	}
}

func TestObjects_accountWideFamily(t *testing.T) {
	// The catalogue is data: an account-wide family (the finance exports) is
	// one entry with no package segment and a lenient name suffix.
	f := Family{Name: "earnings", Prefix: "earnings/earnings_", Scope: AccountWide}
	objs, skipped := f.Objects(listing(
		"earnings/earnings_202609_1234567890123456789-1.zip",
		"earnings/earnings_202608.zip",
	), "")
	if len(skipped) != 0 || len(objs) != 2 {
		t.Fatalf("objects = %+v, skipped %v", objs, skipped)
	}
	if objs[0].Month != "202608" || objs[0].Package != "" || objs[1].Split != "1234567890123456789-1" {
		t.Errorf("objects = %+v", objs)
	}
}

func installsObjects(t *testing.T, names ...string) []Object {
	t.Helper()
	f := Installs
	full := make([]string, len(names))
	for i, n := range names {
		full[i] = "stats/installs/installs_com.example.app_" + n + ".csv"
	}
	objs, _ := f.Objects(listing(full...), "com.example.app")
	return objs
}

func TestSelect_latestMonthDefaultsToOverview(t *testing.T) {
	f := Installs
	objs := installsObjects(t, "202608_overview", "202609_country", "202609_overview")
	sel, err := f.Select(objs, nil, "")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if sel.Split != "overview" || len(sel.Objects) != 1 || sel.Objects[0].Month != "202609" {
		t.Fatalf("selection = %+v", sel)
	}
}

func TestSelect_noDefaultNamesThePresentDimensions(t *testing.T) {
	f := Installs
	objs := installsObjects(t, "202609_country", "202609_app_version", "202608_carrier")
	_, err := f.Select(objs, nil, "")
	var se *SplitError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want *SplitError", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "app_version, country") || strings.Contains(msg, "carrier") {
		t.Errorf("message should list the dimensions of 2026-09 only: %q", msg)
	}
	if !strings.Contains(msg, "--dimension") {
		t.Errorf("message should name --dimension: %q", msg)
	}
}

func TestSelect_dimensionValidatedAgainstTheBucket(t *testing.T) {
	f := Installs
	objs := installsObjects(t, "202609_country", "202609_brand_new_split")
	// A dimension Google adds works the day it appears: no frozen list.
	sel, err := f.Select(objs, []string{"202609"}, "brand_new_split")
	if err != nil || len(sel.Objects) != 1 || sel.Objects[0].Split != "brand_new_split" {
		t.Fatalf("selection = %+v, %v", sel, err)
	}
	_, err = f.Select(objs, []string{"202609"}, "carrier")
	var se *SplitError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), `"carrier"`) || !strings.Contains(err.Error(), "brand_new_split, country") {
		t.Fatalf("err = %v", err)
	}
}

func TestSelect_rangeSkipsMissingMonths(t *testing.T) {
	f := Installs
	objs := installsObjects(t, "202606_country", "202608_country", "202608_overview")
	sel, err := f.Select(objs, []string{"202606", "202607", "202608"}, "country")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(sel.Objects) != 2 || sel.Objects[0].Month != "202606" || sel.Objects[1].Month != "202608" {
		t.Errorf("objects = %+v", sel.Objects)
	}
	if !equal(sel.Missing, []string{"202607"}) {
		t.Errorf("missing = %v", sel.Missing)
	}
	// Without --dimension, overview is picked when any month of the range has it.
	sel, err = f.Select(objs, []string{"202606", "202607", "202608"}, "")
	if err != nil || sel.Split != "overview" || len(sel.Objects) != 1 || !equal(sel.Missing, []string{"202606", "202607"}) {
		t.Errorf("default over a range = %+v, %v", sel, err)
	}
}

func TestSelect_noReport(t *testing.T) {
	f := Installs
	if _, err := f.Select(nil, nil, ""); !errors.Is(err, ErrNoReport) {
		t.Errorf("empty bucket: err = %v, want ErrNoReport", err)
	}
	objs := installsObjects(t, "202609_country")
	if _, err := f.Select(objs, []string{"202605"}, "country"); !errors.Is(err, ErrNoReport) {
		t.Errorf("month absent: err = %v, want ErrNoReport", err)
	}
}

func TestCatalogue_statisticsFamilies(t *testing.T) {
	// The four #695 families sit after installs, in `reports list` order, each
	// with its bucket prefix and the permission its 403 names.
	want := []struct {
		name, prefix string
		perm         string
	}{
		{"installs", "stats/installs/installs_", "View app information"},
		{"ratings", "stats/ratings/ratings_", "View app information"},
		{"crashes", "stats/crashes/crashes_", "View app information"},
		{"store-performance", "stats/store_performance/store_performance_", "View app information"},
		{"subscription-stats", "financial-stats/subscriptions/subscriptions_", "View financial data"},
	}
	cat := Catalogue()
	if len(cat) < len(want) {
		t.Fatalf("Catalogue() = %d families, want at least %d", len(cat), len(want))
	}
	for i, w := range want {
		f := cat[i]
		if f.Name != w.name || f.Prefix != w.prefix || string(f.Permission) != w.perm || f.Scope != PerPackage {
			t.Errorf("family %d = %s %s %q scope %d, want %s %s %q per package", i, f.Name, f.Prefix, f.Permission, f.Scope, w.name, w.prefix, w.perm)
		}
	}
	if StorePerformance.DefaultSplit != "" {
		t.Errorf("store performance has no overview file, so no default split: %q", StorePerformance.DefaultSplit)
	}
	if !SubscriptionStats.PerProduct || Installs.PerProduct {
		t.Error("only subscription stats carry a product id in their object names")
	}
}

func TestObjects_productIDWithUnderscores(t *testing.T) {
	// subscriptions_<package>_<productId>_<YYYYMM>_<dimension>.csv: a product
	// id may hold underscores (and digits), so the parse anchors on the
	// package when known, then on the month and the final dimension.
	f := SubscriptionStats
	const p = "financial-stats/subscriptions/subscriptions_"
	objs, skipped := f.Objects(listing(
		p+"com.example.app_premium_monthly_v2_202609_country.csv",
		p+"com.example.app_promo_202401_202609_country.csv", // a product id ending in a month-like run
		p+"com.example.app_gold_202608.csv",                 // no dimension segment
		p+"com.example.app_202609_country.csv",              // no product segment
	), "com.example.app")
	if len(skipped) != 1 || !strings.HasSuffix(skipped[0], "app_202609_country.csv") {
		t.Errorf("skipped = %v, want the name without a product", skipped)
	}
	want := []Object{
		{Family: "subscription-stats", Package: "com.example.app", Product: "gold", Month: "202608", Split: ""},
		{Family: "subscription-stats", Package: "com.example.app", Product: "premium_monthly_v2", Month: "202609", Split: "country"},
		{Family: "subscription-stats", Package: "com.example.app", Product: "promo_202401", Month: "202609", Split: "country"},
	}
	if len(objs) != len(want) {
		t.Fatalf("objects = %+v", objs)
	}
	for i, w := range want {
		g := objs[i]
		if g.Family != w.Family || g.Package != w.Package || g.Product != w.Product || g.Month != w.Month || g.Split != w.Split {
			t.Errorf("object %d = %+v, want %+v", i, g, w)
		}
	}
}

func TestObjects_productPackageAnchor(t *testing.T) {
	f := SubscriptionStats
	const p = "financial-stats/subscriptions/subscriptions_"
	// With the package known, an underscore in it is no ambiguity.
	objs, _ := f.Objects(listing(p+"com.ex_ample_pro_yearly_202609_country.csv"), "com.ex_ample")
	if len(objs) != 1 || objs[0].Package != "com.ex_ample" || objs[0].Product != "pro_yearly" {
		t.Fatalf("anchored on the package: %+v", objs)
	}
	// Without it (an unfiltered inventory), the package ends at the first
	// underscore: Android package names rarely hold one, product ids often do.
	objs, _ = f.Objects(listing(p+"com.example.app_pro_yearly_202609_country.csv"), "")
	if len(objs) != 1 || objs[0].Package != "com.example.app" || objs[0].Product != "pro_yearly" {
		t.Fatalf("unanchored: %+v", objs)
	}
	if got := f.ProductListPrefix("com.example.app", "pro_yearly"); got != p+"com.example.app_pro_yearly_" {
		t.Errorf("ProductListPrefix = %q", got)
	}
}

func TestSelect_noDefaultSplitFamilyNamesThePresentOnes(t *testing.T) {
	f := StorePerformance
	const p = "stats/store_performance/store_performance_com.example.app_"
	objs, _ := f.Objects(listing(p+"202609_country.csv", p+"202609_traffic_source.csv"), "com.example.app")
	_, err := f.Select(objs, nil, "")
	var se *SplitError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want *SplitError", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "country, traffic_source") || !strings.Contains(msg, "--dimension") || strings.Contains(msg, `""`) {
		t.Errorf("message = %q", msg)
	}
}
