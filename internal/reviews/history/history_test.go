package history

import (
	"bytes"
	"encoding/binary"
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
