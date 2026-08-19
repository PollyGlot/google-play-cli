package datasafety_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/compliance/datasafety"
)

// referenceLine rebuilds a CSV header line that exactly matches the bundled
// reference template, so the "no warning when the header matches" case is
// robust to whatever the reference content happens to be.
func referenceLine(t *testing.T) string {
	t.Helper()
	h := datasafety.ReferenceHeader()
	if len(h) == 0 {
		t.Fatal("ReferenceHeader() is empty; the bundled template must have a header")
	}
	return strings.Join(h, ",")
}

// TestValidate_wellFormed asserts a CSV whose header matches the reference
// and whose rows are rectangular passes with zero problems, zero warnings,
// and an accurate row/column tally. The canonical Payload echoes the input.
func TestValidate_wellFormed(t *testing.T) {
	hdr := referenceLine(t)
	n := len(datasafety.ReferenceHeader())
	row := strings.Repeat("x,", n-1) + "x"
	raw := []byte(hdr + "\n" + row + "\n" + row + "\n")

	rep, probs := datasafety.Validate(raw)
	if len(probs) != 0 {
		t.Fatalf("well-formed CSV reported problems: %v", probs)
	}
	if rep == nil {
		t.Fatal("Validate returned nil Report on a valid CSV")
	}
	if rep.Rows != 2 {
		t.Errorf("Rows = %d, want 2 (data rows, header excluded)", rep.Rows)
	}
	if rep.Columns != n {
		t.Errorf("Columns = %d, want %d", rep.Columns, n)
	}
	if len(rep.Warnings) != 0 {
		t.Errorf("matching header should not warn, got: %v", rep.Warnings)
	}
	if !bytes.Equal(rep.Payload, raw) {
		t.Errorf("Payload = %q, want it to echo the input verbatim", rep.Payload)
	}
}

// TestValidate_emptyFile asserts an empty document is a structural problem
// (the declaration must be non-empty).
func TestValidate_emptyFile(t *testing.T) {
	rep, probs := datasafety.Validate([]byte{})
	if len(probs) == 0 {
		t.Fatal("empty file must be a structural problem")
	}
	if rep != nil {
		t.Errorf("Report should be nil when the file is empty, got %+v", rep)
	}
	if !strings.Contains(strings.ToLower(probs[0].Message), "empty") {
		t.Errorf("problem = %q, want it to mention 'empty'", probs[0].Message)
	}
}

// TestValidate_raggedRows asserts a non-rectangular CSV (a row with a
// different column count than the header) is a structural problem.
func TestValidate_raggedRows(t *testing.T) {
	hdr := referenceLine(t)
	raw := []byte(hdr + "\nonly,two\n")

	_, probs := datasafety.Validate(raw)
	if len(probs) == 0 {
		t.Fatal("ragged rows must be a structural problem")
	}
}

// TestValidate_brokenQuoting asserts an unbalanced quote is a structural
// problem (the CSV does not parse).
func TestValidate_brokenQuoting(t *testing.T) {
	hdr := referenceLine(t)
	raw := []byte(hdr + "\n\"unterminated,quote\n")

	_, probs := datasafety.Validate(raw)
	if len(probs) == 0 {
		t.Fatal("broken quoting must be a structural problem")
	}
}

// TestValidate_headerMismatch asserts a header that diverges from the
// bundled reference template is a NON-FATAL warning, not a problem: the
// command still succeeds (exit 0), but the operator is hinted.
func TestValidate_headerMismatch(t *testing.T) {
	raw := []byte("totally,different,header\na,b,c\n")

	rep, probs := datasafety.Validate(raw)
	if len(probs) != 0 {
		t.Fatalf("a divergent header must NOT be a structural problem, got: %v", probs)
	}
	if rep == nil {
		t.Fatal("Validate returned nil Report despite no structural problem")
	}
	if len(rep.Warnings) == 0 {
		t.Fatal("a divergent header must emit a non-fatal warning")
	}
}

// TestValidate_stripsLeadingBOM asserts a leading UTF-8 BOM is repaired
// (stripped) rather than rejected, and the canonical Payload that set would
// POST carries no BOM.
func TestValidate_stripsLeadingBOM(t *testing.T) {
	hdr := referenceLine(t)
	n := len(datasafety.ReferenceHeader())
	row := strings.Repeat("x,", n-1) + "x"
	utf8BOM := []byte{0xEF, 0xBB, 0xBF}
	raw := append(append([]byte{}, utf8BOM...), []byte(hdr+"\n"+row+"\n")...)

	rep, probs := datasafety.Validate(raw)
	if len(probs) != 0 {
		t.Fatalf("a leading BOM should be repaired, not rejected; got problems: %v", probs)
	}
	if rep == nil {
		t.Fatal("nil Report after BOM repair")
	}
	if bytes.HasPrefix(rep.Payload, utf8BOM) {
		t.Error("canonical Payload still carries the BOM; it must be stripped")
	}
	if len(rep.Warnings) != 0 {
		t.Errorf("BOM-prefixed but matching header should not warn, got: %v", rep.Warnings)
	}
}

// TestValidate_invalidUTF8 asserts non-UTF-8 bytes are a structural problem
// (the declaration is expected to be UTF-8).
func TestValidate_invalidUTF8(t *testing.T) {
	raw := []byte{0xff, 0xfe, 0x00, 0x01}

	_, probs := datasafety.Validate(raw)
	if len(probs) == 0 {
		t.Fatal("invalid UTF-8 must be a structural problem")
	}
}

// TestValidate_headerOnly asserts a header with zero data rows is
// structurally valid (Rows == 0). validate is structural only: whether an
// empty declaration is acceptable is Google's call at POST time.
func TestValidate_headerOnly(t *testing.T) {
	raw := []byte(referenceLine(t) + "\n")

	rep, probs := datasafety.Validate(raw)
	if len(probs) != 0 {
		t.Fatalf("a header-only CSV is structurally valid, got problems: %v", probs)
	}
	if rep.Rows != 0 {
		t.Errorf("Rows = %d, want 0", rep.Rows)
	}
}
