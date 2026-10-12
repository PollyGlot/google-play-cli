package reports

import (
	"errors"
	"strings"
	"testing"
)

// rows builds "A\n" followed by n rows of "x\n": the smallest export that
// costs memory per row, the shape that once grew a 20 MB archive into
// gigabytes.
func rows(n int) string {
	return "A\n" + strings.Repeat("x\n", n)
}

// TestReader_cellBudgetSpansTheCommand covers the cell bound: each export is
// under it alone, the command is not, so the export that crosses it is
// refused (exit 20) with the bound named and the way out given.
func TestReader_cellBudgetSpansTheCommand(t *testing.T) {
	// A row counts as its cells plus two (its bookkeeping): rows(4) is 5
	// one-cell rows, header included, so 15 cells.
	rd := NewReaderWithLimits("", Limits{Bytes: 1 << 20, Cells: 30})
	for i, name := range []string{"sales/salesreport_202608.zip", "sales/salesreport_202609.zip"} {
		if _, err := rd.Read(name, buildZip(t, member{name: "s.csv", body: rows(4)})); err != nil {
			t.Fatalf("export %d (15 cells, 30 held at most): %v", i, err)
		}
	}
	tbl, err := rd.Read("sales/salesreport_202610.zip", buildZip(t, member{name: "s.csv", body: rows(4)}))
	if tbl.Rows != nil || tbl.Columns != nil {
		t.Errorf("table %+v returned alongside the refusal", tbl)
	}
	boundError(t, err, "s.csv", "30-cell budget", "--since/--until")
}

// TestReader_byteBudgetSpansTheCommand covers the byte bound, on two exports
// that pass alone: the second fails, whatever its container.
func TestReader_byteBudgetSpansTheCommand(t *testing.T) {
	csv := rows(29) // 60 bytes
	for name, raw := range map[string][]byte{
		"stats/installs/installs_com.x_202609_overview.csv": []byte(csv),
		"sales/salesreport_202609.zip":                      buildZip(t, member{name: "s.csv", body: csv}),
	} {
		t.Run(name, func(t *testing.T) {
			rd := NewReaderWithLimits("", Limits{Bytes: 100, Cells: 1 << 20})
			if _, err := rd.Read(name, raw); err != nil {
				t.Fatalf("first export: %v", err)
			}
			_, err := rd.Read(name, raw)
			boundError(t, err, "100-byte budget", "--since/--until")
		})
	}
}

// TestReader_zipDeclaredSizeOverTheBudgetLeft covers the bomb pre-check: a
// member whose declared size exceeds what is left of the command's byte
// budget is refused on the declaration, before a byte is inflated.
func TestReader_zipDeclaredSizeOverTheBudgetLeft(t *testing.T) {
	rd := NewReaderWithLimits("", Limits{Bytes: 1000, Cells: 1 << 20})
	_, err := rd.Read("earnings/earnings_202609.zip", buildZip(t, member{name: "a.csv", body: "a\n1\n", declared: 1001}))
	boundError(t, err, "a.csv", "declares 1001", "1000-byte budget")
}

// TestReader_zipMemberInflatingPastItsDeclarationIsRefused covers a member
// whose stream does not stop where its header says: it fails, naming
// itself, and no truncated table comes back.
func TestReader_zipMemberInflatingPastItsDeclarationIsRefused(t *testing.T) {
	rd := NewReaderWithLimits("", Limits{Bytes: 1 << 20, Cells: 1 << 20})
	tbl, err := rd.Read("earnings/earnings_202609.zip", buildZip(t, member{name: "a.csv", body: "a\n" + strings.Repeat("0\n", 2500), declared: 10}))
	if tbl.Rows != nil {
		t.Errorf("rows %v alongside the error", tbl.Rows)
	}
	boundError(t, err, "a.csv")
}

// TestReader_byteBudgetHoldsOnTheBytesRead covers a source that lies about
// nothing but is simply large: the budget is enforced on the bytes read, not
// on a declared size.
func TestReader_byteBudgetHoldsOnTheBytesRead(t *testing.T) {
	rd := NewReaderWithLimits("", Limits{Bytes: 100, Cells: 1 << 20})
	_, err := rd.Read("stats/installs/installs_com.x_202609_overview.csv", []byte(rows(100)))
	boundError(t, err, "100-byte budget")
	rd = NewReaderWithLimits("", Limits{Bytes: 202, Cells: 1 << 20})
	if _, err := rd.Read("stats/installs/installs_com.x_202609_overview.csv", []byte(rows(100))); err != nil {
		t.Errorf("at the budget (202 bytes): %v", err)
	}
}

