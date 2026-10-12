package reports

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"errors"
	"strings"
	"testing"
)

// member is one entry of an in-memory test archive. declared, when non-zero,
// overrides the uncompressed size the central directory claims, so a test can
// build an archive that lies about its size.
type member struct {
	name     string
	body     string
	declared uint64
}

// buildZip writes the members as deflated entries. A lying declared size is
// written raw (zip.Writer would otherwise record the true size).
func buildZip(t *testing.T, members ...member) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, m := range members {
		if m.declared == 0 {
			fw, err := w.Create(m.name)
			if err != nil {
				t.Fatalf("create %s: %v", m.name, err)
			}
			if _, err := fw.Write([]byte(m.body)); err != nil {
				t.Fatalf("write %s: %v", m.name, err)
			}
			continue
		}
		var comp bytes.Buffer
		fl, _ := flate.NewWriter(&comp, flate.BestCompression)
		_, _ = fl.Write([]byte(m.body))
		_ = fl.Close()
		h := &zip.FileHeader{Name: m.name, Method: zip.Deflate, CompressedSize64: uint64(comp.Len()), UncompressedSize64: m.declared}
		fw, err := w.CreateRaw(h)
		if err != nil {
			t.Fatalf("create raw %s: %v", m.name, err)
		}
		if _, err := fw.Write(comp.Bytes()); err != nil {
			t.Fatalf("write raw %s: %v", m.name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// boundError asserts err is the named-bound refusal, exit 20, and that its
// message carries every fragment.
func boundError(t *testing.T, err error, fragments ...string) {
	t.Helper()
	var be *RefusedError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v (%T), want a *RefusedError", err, err)
	}
	if be.ExitCode() != 20 {
		t.Errorf("exit = %d, want 20 (client-side validation)", be.ExitCode())
	}
	for _, f := range fragments {
		if !strings.Contains(err.Error(), f) {
			t.Errorf("message %q lacks %q", err, f)
		}
	}
}

// The bounds are ADR-0050's numbers, stated here independently of the code.
func TestBounds_matchADR0050(t *testing.T) {
	if MaxZipObjectBytes != 128<<20 || MaxZipMembers != 16 || MaxReportBytes != 512<<20 || MaxReportCells != 24<<20 {
		t.Fatalf("bounds = %d B object, %d members, %d B decoded, %d cells; ADR-0050 says 128 MiB, 16, 512 MiB, 24 Mi cells",
			MaxZipObjectBytes, MaxZipMembers, MaxReportBytes, MaxReportCells)
	}
}

func readZipExport(t *testing.T, rd *Reader, raw []byte) (Table, error) {
	t.Helper()
	return rd.Read("sales/salesreport_202609.zip", raw)
}

func TestReader_zipReadsOnlyCSVMembersInArchiveOrder(t *testing.T) {
	raw := buildZip(t,
		member{name: "README.txt", body: "not a report"},
		member{name: "salesreport_202609.csv", body: "Order Number,Country of Buyer\nGPA.1,FR\n"},
		member{name: "nested/", body: ""},
		member{name: "extra.CSV", body: "Order Number,Note\nGPA.2,x\n"},
	)
	got, err := readZipExport(t, NewReader(""), raw)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !equal(keys(got.Columns), []string{"orderNumber", "countryOfBuyer", "note"}) || len(got.Rows) != 2 ||
		got.Rows[0].Value("countryOfBuyer") != "FR" || got.Rows[1].Value("note") != "x" {
		t.Fatalf("table = %+v, want the two .csv members merged in archive order", got)
	}
}

func TestReader_zipObjectOverTheCompressedCap(t *testing.T) {
	raw := buildZip(t, member{name: "a.csv", body: strings.Repeat("x", 200)})
	rd := NewReader("")
	rd.zip.object = int64(len(raw)) - 1
	_, err := readZipExport(t, rd, raw)
	boundError(t, err, "compressed", "bytes")
}

func TestReader_zipObjectOverTheRealCompressedCap(t *testing.T) {
	_, err := readZipExport(t, NewReader(""), make([]byte, MaxZipObjectBytes+1))
	boundError(t, err, "134217728")
}

func TestReader_zipTooManyMembers(t *testing.T) {
	ms := make([]member, MaxZipMembers+1)
	for i := range ms {
		ms[i] = member{name: "m" + strings.Repeat("x", i) + ".csv", body: "a\n1\n"}
	}
	_, err := readZipExport(t, NewReader(""), buildZip(t, ms...))
	boundError(t, err, "17 members", "16")
}

func TestReader_zipNoCSVMember(t *testing.T) {
	_, err := readZipExport(t, NewReader(""), buildZip(t, member{name: "report.xlsx", body: "PK"}))
	boundError(t, err, "no .csv member")
}

func TestReader_notAZip(t *testing.T) {
	_, err := readZipExport(t, NewReader(""), []byte("Order Number\nGPA.1\n"))
	boundError(t, err, "not a ZIP archive")
}

func TestReader_zipDeclaredSizeOverTheRealBudget(t *testing.T) {
	// The real byte budget, met by a header claiming one byte more: refused
	// on the declaration, before a byte is inflated.
	_, err := readZipExport(t, NewReader(""), buildZip(t, member{name: "a.csv", body: "a\n1\n", declared: MaxReportBytes + 1}))
	boundError(t, err, "a.csv", "declares 536870913", "536870912-byte budget")
}
