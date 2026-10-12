package reports

import (
	"bytes"
	"encoding/binary"
	"encoding/csv"
	"testing"
	"unicode/utf16"
)

// encodeUTF16 encodes s as UTF-16 in the given byte order, with or without a
// BOM: the shapes Play's monthly exports come in.
func encodeUTF16(s string, order binary.ByteOrder, bom bool) []byte {
	var buf bytes.Buffer
	if bom {
		var b [2]byte
		order.PutUint16(b[:], 0xFEFF)
		buf.Write(b[:])
	}
	for _, u := range utf16.Encode([]rune(s)) {
		var b [2]byte
		order.PutUint16(b[:], u)
		buf.Write(b[:])
	}
	return buf.Bytes()
}

const installsCSV = "Date,Package Name,Country,Daily Device Installs,Daily User Uninstalls\n" +
	"2026-09-01,com.example.app,FR,12,3\n" +
	"2026-09-01,com.example.app,DE,7,0\n"

func TestDecode_encodingsDetectedByBOM(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"utf-16 LE with BOM", encodeUTF16(installsCSV, binary.LittleEndian, true)},
		{"utf-16 BE with BOM", encodeUTF16(installsCSV, binary.BigEndian, true)},
		{"utf-16 LE without BOM", encodeUTF16(installsCSV, binary.LittleEndian, false)},
		{"utf-16 BE without BOM", encodeUTF16(installsCSV, binary.BigEndian, false)},
		{"utf-8 with BOM", append([]byte{0xEF, 0xBB, 0xBF}, installsCSV...)},
		{"utf-8 without BOM", []byte(installsCSV)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tbl, err := Decode(tc.raw)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			wantKeys := []string{"date", "packageName", "country", "dailyDeviceInstalls", "dailyUserUninstalls"}
			if got := keys(tbl.Columns); !equal(got, wantKeys) {
				t.Fatalf("keys = %v, want %v", got, wantKeys)
			}
			if tbl.Columns[3].Header != "Daily Device Installs" {
				t.Errorf("header = %q, want the CSV text verbatim", tbl.Columns[3].Header)
			}
			if len(tbl.Rows) != 2 {
				t.Fatalf("rows = %d, want 2", len(tbl.Rows))
			}
			if got := tbl.Rows[1].Value("country"); got != "DE" {
				t.Errorf("row 2 country = %q, want DE", got)
			}
			if got := tbl.Rows[0].Value("dailyDeviceInstalls"); got != "12" {
				t.Errorf("row 1 dailyDeviceInstalls = %q, want 12", got)
			}
		})
	}
}

func TestDecode_valuesVerbatimAndQuoted(t *testing.T) {
	csv := "Postcode,Product ID,Note\n" +
		"00501,007,\"a, \"\"quoted\"\" note\nover two lines\"\n" +
		"  01  ,,Très bien 🎉\n"
	tbl, err := Decode(encodeUTF16(csv, binary.LittleEndian, true))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	r := tbl.Rows[0]
	if r.Value("postcode") != "00501" || r.Value("productId") != "007" {
		t.Errorf("leading zeros lost: %v", r)
	}
	if r.Value("note") != "a, \"quoted\" note\nover two lines" {
		t.Errorf("quoted field = %q", r.Value("note"))
	}
	r = tbl.Rows[1]
	if r.Value("postcode") != "  01  " {
		t.Errorf("value not verbatim: %q", r.Value("postcode"))
	}
	if v, ok := r.Get("productId"); !ok || v != "" {
		t.Errorf("empty cell = %q (present %v), want an empty string", v, ok)
	}
	if r.Value("note") != "Très bien 🎉" {
		t.Errorf("non-ASCII = %q", r.Value("note"))
	}
}

