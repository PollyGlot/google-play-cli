// Package artifact implements the local preflight gplay runs before any
// upload byte leaves the machine (PRD #448): it decides what an artifact
// container really is (an AAB, an APK, or neither) from its zip structure
// rather than from its extension, reads the package name the artifact
// declares, and lets the caller refuse an upload whose artifact does not
// match what the command promised.
//
// Everything here is offline and dependency-free: the two manifest readers
// (the aapt2 protobuf manifest of an AAB, the Android binary XML manifest of
// an APK) are hand-rolled, in the spirit of ADR-0007. The point is that a
// wrong artifact path fails in milliseconds instead of after minutes of
// resumable transfer, and that app A's release can never receive app B's
// build.
//
// The two seams a caller uses are Inspect (what is this file?) and Preflight
// (does this file match what I expect?). Both are pure local I/O: they open
// no network connection and mutate nothing.
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

// Kind is the container class of a local artifact. The values match the
// `--format` vocabulary the upload commands already speak (apk / bundle) so
// a preflight refusal can name the flag that would fix it.
type Kind string

const (
	// KindBundle is an Android App Bundle (.aab): a zip carrying
	// BundleConfig.pb and a protobuf manifest under the base module.
	KindBundle Kind = "bundle"
	// KindAPK is an Android package (.apk): a zip carrying a binary-XML
	// AndroidManifest.xml at its root.
	KindAPK Kind = "apk"
	// KindUnknown is anything else: a non-zip file, or a zip that carries
	// neither marker (an .obb expansion file, a mislabeled archive, a
	// screenshot). It is a legitimate expectation on the surfaces that
	// upload something which is not an Android package.
	KindUnknown Kind = "unknown"
	// KindIndeterminate is "gplay declined to classify this container", not a
	// finding about the file. It is what the member cap yields on an archive
	// too large to walk, and it must never be confused with KindUnknown: an
	// asset-rich AAB is over the cap and is still an AAB, so calling it
	// "neither an AAB nor an APK" would refuse a legitimate release. Preflight
	// skips the container check on it and degrades with a note, the same way
	// an unreadable manifest skips the package check.
	KindIndeterminate Kind = "indeterminate"
)

// Describe renders a Kind the way a human error message wants to read it.
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

// Info is what a local inspection could establish about an artifact.
//
// Package is the package name the artifact's manifest declares, or "" when
// the manifest could not be read. Note carries the human-readable reason in
// that degraded case: an unparseable manifest must never produce a false
// refusal (a parser gap is gplay's problem, not the user's), so the caller
// surfaces Note on stderr and proceeds with the container check alone.
type Info struct {
	Kind    Kind
	Package string
	Note    string
}

// Expect is what the calling command promised the artifact would be.
//
// An empty Kinds skips the container check entirely; otherwise the artifact
// must be one of the listed kinds. It is a set rather than a single value
// because the surfaces genuinely differ: `releases upload --format bundle`
// accepts exactly an AAB, `customapps create` accepts either an AAB or an
// APK, and `releases expansion-files upload` accepts only KindUnknown (an
// .obb is neither). Package "" skips the package check, either because the
// surface has no package to compare against or because the manifest was
// unreadable.
type Expect struct {
	Kinds   []Kind
	Package string
}

// describeKinds renders an expectation the way an error message wants it:
// "an Android App Bundle (AAB) or an APK".
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

// Error is a preflight refusal: the artifact is readable but it is not what
// the command is about to upload it as. It exits 20, "client-side
// validation" (docs/DESIGN.md §9), the same code the sibling artifact checks
// on these surfaces already use: the API was never reached, and the file is
// exactly the "malformed AAB" case the table names.
type Error struct {
	Path string
	// Reason is the one-line "expected X, found Y" body. It always names both
	// sides so an agent can self-correct the invocation without scraping.
	Reason string
}

func (e *Error) Error() string {
	return fmt.Sprintf("artifact preflight: %s: %s (pass --skip-preflight to upload anyway)", e.Reason, e.Path)
}

// ExitCode implements exit.Coder.
func (e *Error) ExitCode() int { return 20 }

// IOError is a local-file failure: the artifact is missing, unreadable, or
// not a regular file. It is distinct from Error (which means "readable but
// wrong") and shares the same exit code.
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

// ExitCode implements exit.Coder.
func (e *IOError) ExitCode() int { return 20 }

// Preflight inspects path and refuses when it does not match want. It
// returns the Info it established so the caller can surface a degraded-parse
// note; on success it is silent by contract (the happy path's output must be
// unchanged).
//
// Order matters: the container check runs first, because "you passed an APK
// to a bundle upload" is the more actionable message when both checks would
// fail.
func Preflight(path string, want Expect) (Info, error) {
	info, err := Inspect(path)
	if err != nil {
		return info, err
	}
	// An unclassified container degrades exactly like an unreadable manifest:
	// gplay not knowing what the file is says nothing about the file, so it
	// can never be grounds for a refusal. The reason travels in info.Note.
	if info.Kind == KindIndeterminate {
		return info, nil
	}
	if len(want.Kinds) > 0 && !slices.Contains(want.Kinds, info.Kind) {
		return info, &Error{
			Path:   path,
			Reason: fmt.Sprintf("expected %s, found %s", describeKinds(want.Kinds), info.Kind.Describe()),
		}
	}
	// An unreadable manifest degrades to the container check: never a false
	// refusal on a parser gap.
	if want.Package != "" && info.Package != "" && want.Package != info.Package {
		return info, &Error{
			Path: path,
			Reason: fmt.Sprintf("package mismatch: this upload targets %q but the artifact declares %q",
				want.Package, info.Package),
		}
	}
	return info, nil
}

