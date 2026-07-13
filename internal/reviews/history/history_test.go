package history

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"
)

// utf16LE encodes s as UTF-16LE with a leading BOM — the exact byte shape of
// Google's monthly reviews reports.
func utf16LE(s string) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0xFF, 0xFE}) // BOM
	for _, u := range utf16.Encode([]rune(s)) {
		var b [2]byte
		binary.LittleEndian.PutUint16(b[:], u)
		buf.Write(b[:])
	}
	return buf.Bytes()
}

const sampleCSV = "Package Name,App Version Code,App Version Name,Reviewer Language,Device," +
	"Review Submit Date and Time,Review Submit Millis Since Epoch," +
	"Review Last Update Date and Time,Review Last Update Millis Since Epoch," +
	"Star Rating,Review Title,Review Text," +
	"Developer Reply Date and Time,Developer Reply Millis Since Epoch,Developer Reply Text,Review Link\n" +
	"com.example.app,42,4.2.0,fr,flame,2026-06-15T10:00:00Z,1718445600000," +
	"2026-06-15T10:00:00Z,1718445600000,5,Génial,\"Très bonne app, 日本語 too 🎉\",,,,https://play.google.com/r/1\n"

func TestParse_utf16LE_nonASCII(t *testing.T) {
	rows, err := Parse(utf16LE(sampleCSV))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.PackageName != "com.example.app" {
		t.Errorf("packageName = %q", r.PackageName)
	}
	if r.StarRating != "5" {
		t.Errorf("starRating = %q", r.StarRating)
	}
	if r.ReviewerLanguage != "fr" {
		t.Errorf("reviewerLanguage = %q", r.ReviewerLanguage)
	}
	if r.ReviewTitle != "Génial" {
		t.Errorf("reviewTitle = %q (UTF-16 non-ASCII lost)", r.ReviewTitle)
	}
	// The non-ASCII, quoted, comma-bearing body must round-trip intact.
	if r.ReviewText != "Très bonne app, 日本語 too 🎉" {
		t.Errorf("reviewText = %q (transcoding/quoting lost data)", r.ReviewText)
	}
	if r.AppVersionCode != "42" || r.AppVersionName != "4.2.0" {
		t.Errorf("version cols = %q / %q", r.AppVersionCode, r.AppVersionName)
	}
	if r.ReviewLink != "https://play.google.com/r/1" {
		t.Errorf("reviewLink = %q", r.ReviewLink)
	}
}