// TestDecode_headerKeyRule pins the documented header -> JSON key rule (ADR-0050):
// words are the runs of letters and digits, the first lowercased, each next one
// capitalised with the rest lowercased; a header with no word gets column<N>.
func TestDecode_headerKeyRule(t *testing.T) {
	for header, want := range map[string]string{
		"Daily Device Installs":              "dailyDeviceInstalls",
		"Review Submit Date and Time":        "reviewSubmitDateAndTime",
		"Package Name":                       "packageName",
		"Daily ANRs":                         "dailyAnrs",
		"Store listing acquisitions (7-day)": "storeListingAcquisitions7Day",
		"Country of Buyer":                   "countryOfBuyer",
		"  Date  ":                           "date",
		"Charged Amount (EUR)":               "chargedAmountEur",
		"Pays d'achat":                       "paysDAchat",
		"Größe":                              "größe",
		"%":                                  "column1",
	} {
		var doc bytes.Buffer
		w := csv.NewWriter(&doc)
		_ = w.Write([]string{header})
		w.Flush()
		tbl, err := Decode(doc.Bytes())
		if err != nil {
			t.Fatalf("Decode(%q): %v", header, err)
		}
		if len(tbl.Columns) != 1 || tbl.Columns[0].Key != want {
			t.Errorf("header %q -> %+v, want key %q", header, tbl.Columns, want)
		}
	}
}

func TestDecode_duplicateAndBlankHeadersGetUniqueKeys(t *testing.T) {
	tbl, err := Decode([]byte("Date,Installs,Installs,,Installs 2,%\n1,2,3,4,5,6\n"))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := []string{"date", "installs", "installs2", "column4", "installs22", "column6"}
	if got := keys(tbl.Columns); !equal(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	if tbl.Rows[0].Value("installs2") != "3" || tbl.Rows[0].Value("installs22") != "5" || tbl.Rows[0].Value("column4") != "4" {
		t.Errorf("cells misaligned: %v", tbl.Rows[0])
	}
}

func TestDecode_raggedRows(t *testing.T) {
	// A short row leaves its missing cells absent; an extra non-empty cell is
	// kept under a positional key rather than dropped.
	tbl, err := Decode([]byte("Date,Country\n2026-09-01\n2026-09-02,FR,surplus\n2026-09-03,DE,\n"))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got := keys(tbl.Columns); !equal(got, []string{"date", "country", "column3"}) {
		t.Fatalf("keys = %v", got)
	}
	if _, ok := tbl.Rows[0].Get("country"); ok {
		t.Errorf("short row carries a country cell: %v", tbl.Rows[0])
	}
	if tbl.Rows[1].Value("column3") != "surplus" {
		t.Errorf("extra cell lost: %v", tbl.Rows[1])
	}
	if _, ok := tbl.Rows[2].Get("column3"); ok {
		t.Errorf("empty trailing cell kept: %v", tbl.Rows[2])
	}
}

func TestDecode_emptyReport(t *testing.T) {
	for _, raw := range [][]byte{nil, {0xFF, 0xFE}, []byte("\n\n")} {
		tbl, err := Decode(raw)
		if err != nil {
			t.Fatalf("Decode(%q): %v", raw, err)
		}
		if len(tbl.Columns) != 0 || len(tbl.Rows) != 0 {
			t.Errorf("Decode(%q) = %+v, want an empty table", raw, tbl)
		}
	}
	tbl, err := Decode([]byte("Date,Country\n"))
	if err != nil || len(tbl.Columns) != 2 || len(tbl.Rows) != 0 {
		t.Errorf("header-only: %+v, %v", tbl, err)
	}
}

func TestDecode_malformedCSVFails(t *testing.T) {
	if _, err := Decode([]byte("Date,Note\n2026-09-01,\"unterminated\n")); err == nil {
		t.Fatal("want an error on an unterminated quote")
	}
}

func keys(cols []Column) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Key
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestDecode_oddUTF16IsRefused covers a UTF-16 export cut mid code unit: the
// last byte cannot be decoded, so the report is refused (exit 20) rather than
// returned with its last amount silently cut short.
func TestDecode_oddUTF16IsRefused(t *testing.T) {
	for name, raw := range map[string][]byte{
		"LE with BOM":    append(encodeUTF16("Amount\n12", binary.LittleEndian, true), '3'),
		"BE with BOM":    append(encodeUTF16("Amount\n12", binary.BigEndian, true), 0),
		"LE without BOM": append(encodeUTF16("Amount\n12", binary.LittleEndian, false), '3'),
		"BE without BOM": append(encodeUTF16("Amount\n12", binary.BigEndian, false), 0),
	} {
		t.Run(name, func(t *testing.T) {
			tbl, err := Decode(raw)
			if tbl.Rows != nil || tbl.Columns != nil {
				t.Errorf("table %+v returned alongside the refusal", tbl)
			}
			boundError(t, err, "UTF-16", "odd number of bytes")
		})
	}
}
