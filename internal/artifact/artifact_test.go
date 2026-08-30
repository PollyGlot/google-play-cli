package artifact_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/artifact"
	"github.com/PollyGlot/google-play-cli/internal/artifacttest"
	"github.com/PollyGlot/google-play-cli/internal/exit"
)

func TestInspect_classifiesContainersByStructure(t *testing.T) {
	dir := t.TempDir()

	// Extensions are deliberately wrong everywhere: the whole point is that
	// classification reads the container, not the name.
	aab := artifacttest.AAB(t, dir, "mislabeled.apk", "com.example.app")
	apk := artifacttest.APK(t, dir, "mislabeled.aab", "com.example.app")
	obb := artifacttest.Zip(t, dir, "assets.obb", map[string][]byte{"data.bin": []byte("assets")})
	notZip := artifacttest.WriteFile(t, dir, "notes.aab", []byte("this is not a zip at all"))

	for _, tc := range []struct {
		name    string
		path    string
		want    artifact.Kind
		wantPkg string
	}{
		{"aab named .apk", aab, artifact.KindBundle, "com.example.app"},
		{"apk named .aab", apk, artifact.KindAPK, "com.example.app"},
		{"plain zip", obb, artifact.KindUnknown, ""},
		{"not a zip", notZip, artifact.KindUnknown, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := artifact.Inspect(tc.path)
			if err != nil {
				t.Fatalf("Inspect(%s): %v", tc.path, err)
			}
			if got.Kind != tc.want {
				t.Errorf("Kind = %q, want %q", got.Kind, tc.want)
			}
			if got.Package != tc.wantPkg {
				t.Errorf("Package = %q, want %q", got.Package, tc.wantPkg)
			}
		})
	}
}

func TestInspect_readsUTF16StringPool(t *testing.T) {
	dir := t.TempDir()
	path := artifacttest.Zip(t, dir, "legacy.apk", map[string][]byte{
		"AndroidManifest.xml": artifacttest.BinaryXMLManifest("com.legacy.aapt1", true),
	})
	got, err := artifact.Inspect(path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if got.Kind != artifact.KindAPK || got.Package != "com.legacy.aapt1" {
		t.Fatalf("Inspect = %+v, want APK/com.legacy.aapt1", got)
	}
}

func TestInspect_missingAndNonRegularPathsAreIOErrors(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{filepath.Join(dir, "nope.aab"), dir} {
		_, err := artifact.Inspect(path)
		var ioErr *artifact.IOError
		if !errors.As(err, &ioErr) {
			t.Fatalf("Inspect(%s) error = %v, want *artifact.IOError", path, err)
		}
		if exit.For(err) != 20 {
			t.Errorf("exit code = %d, want 20", exit.For(err))
		}
	}
}

func TestPreflight_refusesKindMismatchNamingBothSides(t *testing.T) {
	dir := t.TempDir()
	apk := artifacttest.APK(t, dir, "app.apk", "com.example.app")

	_, err := artifact.Preflight(apk, artifact.Expect{Kinds: []artifact.Kind{artifact.KindBundle}})
	var refusal *artifact.Error
	if !errors.As(err, &refusal) {
		t.Fatalf("Preflight error = %v, want *artifact.Error", err)
	}
	if exit.For(err) != 20 {
		t.Errorf("exit code = %d, want 20 (client-side validation, DESIGN §9)", exit.For(err))
	}
	msg := err.Error()
	for _, want := range []string{"expected an Android App Bundle (AAB)", "found an APK", apk, "--skip-preflight"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not name %q", msg, want)
		}
	}
}