func TestParse_headerDriven_reordered(t *testing.T) {
	// Columns in a different order than the struct: header-driven parsing must
	// still land each value in the right field.
	csv := "Star Rating,Package Name,Review Text\n3,com.x,hello\n"
	rows, err := Parse(utf16LE(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(rows) != 1 || rows[0].StarRating != "3" || rows[0].PackageName != "com.x" || rows[0].ReviewText != "hello" {
		t.Fatalf("reordered header mis-parsed: %+v", rows)
	}
}

func TestParse_emptyReport_headerOnly(t *testing.T) {
	rows, err := Parse(utf16LE("Package Name,Star Rating\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("header-only report should yield 0 rows, got %d", len(rows))
	}
}

func TestParse_emptyBytes(t *testing.T) {
	rows, err := Parse(nil)
	if err != nil || rows != nil {
		t.Fatalf("Parse(nil) = %v, %v; want nil, nil", rows, err)
	}
}

func TestLatestMonth(t *testing.T) {
	names := []string{
		"reviews/reviews_com.example.app_202604.csv",
		"reviews/reviews_com.example.app_202606.csv",
		"reviews/reviews_com.example.app_202605.csv",
		"reviews/reviews_com.other.app_202612.csv",  // different package, ignored
		"stats/installs_com.example.app_202606.csv", // different report, ignored
	}
	if got := LatestMonth("com.example.app", names); got != "202606" {
		t.Errorf("LatestMonth = %q, want 202606", got)
	}
	if got := LatestMonth("com.nonexistent", names); got != "" {
		t.Errorf("LatestMonth(no match) = %q, want \"\"", got)
	}
}

func TestNormalizeMonth(t *testing.T) {
	ok := map[string]string{"2026-06": "202606", "2020-12": "202012", "2019-01": "201901"}
	for in, want := range ok {
		got, err := NormalizeMonth(in)
		if err != nil || got != want {
			t.Errorf("NormalizeMonth(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"202606", "2026-6", "2026-13", "2026-00", "abc", "2026/06", ""} {
		if _, err := NormalizeMonth(bad); err == nil {
			t.Errorf("NormalizeMonth(%q) should have errored", bad)
		}
	}
}

func TestObjectName(t *testing.T) {
	if got := ObjectName("com.example.app", "202606"); got != "reviews/reviews_com.example.app_202606.csv" {
		t.Errorf("ObjectName = %q", got)
	}
}

func TestMonthRange(t *testing.T) {
	got, err := MonthRange("2026-01", "2026-03")
	if err != nil {
		t.Fatalf("MonthRange: %v", err)
	}
	want := []string{"202601", "202602", "202603"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("MonthRange = %v, want %v", got, want)
	}

	// Same month both ends → a single element.
	if got, _ := MonthRange("2026-06", "2026-06"); len(got) != 1 || got[0] != "202606" {
		t.Errorf("MonthRange(single) = %v, want [202606]", got)
	}

	// Year boundary crossing.
	got, err = MonthRange("2026-11", "2027-02")
	if err != nil {
		t.Fatalf("MonthRange(cross-year): %v", err)
	}
	if strings.Join(got, ",") != "202611,202612,202701,202702" {
		t.Errorf("MonthRange(cross-year) = %v", got)
	}

	// Inverted range and malformed bounds are errors.
	if _, err := MonthRange("2026-03", "2026-01"); err == nil {
		t.Error("inverted range should error")
	}
	for _, bad := range [][2]string{{"2026-13", "2026-14"}, {"bad", "2026-01"}, {"2026-01", "bad"}} {
		if _, err := MonthRange(bad[0], bad[1]); err == nil {
			t.Errorf("MonthRange(%q,%q) should have errored", bad[0], bad[1])
		}
	}
}

func TestMerge_dedupLastUpdateWins_sortedBySubmit(t *testing.T) {
	// Review /1 appears in two months: the later export carries a greater Last
	// Update and must win. Rows arrive out of submit order to prove the sort.
	rows := []Row{
		{ReviewLink: "https://play.google.com/r/2", ReviewSubmitMillisSinceEpoch: "2000", ReviewText: "second"},
		{ReviewLink: "https://play.google.com/r/1", ReviewSubmitMillisSinceEpoch: "1000", ReviewLastUpdateMillisSinceEpoch: "1500", ReviewText: "old"},
		{ReviewLink: "https://play.google.com/r/1", ReviewSubmitMillisSinceEpoch: "1000", ReviewLastUpdateMillisSinceEpoch: "1800", ReviewText: "edited"},
	}
	got := Merge(rows)
	if len(got) != 2 {
		t.Fatalf("want 2 rows after dedup, got %d: %+v", len(got), got)
	}
	// Sorted by submit millis: /1 (1000) then /2 (2000).
	if got[0].ReviewLink != "https://play.google.com/r/1" || got[1].ReviewLink != "https://play.google.com/r/2" {
		t.Errorf("not sorted by submit time: %+v", got)
	}
	// Last update wins.
	if got[0].ReviewText != "edited" {
		t.Errorf("latest Review Last Update should win, got %q", got[0].ReviewText)
	}
}

func TestMerge_noLink_keptDistinct(t *testing.T) {
	// Rows without a ReviewLink cannot be identified across months → all kept.
	rows := []Row{
		{ReviewSubmitMillisSinceEpoch: "2", ReviewText: "a"},
		{ReviewSubmitMillisSinceEpoch: "1", ReviewText: "b"},
	}
	got := Merge(rows)
	if len(got) != 2 {
		t.Fatalf("linkless rows must all survive, got %d", len(got))
	}
	if got[0].ReviewText != "b" || got[1].ReviewText != "a" {
		t.Errorf("linkless rows not sorted by submit: %+v", got)
	}
}
