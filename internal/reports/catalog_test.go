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
