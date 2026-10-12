package reports

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"strings"
)

// Container is how a family's exports are packaged in the bucket.
type Container int

const (
	// CSV exports are the report file itself (the statistics).
	CSV Container = iota
	// ZIP exports hold the report as a CSV member (the finance families).
	ZIP
)

// ZIP bounds (ADR-0050 §10), on the pattern of the artifact preflight
// (ADR-0046): the archive comes from a bucket gplay does not control, so a
// hostile or corrupt object must fail with a named bound, never exhaust
// memory nor yield a truncated report. Sized for a high-volume account's
// monthly earnings.
const (
	// MaxZipObjectBytes caps the compressed object, checked on the listed size
	// before the fetch and on the bytes fetched.
	MaxZipObjectBytes = 128 << 20
	// MaxZipMembers caps the entries of one archive. It is counted once
	// archive/zip has parsed the central directory, which the object cap
	// already bounds: a 72 MiB archive of a million empty entries costs about
	// 200 MiB of heap (measured), under the decompression budget below.
	MaxZipMembers = 16
	// MaxZipMemberBytes caps the decompressed bytes of one member.
	MaxZipMemberBytes = 1 << 30
	// MaxZipTotalBytes caps the decompressed bytes of the whole archive.
	MaxZipTotalBytes = 1 << 30
)

// zipLimits carries the bounds, so the tests can drive each one with a small
// archive; production always uses defaultZipLimits.
type zipLimits struct {
	object  int64
	members int
	member  uint64
	total   uint64
}

var defaultZipLimits = zipLimits{object: MaxZipObjectBytes, members: MaxZipMembers, member: MaxZipMemberBytes, total: MaxZipTotalBytes}

// RefusedError is an export gplay declines to read: over a size bound (the
// read cap, or one of the ZIP bounds), or not the archive it should be. It
// exits 20 (client-side validation, docs/DESIGN.md §9): gplay refused the
// bytes, and no truncated report is returned.
type RefusedError struct {
	Object string // the object key, or the member name, the refusal is about
	Reason string
}

func (e *RefusedError) Error() string {
	if e.Object == "" {
		return "export refused: " + e.Reason
	}
	return "export refused: " + e.Object + ": " + e.Reason
}

// ExitCode implements exit.Coder.
func (e *RefusedError) ExitCode() int { return 20 }

// Member is one CSV member of a report archive, decompressed.
type Member struct {
	Name string
	Data []byte
}

// ExtractCSV returns the .csv members of a report ZIP, in archive order,
// under ADR-0050's bounds: the compressed size, the member count, then for
// each .csv member its declared size against the per-member and remaining
// total budgets (before any byte is inflated) and the bytes actually inflated
// through a limited reader. Other members (a readme, a directory) are skipped
// but count towards the member cap. An archive with no .csv member, or that
// is not a ZIP, is refused like a crossed bound.
func ExtractCSV(raw []byte) ([]Member, error) { return extractCSV(raw, defaultZipLimits) }

func extractCSV(raw []byte, lim zipLimits) ([]Member, error) {
	if int64(len(raw)) > lim.object {
		return nil, &RefusedError{Reason: fmt.Sprintf("%d compressed bytes, over the %d-byte cap", len(raw), lim.object)}
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, &RefusedError{Reason: "not a ZIP archive: " + err.Error()}
	}
	if len(zr.File) > lim.members {
		return nil, &RefusedError{Reason: fmt.Sprintf("%d members, over the %d-member cap", len(zr.File), lim.members)}
	}
	remaining := lim.total
	var out []Member
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !strings.EqualFold(path.Ext(f.Name), ".csv") {
			continue
		}
		data, err := readMember(f, lim.member, &remaining, lim.total)
		if err != nil {
			return nil, err
		}
		out = append(out, Member{Name: f.Name, Data: data})
	}
	if len(out) == 0 {
		return nil, &RefusedError{Reason: "no .csv member in the archive"}
	}
	return out, nil
}

// readMember inflates one member under the per-member cap and what is left of
// the total budget. The declared size is checked first, so the common bomb (a
// small deflate stream claiming gigabytes) never runs; the limited reader
// then holds the bound on the bytes actually produced, whatever the header
// says. (archive/zip itself stops at the declared size; the limited reader
// does not rely on it.)
func readMember(f *zip.File, perMember uint64, remaining *uint64, total uint64) ([]byte, error) {
	if f.UncompressedSize64 > perMember {
		return nil, &RefusedError{Object: f.Name, Reason: fmt.Sprintf("member declares %d decompressed bytes, over the %d-byte per-member cap", f.UncompressedSize64, perMember)}
	}
	if f.UncompressedSize64 > *remaining {
		return nil, &RefusedError{Object: f.Name, Reason: fmt.Sprintf("member declares %d decompressed bytes, over what is left of the %d-byte total budget", f.UncompressedSize64, total)}
	}
	budget, which := perMember, "per-member"
	if *remaining < budget {
		budget, which = *remaining, "total"
	}
	rc, err := f.Open()
	if err != nil {
		return nil, &RefusedError{Object: f.Name, Reason: "cannot open member: " + err.Error()}
	}
	defer func() { _ = rc.Close() }()
	data, err := readLimited(rc, f.Name, budget, which)
	if err != nil {
		return nil, err
	}
	*remaining -= uint64(len(data))
	return data, nil
}

// readLimited reads r to its end, refusing past budget bytes: it reads one
// byte more than the budget so an overflow is detected, never truncated.
func readLimited(r io.Reader, name string, budget uint64, which string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, int64(budget)+1))
	if err != nil {
		return nil, &RefusedError{Object: name, Reason: "cannot inflate member: " + err.Error()}
	}
	if uint64(len(data)) > budget {
		return nil, &RefusedError{Object: name, Reason: fmt.Sprintf("member expands past the %d-byte %s cap", budget, which)}
	}
	return data, nil
}

// Decode turns the bytes of one export of the family into a Table: a CSV
// export is decoded as is; a ZIP export's .csv members are extracted under
// ADR-0050's bounds, each decoded with its own encoding detected (by BOM),
// and merged in archive order.
func (f Family) Decode(raw []byte) (Table, error) {
	if f.Container != ZIP {
		return Decode(raw)
	}
	members, err := ExtractCSV(raw)
	if err != nil {
		return Table{}, err
	}
	tables := make([]Table, 0, len(members))
	for _, m := range members {
		t, err := Decode(m.Data)
		if err != nil {
			return Table{}, fmt.Errorf("%s: %w", m.Name, err)
		}
		tables = append(tables, t)
	}
	return Merge(tables...), nil
}
