package artifact

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
)

type Kind string

const (
	KindBundle        Kind = "bundle"
	KindAPK           Kind = "apk"
	KindUnknown       Kind = "unknown"
	KindIndeterminate Kind = "indeterminate"
)

func (k Kind) Describe() string {
	switch k {
	case KindBundle:
		return "an Android App Bundle (AAB)"
	case KindAPK:
		return "an APK"
	case KindIndeterminate:
		return "a container gplay could not classify"
	default:
		return "neither an AAB nor an APK"
	}
}

type Info struct {
	Kind    Kind
	Package string
	Note    string
}

type Expect struct {
	Kinds   []Kind
	Package string
}

func describeKinds(kinds []Kind) string {
	parts := make([]string, 0, len(kinds))
	for _, k := range kinds {
		parts = append(parts, k.Describe())
	}
	switch len(parts) {
	case 0:
		return "anything"
	case 1:
		return parts[0]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " or " + parts[len(parts)-1]
	}
}

type Error struct {
	Path   string
	Reason string
}

func (e *Error) Error() string {
	return fmt.Sprintf("artifact preflight: %s: %s (pass --skip-preflight to upload anyway)", e.Reason, e.Path)
}

func (e *Error) ExitCode() int { return 20 }

type IOError struct {
	Path string
	Msg  string
	Err  error
}

func (e *IOError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Path, e.Msg, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Msg)
}

func (e *IOError) Unwrap() error { return e.Err }

func (e *IOError) ExitCode() int { return 20 }

func Preflight(path string, want Expect) (Info, error) {
	info, err := Inspect(path)
	if err != nil {
		return info, err
	}
	if info.Kind == KindIndeterminate {
		return info, nil
	}
	if len(want.Kinds) > 0 && !slices.Contains(want.Kinds, info.Kind) {
		return info, &Error{
			Path:   path,
			Reason: fmt.Sprintf("expected %s, found %s", describeKinds(want.Kinds), info.Kind.Describe()),
		}
	}
	if want.Package != "" && info.Package != "" && want.Package != info.Package {
		return info, &Error{
			Path: path,
			Reason: fmt.Sprintf("package mismatch: this upload targets %q but the artifact declares %q",
				want.Package, info.Package),
		}
	}
	return info, nil
}

type Options struct {
	Skip   bool
	Report bool
}

func Verify(stderr io.Writer, path string, want Expect, opts Options) error {
	if opts.Skip {
		return statArtifact(path)
	}
	info, err := Preflight(path, want)
	if info.Note != "" {
		note(stderr, "NOTE: artifact preflight: "+info.Note)
	}
	if err != nil {
		return err
	}
	if opts.Report {
		if info.Package != "" {
			note(stderr, fmt.Sprintf("NOTE: artifact preflight: %s declares package %q (dry-run)", info.Kind.Describe(), info.Package))
		} else {
			note(stderr, fmt.Sprintf("NOTE: artifact preflight: the artifact is %s (dry-run)", info.Kind.Describe()))
		}
	}
	return nil
}

func note(w io.Writer, line string) {
	if w == nil {
		return
	}
	_, _ = io.WriteString(w, line+"\n")
}

func Inspect(path string) (Info, error) {
	if err := statArtifact(path); err != nil {
		return Info{}, err
	}

	zr, err := zip.OpenReader(path)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return Info{}, &IOError{Path: path, Msg: "cannot read artifact", Err: err}
		}
		return Info{Kind: KindUnknown}, nil
	}
	defer func() { _ = zr.Close() }()

	return inspectZip(&zr.Reader), nil
}

func statArtifact(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return &IOError{Path: path, Msg: "cannot read artifact", Err: err}
	}
	if !st.Mode().IsRegular() {
		return &IOError{Path: path, Msg: "not a regular file"}
	}
	return nil
}

func inspectZip(zr *zip.Reader) Info {
	if len(zr.File) > MaxZipEntries {
		return Info{
			Kind: KindIndeterminate,
			Note: fmt.Sprintf("artifact has %d zip members, over the %d-member preflight cap: container not classified, upload proceeding unchecked", len(zr.File), MaxZipEntries),
		}
	}

	byName := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		if _, seen := byName[f.Name]; !seen {
			byName[f.Name] = f
		}
	}

	budget := int64(maxTotalDecompressedBytes)

	if m, ok := byName[aabManifestPath]; ok || byName[aabBundleConfigPath] != nil {
		info := Info{Kind: KindBundle}
		if !ok {
			info.Note = "artifact looks like an AAB (BundleConfig.pb) but carries no " + aabManifestPath + ": package name not verified"
			return info
		}
		raw, err := readEntry(m, &budget)
		if err != nil {
			info.Note = "could not read " + aabManifestPath + ": " + err.Error() + "; package name not verified"
			return info
		}
		pkg, err := parseProtoManifestPackage(raw)
		if err != nil {
			info.Note = "could not parse the AAB protobuf manifest: " + err.Error() + "; package name not verified"
			return info
		}
		info.Package = pkg
		return info
	}

	if m, ok := byName[apkManifestPath]; ok {
		info := Info{Kind: KindAPK}
		raw, err := readEntry(m, &budget)
		if err != nil {
			info.Note = "could not read " + apkManifestPath + ": " + err.Error() + "; package name not verified"
			return info
		}
		pkg, err := parseBinaryXMLPackage(raw)
		if err != nil {
			info.Note = "could not parse the APK binary XML manifest: " + err.Error() + "; package name not verified"
			return info
		}
		info.Package = pkg
		return info
	}

	return Info{Kind: KindUnknown}
}

const (
	aabManifestPath     = "base/manifest/AndroidManifest.xml"
	aabBundleConfigPath = "BundleConfig.pb"
	apkManifestPath     = "AndroidManifest.xml"
)

const (
	MaxZipEntries             = 1 << 16
	maxManifestBytes          = 4 << 20
	maxTotalDecompressedBytes = 8 << 20
)

func readEntry(f *zip.File, budget *int64) ([]byte, error) {
	if f.UncompressedSize64 > maxManifestBytes {
		return nil, fmt.Errorf("zip member declares %d decompressed bytes, over the %d-byte cap", f.UncompressedSize64, uint64(maxManifestBytes))
	}
	cap64 := int64(maxManifestBytes)
	if *budget < cap64 {
		cap64 = *budget
	}
	if cap64 <= 0 {
		return nil, fmt.Errorf("preflight decompression budget of %d bytes is exhausted", maxTotalDecompressedBytes)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()

	b, err := io.ReadAll(io.LimitReader(rc, cap64+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > cap64 {
		return nil, fmt.Errorf("zip member expands past the %d-byte cap", cap64)
	}
	*budget -= int64(len(b))
	return b, nil
}
