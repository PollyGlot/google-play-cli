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

type Container int

const (
	CSV Container = iota
	ZIP
)

const (
	MaxZipObjectBytes = 128 << 20
	MaxZipMembers     = 16
)

type zipLimits struct {
	object  int64
	members int
}

var defaultZipLimits = zipLimits{object: MaxZipObjectBytes, members: MaxZipMembers}

type RefusedError struct {
	Object string
	Reason string
}

func (e *RefusedError) Error() string {
	if e.Object == "" {
		return "export refused: " + e.Reason
	}
	return "export refused: " + e.Object + ": " + e.Reason
}

func (e *RefusedError) ExitCode() int { return 20 }

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

func readMember(f *zip.File, each func(name string, r io.Reader) error) error {
	rc, err := f.Open()
	if err != nil {
		return &RefusedError{Object: f.Name, Reason: "cannot open member: " + err.Error()}
	}
	defer func() { _ = rc.Close() }()
	return withObject(each(f.Name, &inflateReader{r: rc}), f.Name)
}

type inflateReader struct{ r io.Reader }

func (ir *inflateReader) Read(p []byte) (int, error) {
	n, err := ir.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, &RefusedError{Reason: "cannot inflate member: " + err.Error()}
	}
	return n, err
}

func withObject(err error, object string) error {
	var refused *RefusedError
	if errors.As(err, &refused) && refused.Object == "" {
		refused.Object = object
	}
	return err
}
