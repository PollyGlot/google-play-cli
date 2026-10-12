package reports

import "testing"

// TestMerge_unionOfColumnsFirstSeen covers a range read across a Google column
// change: the result carries the union of the columns in first-seen order, and
// a month that predates a column leaves that cell absent rather than shifting
// the row.
func TestMerge_unionOfColumnsFirstSeen(t *testing.T) {
	aug, err := Decode([]byte("Date,Country,Daily Device Installs\n2026-08-31,FR,4\n"))
	if err != nil {
		t.Fatal(err)
	}
	sep, err := Decode([]byte("Date,Country,Install Events,Daily Device Installs\n2026-09-01,FR,9,5\n"))
	if err != nil {
		t.Fatal(err)
	}
	m := Merge(aug, sep)
	want := []string{"date", "country", "dailyDeviceInstalls", "installEvents"}
	if got := keys(m.Columns); !equal(got, want) {
		t.Fatalf("columns = %v, want %v", got, want)
	}
	if len(m.Rows) != 2 || m.Rows[0]["date"] != "2026-08-31" || m.Rows[1]["date"] != "2026-09-01" {
		t.Fatalf("rows not concatenated in order: %v", m.Rows)
	}
	if _, ok := m.Rows[0]["installEvents"]; ok {
		t.Errorf("August row gained a cell it never had: %v", m.Rows[0])
	}
	if m.Rows[1]["installEvents"] != "9" || m.Rows[1]["dailyDeviceInstalls"] != "5" {
		t.Errorf("September row misaligned: %v", m.Rows[1])
	}
}

func TestMerge_firstHeaderWinsAndEmptyInputs(t *testing.T) {
	a := Table{Columns: []Column{{Key: "date", Header: "Date"}}}
	b := Table{Columns: []Column{{Key: "date", Header: "DATE"}}, Rows: []Row{{"date": "x"}}}
	m := Merge(a, Table{}, b)
	if len(m.Columns) != 1 || m.Columns[0].Header != "Date" || len(m.Rows) != 1 {
		t.Fatalf("merge = %+v", m)
	}
	if got := Merge(); len(got.Columns) != 0 || len(got.Rows) != 0 {
		t.Fatalf("Merge() = %+v, want empty", got)
	}
}
