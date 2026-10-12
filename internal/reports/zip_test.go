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
func TestZipBounds_matchADR0050(t *testing.T) {
	if MaxZipObjectBytes != 128<<20 || MaxZipMembers != 16 || MaxZipMemberBytes != 1<<30 || MaxZipTotalBytes != 1<<30 {
		t.Fatalf("bounds = %d B object, %d members, %d B per member, %d B total; ADR-0050 says 128 MiB, 16, 1 GiB, 1 GiB",
			MaxZipObjectBytes, MaxZipMembers, MaxZipMemberBytes, MaxZipTotalBytes)
	}
}

func TestExtractCSV_readsOnlyCSVMembersInArchiveOrder(t *testing.T) {
	raw := buildZip(t,
		member{name: "README.txt", body: "not a report"},
		member{name: "salesreport_202609.csv", body: "Order Number,Country of Buyer\nGPA.1,FR\n"},
		member{name: "nested/", body: ""},
		member{name: "extra.CSV", body: "a\n1\n"},
	)
	got, err := ExtractCSV(raw)
	if err != nil {
		t.Fatalf("ExtractCSV: %v", err)
	}
	if len(got) != 2 || got[0].Name != "salesreport_202609.csv" || got[1].Name != "extra.CSV" {
		t.Fatalf("members = %+v, want the two .csv members in archive order", got)
	}
	if string(got[0].Data) != "Order Number,Country of Buyer\nGPA.1,FR\n" {
		t.Errorf("member data = %q", got[0].Data)
	}
}

func TestExtractCSV_objectOverTheCompressedCap(t *testing.T) {
	raw := buildZip(t, member{name: "a.csv", body: strings.Repeat("x", 200)})
	_, err := extractCSV(raw, zipLimits{object: int64(len(raw)) - 1, members: 16, member: 1 << 20, total: 1 << 20})
	boundError(t, err, "compressed", "bytes")
}

func TestExtractCSV_objectOverTheRealCompressedCap(t *testing.T) {
	raw := make([]byte, MaxZipObjectBytes+1)
	_, err := ExtractCSV(raw)
	boundError(t, err, "134217728")
}

func TestExtractCSV_tooManyMembers(t *testing.T) {
	ms := make([]member, MaxZipMembers+1)
	for i := range ms {
		ms[i] = member{name: "m" + strings.Repeat("x", i) + ".csv", body: "a\n1\n"}
	}
	_, err := ExtractCSV(buildZip(t, ms...))
	boundError(t, err, "17 members", "16")
}

func TestExtractCSV_noCSVMember(t *testing.T) {
	_, err := ExtractCSV(buildZip(t, member{name: "report.xlsx", body: "PK"}))
	boundError(t, err, "no .csv member")
}

func TestExtractCSV_notAZip(t *testing.T) {
	_, err := ExtractCSV([]byte("Order Number\nGPA.1\n"))
	boundError(t, err, "not a ZIP archive")
}

func TestExtractCSV_declaredSizeOverTheMemberBudget(t *testing.T) {
	// The real 1 GiB bound, met by a header claiming 1 GiB + 1: refused on the
	// declaration, before a byte is inflated.
	_, err := ExtractCSV(buildZip(t, member{name: "a.csv", body: "a\n1\n", declared: MaxZipMemberBytes + 1}))
	boundError(t, err, "a.csv", "declares 1073741825", "1073741824")
}

func TestExtractCSV_declaredSizeOverTheRemainingTotal(t *testing.T) {
	// Each member is honestly declared and under the per-member bound; the
	// second one would cross the total.
	raw := buildZip(t,
		member{name: "a.csv", body: strings.Repeat("1", 600)},
		member{name: "b.csv", body: strings.Repeat("2", 600)},
	)
	_, err := extractCSV(raw, zipLimits{object: 1 << 20, members: 16, member: 1000, total: 1000})
	boundError(t, err, "b.csv", "total", "1000")
}

func TestExtractCSV_lyingMemberNeverInflatesPastItsBudget(t *testing.T) {
	// The header claims 10 bytes; the stream inflates to 5000. archive/zip
	// stops at the declared size, and the limited reader would stop at the
	// budget if it did not: either way the member fails, naming itself, and
	// no truncated data comes back.
	raw := buildZip(t, member{name: "a.csv", body: strings.Repeat("0", 5000), declared: 10})
	got, err := extractCSV(raw, zipLimits{object: 1 << 20, members: 16, member: 1000, total: 1 << 20})
	if got != nil {
		t.Errorf("members = %+v alongside the error", got)
	}
	boundError(t, err, "a.csv")
}

func TestReadLimited_budgetIsEnforcedOnTheBytesRead(t *testing.T) {
	// The limited reader itself, on a stream that does not stop where it
	// claims: the guard ADR-0050 asks for whatever reader sits underneath.
	_, err := readLimited(strings.NewReader(strings.Repeat("0", 5000)), "a.csv", 1000, "per-member")
	boundError(t, err, "a.csv", "expands past", "1000", "per-member")
	b, err := readLimited(strings.NewReader("12345"), "a.csv", 5, "per-member")
	if err != nil || string(b) != "12345" {
		t.Errorf("at the budget: %q, %v", b, err)
	}
}
