package reports

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/reports/bucket"
)

func TestCatalogue_financeFamilies(t *testing.T) {
	for _, tc := range []struct {
		f      Family
		name   string
		prefix string
	}{
		{Earnings, "earnings", "earnings/earnings_"},
		{Sales, "sales", "sales/salesreport_"},
	} {
		f := tc.f
		if f.Name != tc.name || f.Prefix != tc.prefix || f.Scope != AccountWide || f.Container != ZIP {
			t.Errorf("%s = %+v", tc.name, f)
		}
		if f.Permission != bucket.ViewFinancialData || f.Page != "Financial" || !f.BuyerLocation {
			t.Errorf("%s: permission %q, page %q, buyer location %v", tc.name, f.Permission, f.Page, f.BuyerLocation)
		}
		if f.MaxObjectBytes != MaxZipObjectBytes {
			t.Errorf("%s read cap = %d, want the ZIP object cap", tc.name, f.MaxObjectBytes)
		}
		// Account-wide: --package never narrows the listing.
		if f.ListPrefix("com.example.app") != tc.prefix {
			t.Errorf("%s ListPrefix = %q", tc.name, f.ListPrefix("com.example.app"))
		}
		found := false
		for _, c := range Catalogue() {
			found = found || c.Name == tc.name
		}
		if !found {
			t.Errorf("%s is not in the catalogue", tc.name)
		}
	}
	if Installs.Container != CSV || Installs.BuyerLocation {
		t.Error("statistics families stay plain CSV with no buyer location")
	}
}

func TestObjects_financeNames(t *testing.T) {
	objs, skipped := Earnings.Objects(listing(
		"earnings/earnings_202609_1234567890123456789-1.zip",
		"earnings/earnings_202608.ZIP",
		"earnings/earnings_202609_1234567890123456789-2.zip",
		"earnings/readme.txt",
	), "")
	if len(objs) != 3 || len(skipped) != 1 {
		t.Fatalf("objects = %+v, skipped %v", objs, skipped)
	}
	sales, _ := Sales.Objects(listing("sales/salesreport_202609.zip"), "")
	if len(sales) != 1 || sales[0].Month != "202609" || sales[0].Split != "" || sales[0].Package != "" {
		t.Fatalf("sales objects = %+v", sales)
	}
}

func TestObjects_accountWideIgnoresThePackageFilter(t *testing.T) {
	// An account-wide export holds every app's rows: a --package asks for a
	// row filter, so the export itself must survive the listing.
	objs, _ := Sales.Objects(listing("sales/salesreport_202609.zip"), "com.example.app")
	if len(objs) != 1 {
		t.Fatalf("objects = %+v, want the account-wide export kept", objs)
	}
}

func TestSelect_accountWideReadsEveryExportOfTheMonth(t *testing.T) {
	objs, _ := Earnings.Objects(listing(
		"earnings/earnings_202608_1234-1.zip",
		"earnings/earnings_202609_1234-1.zip",
		"earnings/earnings_202609_1234-2.zip",
	), "")
	sel, err := Earnings.Select(objs, nil, "")
	if err != nil {
		t.Fatalf("Select latest: %v", err)
	}
	if sel.Split != "" || len(sel.Months) != 1 || sel.Months[0] != "202609" || len(sel.Objects) != 2 ||
		sel.Objects[0].Name != "earnings/earnings_202609_1234-1.zip" || sel.Objects[1].Name != "earnings/earnings_202609_1234-2.zip" {
		t.Fatalf("selection = %+v, want both 2026-09 files in name order", sel)
	}

	sel, err = Earnings.Select(objs, []string{"202607", "202608", "202609"}, "")
	if err != nil {
		t.Fatalf("Select range: %v", err)
	}
	if strings.Join(sel.Months, ",") != "202608,202609" || len(sel.Objects) != 3 || strings.Join(sel.Missing, ",") != "202607" {
		t.Fatalf("range selection = %+v", sel)
	}

	if _, err := Earnings.Select(objs, []string{"202501"}, ""); !errors.Is(err, ErrNoReport) {
		t.Errorf("absent month: err = %v, want ErrNoReport", err)
	}
}

func TestFamilyDecode_zipMemberEncodingDetectedByBOM(t *testing.T) {
	csv := "Order Number,Product ID,Country of Buyer\nGPA.3300-0001,com.example.app,FR\n"
	raw := buildZip(t,
		member{name: "salesreport_202609.csv", body: string(encodeUTF16(csv, binary.LittleEndian, true))},
		member{name: "salesreport_202609_part2.csv", body: "\uFEFFOrder Number,Product ID,Country of Buyer\nGPA.3300-0002,com.example.pro,DE\n"},
	)
	tbl, err := Sales.Decode("sales/salesreport_202609.zip", raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(tbl.Columns) != 3 || tbl.Columns[1].Key != "productId" || len(tbl.Rows) != 2 ||
		tbl.Rows[0]["orderNumber"] != "GPA.3300-0001" || tbl.Rows[1]["countryOfBuyer"] != "DE" {
		t.Fatalf("table = %+v", tbl)
	}
}

func TestFamilyDecode_csvFamilyIsUnchanged(t *testing.T) {
	tbl, err := Installs.Decode("stats/installs/installs_com.x_202609_overview.csv", []byte("Date,Daily Device Installs\n2026-09-01,4\n"))
	if err != nil || len(tbl.Rows) != 1 || tbl.Rows[0]["dailyDeviceInstalls"] != "4" {
		t.Fatalf("Decode = %+v, %v", tbl, err)
	}
}

func TestFamilyDecode_zipRefusalPropagates(t *testing.T) {
	_, err := Sales.Decode("sales/salesreport_202609.ZIP", []byte("not a zip"))
	var ae *RefusedError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v, want the RefusedError", err)
	}
}
