package reports

import (
	"archive/zip"
	"bytes"
	"errors"
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
// memory nor yield a truncated report. What a member inflates to is bounded
// by the budget of the whole command (Limits), not per archive.
const (
	// MaxZipObjectBytes caps the compressed object, checked on the listed size
	// before the fetch and on the bytes fetched.
	MaxZipObjectBytes = 128 << 20
	// MaxZipMembers caps the entries of one archive. It is counted once
	// archive/zip has parsed the central directory, which the object cap
	// already bounds: a 72 MiB archive of a million empty entries costs about
	// 200 MiB of heap (measured).
	MaxZipMembers = 16
)

// zipLimits carries the archive bounds, so the tests can drive each one with
// a small archive; production always uses defaultZipLimits.
type zipLimits struct {
	object  int64
	members int
}

var defaultZipLimits = zipLimits{object: MaxZipObjectBytes, members: MaxZipMembers}

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

// readZip streams the .csv members of a report ZIP to each, in archive
// order, under ADR-0050's bounds: the compressed size and the member count,
// then for each .csv member its declared size against what is left of the
// command's byte budget (before a byte is inflated), and the bytes actually
// inflated, charged to that budget as they are read (a declared size can
// lie). Other members (a readme, a directory) are skipped but count towards
// the member cap. An archive with no .csv member, or that is not a ZIP, is
// refused like a crossed bound.
func readZip(raw []byte, lim zipLimits, b *budget, each func(name string, r io.Reader) error) error {
	if int64(len(raw)) > lim.object {
		return &RefusedError{Reason: fmt.Sprintf("%d compressed bytes, over the %d-byte cap", len(raw), lim.object)}
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return &RefusedError{Reason: "not a ZIP archive: " + err.Error()}
	}
	if len(zr.File) > lim.members {
		return &RefusedError{Reason: fmt.Sprintf("%d members, over the %d-member cap", len(zr.File), lim.members)}
	}
	found := false
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !strings.EqualFold(path.Ext(f.Name), ".csv") {
			continue
		}
		found = true
		if left := b.bytesLeft(); f.UncompressedSize64 > uint64(left) {
			return &RefusedError{Object: f.Name, Reason: fmt.Sprintf("member declares %d decompressed bytes, over the %d left of the %s: %s",
				f.UncompressedSize64, left, b.bytesBound(), narrow)}
		}
		if err := readMember(f, each); err != nil {
			return err
		}
	}
	if !found {
		return &RefusedError{Reason: "no .csv member in the archive"}
	}
	return nil
}

// readMember opens one member and hands its inflating stream to each. A
// failure to inflate (a corrupt stream, a member longer than its header
// says, which archive/zip stops at) is a refusal naming the member.
func readMember(f *zip.File, each func(name string, r io.Reader) error) error {
	rc, err := f.Open()
	if err != nil {
		return &RefusedError{Object: f.Name, Reason: "cannot open member: " + err.Error()}
	}
	defer func() { _ = rc.Close() }()
	return withObject(each(f.Name, &inflateReader{r: rc}), f.Name)
}

// inflateReader labels a read failure of a member as a refusal.
type inflateReader struct{ r io.Reader }

func (ir *inflateReader) Read(p []byte) (int, error) {
	n, err := ir.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, &RefusedError{Reason: "cannot inflate member: " + err.Error()}
	}
	return n, err
}

// withObject names object in a refusal that names nothing yet.
func withObject(err error, object string) error {
	var refused *RefusedError
	if errors.As(err, &refused) && refused.Object == "" {
		refused.Object = object
	}
	return err
}