func TestPreflight_refusesPackageMismatchNamingBothSides(t *testing.T) {
	dir := t.TempDir()
	aab := artifacttest.AAB(t, dir, "app.aab", "com.other.app")

	_, err := artifact.Preflight(aab, artifact.Expect{Kinds: []artifact.Kind{artifact.KindBundle}, Package: "com.example.app"})
	var refusal *artifact.Error
	if !errors.As(err, &refusal) {
		t.Fatalf("Preflight error = %v, want *artifact.Error", err)
	}
	msg := err.Error()
	for _, want := range []string{"package mismatch", `"com.example.app"`, `"com.other.app"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not name %q", msg, want)
		}
	}
}

func TestPreflight_acceptsMatchingArtifactSilently(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name string
		path string
		kind artifact.Kind
	}{
		{"bundle", artifacttest.AAB(t, dir, "ok.aab", "com.example.app"), artifact.KindBundle},
		{"apk", artifacttest.APK(t, dir, "ok.apk", "com.example.app"), artifact.KindAPK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info, err := artifact.Preflight(tc.path, artifact.Expect{Kinds: []artifact.Kind{tc.kind}, Package: "com.example.app"})
			if err != nil {
				t.Fatalf("Preflight: %v", err)
			}
			if info.Note != "" {
				t.Errorf("Note = %q, want empty: a matching artifact is silent", info.Note)
			}
		})
	}
}

func TestPreflight_emptyExpectationsSkipTheirCheck(t *testing.T) {
	dir := t.TempDir()
	apk := artifacttest.APK(t, dir, "app.apk", "com.other.app")

	// No Kind and no Package: the preflight has nothing to refuse on.
	if _, err := artifact.Preflight(apk, artifact.Expect{}); err != nil {
		t.Fatalf("Preflight with an empty Expect: %v", err)
	}
	// Kind only: a package mismatch is not checked when none is expected.
	if _, err := artifact.Preflight(apk, artifact.Expect{Kinds: []artifact.Kind{artifact.KindAPK}}); err != nil {
		t.Fatalf("Preflight with Kind only: %v", err)
	}
}

func TestPreflight_unparseableManifestDegradesToContainerCheck(t *testing.T) {
	dir := t.TempDir()
	// A well-formed AAB container whose protobuf manifest is garbage: gplay's
	// parser gap must never become a false refusal.
	broken := artifacttest.Zip(t, dir, "broken.aab", map[string][]byte{
		"BundleConfig.pb":                   {},
		"base/manifest/AndroidManifest.xml": []byte("\xff\xff\xff\xff not protobuf"),
	})

	info, err := artifact.Preflight(broken, artifact.Expect{Kinds: []artifact.Kind{artifact.KindBundle}, Package: "com.example.app"})
	if err != nil {
		t.Fatalf("Preflight: %v, want the container check to pass and the package check to be skipped", err)
	}
	if info.Package != "" {
		t.Errorf("Package = %q, want empty", info.Package)
	}
	if info.Note == "" {
		t.Error("Note is empty: a degraded parse must be reportable on stderr")
	}

	// The container check still bites on the same artifact.
	if _, err := artifact.Preflight(broken, artifact.Expect{Kinds: []artifact.Kind{artifact.KindAPK}}); err == nil {
		t.Error("Preflight accepted an AAB where an APK was expected")
	}
}

func TestPreflight_bundleWithoutManifestStillClassifies(t *testing.T) {
	dir := t.TempDir()
	// BundleConfig.pb alone is enough to know it is an AAB, but not enough to
	// read a package name.
	path := artifacttest.Zip(t, dir, "configonly.aab", map[string][]byte{"BundleConfig.pb": {}})
	info, err := artifact.Preflight(path, artifact.Expect{Kinds: []artifact.Kind{artifact.KindBundle}, Package: "com.example.app"})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if info.Note == "" {
		t.Error("Note is empty: the unverified package must be reportable")
	}
}

func TestInspect_refusesAZipBombManifest(t *testing.T) {
	dir := t.TempDir()
	// 32 MiB of zeros compresses to a few kilobytes and is well past the
	// 4 MiB per-member cap.
	bomb := make([]byte, 32<<20)
	path := artifacttest.Zip(t, dir, "bomb.apk", map[string][]byte{"AndroidManifest.xml": bomb})

	if st, err := os.Stat(path); err != nil || st.Size() > 1<<20 {
		t.Fatalf("fixture is not a compression bomb (size=%v err=%v)", st, err)
	}

	info, err := artifact.Inspect(path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.Kind != artifact.KindAPK {
		t.Errorf("Kind = %q, want apk: the container is still classified", info.Kind)
	}
	if info.Package != "" {
		t.Errorf("Package = %q, want empty", info.Package)
	}
	if !strings.Contains(info.Note, "cap") {
		t.Errorf("Note = %q, want it to name the cap", info.Note)
	}
}

// TestPreflight_overTheMemberCapIsIndeterminateNotUnknown pins the
// distinction the member cap got wrong: an artifact gplay declined to walk is
// unclassified, not "neither an AAB nor an APK". An asset-rich AAB genuinely
// carries more members than the cap, and refusing it would make gplay's own
// budget the reason a good release fails (ADR-0046: a parser gap never
// becomes a false refusal).
func TestPreflight_overTheMemberCapIsIndeterminateNotUnknown(t *testing.T) {
	dir := t.TempDir()
	filler := artifacttest.FillerMembers(artifact.MaxZipEntries + 1)
	aab := artifacttest.AABWith(t, dir, "huge.aab", "com.example.app", filler)
	apk := artifacttest.APKWith(t, dir, "huge.apk", "com.example.app", filler)

	for _, tc := range []struct {
		name string
		path string
		// want is the expectation the surface that uploads this file states.
		want artifact.Expect
	}{
		// `releases upload --format bundle` on an asset-rich AAB.
		{"bundle expectation", aab, artifact.Expect{Kinds: []artifact.Kind{artifact.KindBundle}, Package: "com.example.app"}},
		// `releases expansion-files upload` on an over-cap archive: the
		// unknown expectation must be skipped too, not silently satisfied.
		{"unknown expectation", apk, artifact.Expect{Kinds: []artifact.Kind{artifact.KindUnknown}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info, err := artifact.Preflight(tc.path, tc.want)
			if err != nil {
				t.Fatalf("Preflight: %v, want an over-cap artifact to degrade rather than be refused", err)
			}
			if info.Kind != artifact.KindIndeterminate {
				t.Errorf("Kind = %q, want %q", info.Kind, artifact.KindIndeterminate)
			}
			if !strings.Contains(info.Note, "member") {
				t.Errorf("Note = %q, want it to name the member cap", info.Note)
			}
		})
	}
}

// TestVerify_skipKeepsTheLocalFileCheck asserts --skip-preflight lifts the
// container check and nothing else: a missing or non-regular path was exit 20
// on these surfaces before the preflight existed and stays exit 20.
func TestVerify_skipKeepsTheLocalFileCheck(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name string
		path string
	}{
		{"missing", filepath.Join(dir, "never-built.aab")},
		{"not a regular file", dir},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := artifact.Verify(io.Discard, tc.path, artifact.Expect{Kinds: []artifact.Kind{artifact.KindBundle}}, artifact.Options{Skip: true})
			var ioErr *artifact.IOError
			if !errors.As(err, &ioErr) {
				t.Fatalf("Verify error = %v, want *artifact.IOError", err)
			}
			if exit.For(err) != 20 {
				t.Errorf("exit code = %d, want 20", exit.For(err))
			}
		})
	}

	// The classification itself is still skipped: a file that is not an
	// Android package at all uploads as-is.
	notAnArtifact := artifacttest.WriteFile(t, dir, "notes.aab", []byte("not an artifact at all"))
	if err := artifact.Verify(io.Discard, notAnArtifact, artifact.Expect{Kinds: []artifact.Kind{artifact.KindBundle}}, artifact.Options{Skip: true}); err != nil {
		t.Errorf("Verify with Skip refused an unclassifiable file: %v", err)
	}
}

// TestInspect_readsAPackageNameOverTheOneByteLengthPrefix covers aapt's
// two-byte length prefix, the encoding every UTF-8 pool string longer than
// 127 bytes uses. Package names that long are legal, and the reader's
// continuation branch had no fixture reaching it.
func TestInspect_readsAPackageNameOverTheOneByteLengthPrefix(t *testing.T) {
	long := strings.Repeat("com.example.averylongsegment.", 12) + "app"
	if len(long) <= 0x7F {
		t.Fatalf("fixture package is %d bytes, need more than 127 to exercise the two-byte prefix", len(long))
	}
	path := artifacttest.APK(t, t.TempDir(), "long.apk", long)

	got, err := artifact.Inspect(path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if got.Package != long {
		t.Errorf("Package = %q, want %q", got.Package, long)
	}
}