// TestReader_packageFilterOnTheHeaderDerivedColumn covers --package on an
// account-wide export: rows are kept on the package column the header
// declares, by preference Package Name, Package ID, then Product ID (the
// finance spelling), and the reader counts the rows read and kept.
func TestReader_packageFilterOnTheHeaderDerivedColumn(t *testing.T) {
	for name, csv := range map[string]string{
		"Product ID (finance spelling)": "Order Number,Product ID,Charged Amount\nGPA.1,com.example.app,1\nGPA.2,com.example.pro,2\nGPA.3,com.example.app,3\n",
		"Package Name":                  "Order Number,Charged Amount,Package Name\nGPA.1,1,com.example.app\nGPA.2,2,com.example.pro\nGPA.3,3,com.example.app\n",
		"Package ID beats Product ID":   "Order Number,Product ID,Package ID\nGPA.1,x,com.example.app\nGPA.2,com.example.app,com.example.pro\nGPA.3,x,com.example.app\n",
	} {
		t.Run(name, func(t *testing.T) {
			rd := NewReader("com.example.app")
			got, err := rd.Read("sales/salesreport_202609.csv", []byte(csv))
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if len(got.Rows) != 2 || got.Rows[0].Value("orderNumber") != "GPA.1" || got.Rows[1].Value("orderNumber") != "GPA.3" {
				t.Errorf("rows = %v", got.Rows)
			}
			if len(got.Columns) != 3 || rd.RowsRead() != 3 || rd.RowsKept() != 2 {
				t.Errorf("columns %v, read %d, kept %d; want 3 columns, 3 read, 2 kept", got.Columns, rd.RowsRead(), rd.RowsKept())
			}
		})
	}
}

// TestReader_packageFilterWithoutPackageColumn: a header with no package
// column cannot be filtered (exit 2); an export with no header at all is an
// empty report.
func TestReader_packageFilterWithoutPackageColumn(t *testing.T) {
	_, err := NewReader("com.example.app").Read("sales/salesreport_202609.csv", []byte("Order Number,Charged Amount\nGPA.1,1\n"))
	var npe *NoPackageColumnError
	if !errors.As(err, &npe) || npe.ExitCode() != 2 || !strings.Contains(err.Error(), "Order Number, Charged Amount") {
		t.Fatalf("err = %v, want a NoPackageColumnError naming the columns present", err)
	}
	tbl, err := NewReader("com.example.app").Read("sales/salesreport_202609.csv", nil)
	if err != nil || len(tbl.Columns) != 0 || len(tbl.Rows) != 0 {
		t.Errorf("empty export: %+v, %v; want an empty report", tbl, err)
	}
}

// TestReader_filteredRowsHoldNoBudget: the cell budget bounds what is held,
// so the rows --package drops do not count against it.
func TestReader_filteredRowsHoldNoBudget(t *testing.T) {
	csv := "Order Number,Product ID\n" + strings.Repeat("GPA.2,com.example.pro\n", 50) + "GPA.1,com.example.app\n"
	// The header and the kept row are 4 cells each (2 cells plus 2).
	rd := NewReaderWithLimits("com.example.app", Limits{Bytes: 1 << 20, Cells: 8})
	got, err := rd.Read("sales/salesreport_202609.csv", []byte(csv))
	if err != nil || len(got.Rows) != 1 {
		t.Fatalf("Read = %v, %v; want the one com.example.app row under an 8-cell budget", got.Rows, err)
	}
}

// TestReader_cellsPastTheHeaderHoldBudget: a row wider than its header grows
// the table by one positional column per extra cell. A dropped row must not
// grow it at all, and a kept row's extra columns are charged to the cell
// budget, so a single very wide row cannot bypass the bound.
func TestReader_cellsPastTheHeaderHoldBudget(t *testing.T) {
	wide := strings.Repeat(",x", 1000)
	t.Run("dropped row adds no column", func(t *testing.T) {
		csv := "Product ID\ncom.other" + wide + "\ncom.example.app\n"
		rd := NewReaderWithLimits("com.example.app", Limits{Bytes: 1 << 20, Cells: 1 << 20})
		got, err := rd.Read("sales/salesreport_202609.csv", []byte(csv))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Columns) != 1 {
			t.Errorf("a row --package dropped added %d columns", len(got.Columns)-1)
		}
	})
	t.Run("kept row's extra columns are charged", func(t *testing.T) {
		csv := "Product ID\ncom.example.app" + wide + "\n"
		rd := NewReaderWithLimits("com.example.app", Limits{Bytes: 1 << 20, Cells: 1500})
		_, err := rd.Read("sales/salesreport_202609.csv", []byte(csv))
		boundError(t, err, "1500-cell budget")
	})
}