// Options tunes one Verify call to the invocation's flags.
type Options struct {
	// Skip is the --skip-preflight escape hatch: the container and package
	// checks are dropped, so a parser gap on an unusual but legitimate
	// artifact can never block a release. It restores exactly the
	// pre-preflight behaviour, which means the local-file check stays: a
	// missing or non-regular path was already exit 20 before the preflight
	// existed and must not become a silent success.
	Skip bool
	// Report is the --dry-run rehearsal: name what the artifact turned out to
	// be even when it matches, since a preview that stays silent tells the
	// user nothing about the check it just ran. Never set on the live path,
	// where success is silent by contract.
	Report bool
}

// Verify is the one call an upload command makes: it runs the preflight,
// writes any advisory note to stderr, and returns the refusal (if any) for
// the command to propagate. Silent on a live-path success.
//
// stderr may be nil (a hand-built RunContext in a test); notes are then
// dropped, which is correct: they are advisory, and the refusal travels in
// the error, never in the note.
func Verify(stderr io.Writer, path string, want Expect, opts Options) error {
	if opts.Skip {
		// --skip-preflight lifts the container check, not the file check the
		// surfaces ran before this package existed.
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

// Inspect reads path and reports what it is. A missing, unreadable or
// non-regular file is an *IOError; anything that opens successfully yields
// an Info, never an error. A file that is readable but carries no marker is
// KindUnknown (a fact about the file); one gplay stopped short of walking is
// KindIndeterminate (a fact about gplay). Neither is a failure.
func Inspect(path string) (Info, error) {
	if err := statArtifact(path); err != nil {
		return Info{}, err
	}

	zr, err := zip.OpenReader(path)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return Info{}, &IOError{Path: path, Msg: "cannot read artifact", Err: err}
		}
		// Not a zip at all: neither an AAB nor an APK, and that is a fact,
		// not an error. The .obb surface relies on exactly this.
		return Info{Kind: KindUnknown}, nil
	}
	defer func() { _ = zr.Close() }()

	return inspectZip(&zr.Reader), nil
}

// statArtifact is the local-file check every upload surface owes its caller,
// preflight or not: the path must exist and be a regular file. Split out
// because --skip-preflight drops the classification but keeps this.
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

// inspectZip is the classification proper, split out so tests can drive it
// from an in-memory zip without touching the filesystem.
func inspectZip(zr *zip.Reader) Info {
	// Bounding the member count keeps a crafted archive with millions of tiny
	// entries from turning the classification into the slow path the
	// preflight exists to avoid. It bounds this walk only: zip.OpenReader has
	// already read the whole central directory by the time we get here.
	//
	// Over the cap the answer is KindIndeterminate, never KindUnknown. An
	// asset-rich AAB really does carry tens of thousands of members, and
	// answering "neither an AAB nor an APK" would turn gplay's own budget
	// into a refusal of a perfectly good bundle.
	if len(zr.File) > MaxZipEntries {
		return Info{
			Kind: KindIndeterminate,
			Note: fmt.Sprintf("artifact has %d zip members, over the %d-member preflight cap: container not classified, upload proceeding unchecked", len(zr.File), MaxZipEntries),
		}
	}

	byName := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		// First occurrence wins: a duplicated member name is a classic
		// spoofing trick, and the readers Google uses take the first too.
		if _, seen := byName[f.Name]; !seen {
			byName[f.Name] = f
		}
	}

	budget := int64(maxTotalDecompressedBytes)

	// AAB markers are checked first because they are the more specific pair:
	// an AAB has no root AndroidManifest.xml, so the two cannot both match.
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

// Zip member paths that discriminate the two container formats. Both are
// fixed by the tooling that produces them (bundletool / aapt2) and are what
// Play itself keys on.
const (
	aabManifestPath     = "base/manifest/AndroidManifest.xml"
	aabBundleConfigPath = "BundleConfig.pb"
	apkManifestPath     = "AndroidManifest.xml"
)

// Expansion bounds. The preflight parses bytes it did not produce, from a
// file a CI job may not control, so every decompression is capped: a crafted
// artifact must not be able to zip-bomb the check that exists to make
// uploads fail fast.
const (
	// MaxZipEntries caps the member count the classifier will walk. Exported
	// so the preflight suites build their over-cap fixtures from the real
	// number instead of restating it. Above it, Inspect yields
	// KindIndeterminate: gplay stops looking, it does not conclude.
	MaxZipEntries = 1 << 16
	// maxManifestBytes caps a single decompressed member. A real
	// AndroidManifest.xml is tens of kilobytes.
	maxManifestBytes = 4 << 20
	// maxTotalDecompressedBytes caps everything one Inspect may decompress,
	// so repeated reads cannot add up past the per-member cap.
	maxTotalDecompressedBytes = 8 << 20
)

// readEntry decompresses one zip member under both the per-member cap and
// the shared budget, refusing rather than allocating when either would be
// exceeded. The declared size is checked before any decompression so the
// common bomb (a small deflate stream expanding to gigabytes) never runs.
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

	// cap64+1 so an entry that lies about UncompressedSize64 is caught by the
	// actual byte count rather than by the header we cannot trust.
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
