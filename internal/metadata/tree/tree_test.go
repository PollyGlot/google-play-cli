package tree_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/metadata/tree"
)

// writeFile is a test helper: it creates parent dirs and writes raw
// bytes verbatim (no newline normalization) so a test can lay down an
// exact on-disk fixture.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// get is a terse accessor for asserting (value, managed) in tests.
func get(t *testing.T, l listing.Listing, f listing.Field) (string, bool) {
	t.Helper()
	return l.Get(f)
}

// TestRead_multipleLocalesAndFields covers the headline AC: a
// `<locale>/*.txt` tree with several locales × fields reads into the
// typed model, with the trailing newline of each file stripped.
func TestRead_multipleLocalesAndFields(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "en-US", "title.txt"), "My App\n")
	writeFile(t, filepath.Join(dir, "en-US", "short_description.txt"), "Short\n")
	writeFile(t, filepath.Join(dir, "en-US", "full_description.txt"), "Long description.\n")
	writeFile(t, filepath.Join(dir, "en-US", "video.txt"), "https://youtu.be/x\n")
	writeFile(t, filepath.Join(dir, "fr-FR", "title.txt"), "Mon appli\n")
	writeFile(t, filepath.Join(dir, "fr-FR", "full_description.txt"), "Description longue.\n")

	tr, err := tree.Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if got := tr.Locales(); !reflect.DeepEqual(got, []string{"en-US", "fr-FR"}) {
		t.Fatalf("locales = %v, want [en-US fr-FR]", got)
	}

	en := tr["en-US"]
	for f, want := range map[listing.Field]string{
		listing.Title:            "My App",
		listing.ShortDescription: "Short",
		listing.FullDescription:  "Long description.",
		listing.Video:            "https://youtu.be/x",
	} {
		if v, ok := get(t, en, f); !ok || v != want {
			t.Errorf("en-US field %v = %q, %v; want %q, true", f, v, ok, want)
		}
	}

	fr := tr["fr-FR"]
	if v, ok := get(t, fr, listing.Title); !ok || v != "Mon appli" {
		t.Errorf("fr-FR title = %q, %v; want Mon appli, true", v, ok)
	}
	// fr-FR has no short_description file → that field is unmanaged.
	if _, ok := get(t, fr, listing.ShortDescription); ok {
		t.Error("fr-FR short_description = managed, want unmanaged (no file)")
	}
}

// TestRead_missingVsPresentEmpty is the core ADR-0011 distinction: an
// absent file is an unmanaged field; a present-but-empty file is a
// managed empty value ("clear online").
func TestRead_missingVsPresentEmpty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "en-US", "title.txt"), "Title\n")
	// short_description present but empty → managed clear.
	writeFile(t, filepath.Join(dir, "en-US", "short_description.txt"), "")
	// full_description and video absent → unmanaged.

	tr, err := tree.Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	en := tr["en-US"]

	if v, ok := get(t, en, listing.ShortDescription); !ok || v != "" {
		t.Errorf("short_description = %q, %v; want \"\", true (present-but-empty = managed clear)", v, ok)
	}
	if _, ok := get(t, en, listing.FullDescription); ok {
		t.Error("full_description = managed, want unmanaged (file absent)")
	}
	if _, ok := get(t, en, listing.Video); ok {
		t.Error("video = managed, want unmanaged (file absent)")
	}
}

// TestRead_ignoresStraysAndChangelogs asserts the metadata/releases
// boundary on disk: a root-level changelogs/, a per-locale changelogs/,
// unknown *.txt, a README, and a *.md are all ignored; a top-level file
// (not a directory) is not treated as a locale.
func TestRead_ignoresStraysAndChangelogs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "en-US", "title.txt"), "Title\n")
	// Strays inside the locale: unknown .txt, README, .md → ignored.
	writeFile(t, filepath.Join(dir, "en-US", "default.txt"), "ignored\n")
	writeFile(t, filepath.Join(dir, "en-US", "README.md"), "notes\n")
	writeFile(t, filepath.Join(dir, "en-US", "keywords.txt"), "a,b,c\n")
	// changelogs/ nested in a locale → ignored (not descended).
	writeFile(t, filepath.Join(dir, "en-US", "changelogs", "100.txt"), "what's new\n")
	// changelogs/ at the root → ignored (no recognized field file).
	writeFile(t, filepath.Join(dir, "changelogs", "100.txt"), "what's new\n")
	// Top-level README file (not a dir) → not a locale.
	writeFile(t, filepath.Join(dir, "README.md"), "top-level\n")

	tr, err := tree.Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if got := tr.Locales(); !reflect.DeepEqual(got, []string{"en-US"}) {
		t.Fatalf("locales = %v, want [en-US] (changelogs/ and README ignored)", got)
	}
	en := tr["en-US"]
	if v, ok := get(t, en, listing.Title); !ok || v != "Title" {
		t.Errorf("title = %q, %v; want Title, true", v, ok)
	}
	// Only title.txt was recognized — the locale has exactly one field.
	if len(en.Fields) != 1 {
		t.Errorf("en-US manages %d fields, want 1 (strays ignored): %v", len(en.Fields), en.Fields)
	}
}

// TestRead_localeWithNoRecognizedFieldIsOmitted asserts a directory that
// holds only strays produces no Tree entry at all.
func TestRead_localeWithNoRecognizedFieldIsOmitted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "en-US", "title.txt"), "Title\n")
	writeFile(t, filepath.Join(dir, "junk", "README.md"), "nothing here\n")

	tr, err := tree.Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if _, ok := tr["junk"]; ok {
		t.Error("junk/ produced a Tree entry, want omitted (no recognized field)")
	}
	if got := tr.Locales(); !reflect.DeepEqual(got, []string{"en-US"}) {
		t.Fatalf("locales = %v, want [en-US]", got)
	}
}

// TestRead_knownLocaleMisnamedFieldFile asserts the typo guard: a
// directory named like a KNOWN Play locale that holds an unrecognized
// *.txt and no recognized field file is a filename typo — Read returns a
// *LocaleNoFieldsError rather than silently dropping the locale (which,
// under apply --prune, would delete the live Listing).
func TestRead_knownLocaleMisnamedFieldFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "en-US", "title.txt"), "Title\n")
	// de-DE is a known locale, but its description file is mis-named
	// (hyphen instead of underscore) → no recognized field → typo.
	writeFile(t, filepath.Join(dir, "de-DE", "full-description.txt"), "Beschreibung\n")

	_, err := tree.Read(dir)
	if err == nil {
		t.Fatal("Read = nil error, want *LocaleNoFieldsError for a mis-named field file")
	}
	var lnf *tree.LocaleNoFieldsError
	if !errors.As(err, &lnf) {
		t.Fatalf("err = %v (%T), want *LocaleNoFieldsError", err, err)
	}
	if lnf.Locale != "de-DE" {
		t.Errorf("LocaleNoFieldsError.Locale = %q, want de-DE", lnf.Locale)
	}
	if !reflect.DeepEqual(lnf.Files, []string{"full-description.txt"}) {
		t.Errorf("LocaleNoFieldsError.Files = %v, want [full-description.txt]", lnf.Files)
	}
}

// TestRead_knownLocaleReadmeOnlyIsBenign asserts a known-locale dir holding
// only a non-.txt stray (a README) is NOT a typo error — it stays silently
// ignored, matching the documented metadata/releases boundary.
func TestRead_knownLocaleReadmeOnlyIsBenign(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "en-US", "title.txt"), "Title\n")
	writeFile(t, filepath.Join(dir, "fr-FR", "README.md"), "notes for translators\n")

	tr, err := tree.Read(dir)
	if err != nil {
		t.Fatalf("Read: %v (README-only known-locale dir should be ignored, not an error)", err)
	}
	if got := tr.Locales(); !reflect.DeepEqual(got, []string{"en-US"}) {
		t.Fatalf("locales = %v, want [en-US] (fr-FR README-only ignored)", got)
	}
}

// TestRead_unknownLocaleMisnamedFieldFileIsBenign asserts the typo guard is
// scoped to KNOWN locales: a non-locale dir (or a locale Google added after
// this release) with a stray .txt is still silently ignored, so the codec
// never errors on a directory it cannot vouch for as locale-shaped.
func TestRead_unknownLocaleMisnamedFieldFileIsBenign(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "en-US", "title.txt"), "Title\n")
	// "notes" is not a known Play locale → a stray .txt inside it is ignored.
	writeFile(t, filepath.Join(dir, "notes", "full-description.txt"), "x\n")

	tr, err := tree.Read(dir)
	if err != nil {
		t.Fatalf("Read: %v (unknown-locale dir with a stray .txt should be ignored)", err)
	}
	if got := tr.Locales(); !reflect.DeepEqual(got, []string{"en-US"}) {
		t.Fatalf("locales = %v, want [en-US] (notes/ ignored)", got)
	}
}

// TestRead_emptyDir asserts an existing but empty dir is a non-error,
// empty Tree.
func TestRead_emptyDir(t *testing.T) {
	dir := t.TempDir()
	tr, err := tree.Read(dir)
	if err != nil {
		t.Fatalf("Read(empty dir): %v", err)
	}
	if tr == nil {
		t.Fatal("Read(empty dir) = nil Tree, want empty non-nil")
	}
	if len(tr) != 0 {
		t.Errorf("Read(empty dir) = %v, want empty", tr)
	}
}

// TestRead_missingDir asserts a non-existent dir is an error the caller
// can map to an exit code.
func TestRead_missingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := tree.Read(dir); err == nil {
		t.Fatal("Read(missing dir) = nil error, want error")
	}
}

// TestRead_trimsOnlyOneTrailingNewline asserts the trailing-newline rule
// is "strip at most one", preserving internal newlines and any extra
// trailing blank line of a full_description.
func TestRead_trimsOnlyOneTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	// Internal newlines kept; exactly one trailing \n stripped → the
	// value retains its own trailing blank line.
	writeFile(t, filepath.Join(dir, "en-US", "full_description.txt"), "line1\nline2\n\n")
	// CRLF line ending: only the final \n is stripped — the preceding \r
	// is part of the value. The codec strips EXACTLY what Write appends (one
	// \n) and does not normalize CRLF, so Read stays the exact inverse of
	// Write (save field files as LF for clean values).
	writeFile(t, filepath.Join(dir, "en-US", "title.txt"), "Title\r\n")
	// No trailing newline at all: value is taken verbatim.
	writeFile(t, filepath.Join(dir, "en-US", "video.txt"), "https://youtu.be/x")

	tr, err := tree.Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	en := tr["en-US"]
	if v, _ := get(t, en, listing.FullDescription); v != "line1\nline2\n" {
		t.Errorf("full_description = %q, want %q (internal newline + one trailing blank line kept)", v, "line1\nline2\n")
	}
	if v, _ := get(t, en, listing.Title); v != "Title\r" {
		t.Errorf("title = %q, want %q (only the trailing \\n stripped, \\r kept)", v, "Title\r")
	}
	if v, _ := get(t, en, listing.Video); v != "https://youtu.be/x" {
		t.Errorf("video = %q, want verbatim (no trailing newline to strip)", v)
	}
}

// TestWrite_exactBytesPerField asserts Write produces one file per
// managed field with the exact bytes: value+"\n" for a non-empty value
// and a 0-byte file for a managed empty value.
func TestWrite_exactBytesPerField(t *testing.T) {
	dir := t.TempDir()
	l := listing.NewListing("en-US")
	l.Set(listing.Title, "My App")
	l.Set(listing.FullDescription, "line1\nline2")
	l.Set(listing.ShortDescription, "") // managed clear → 0-byte file
	tr := listing.Tree{"en-US": l}

	if err := tree.Write(dir, tr); err != nil {
		t.Fatalf("Write: %v", err)
	}

	cases := map[string]string{
		"title.txt":             "My App\n",
		"full_description.txt":  "line1\nline2\n",
		"short_description.txt": "", // 0 bytes
	}
	for file, want := range cases {
		b, err := os.ReadFile(filepath.Join(dir, "en-US", file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if string(b) != want {
			t.Errorf("%s = %q, want %q", file, b, want)
		}
	}

	// video was never managed → no file written (additive, no clear).
	if _, err := os.Stat(filepath.Join(dir, "en-US", "video.txt")); !os.IsNotExist(err) {
		t.Errorf("video.txt exists or stat err %v, want not-exist (unmanaged field not written)", err)
	}
}

// TestWrite_isAdditive asserts Write never deletes a pre-existing file,
// even one for an unmanaged field — pruning is a higher-layer concern.
func TestWrite_isAdditive(t *testing.T) {
	dir := t.TempDir()
	// Pre-existing file for a field the Listing won't manage.
	writeFile(t, filepath.Join(dir, "en-US", "video.txt"), "https://old\n")

	l := listing.NewListing("en-US")
	l.Set(listing.Title, "My App")
	if err := tree.Write(dir, listing.Tree{"en-US": l}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dir, "en-US", "video.txt"))
	if err != nil {
		t.Fatalf("read video.txt: %v", err)
	}
	if string(b) != "https://old\n" {
		t.Errorf("video.txt = %q, want untouched (Write is additive)", b)
	}
}

// TestWrite_createsLocaleDirs asserts Write makes the locale directory
// tree when it does not yet exist.
func TestWrite_createsLocaleDirs(t *testing.T) {
	dir := t.TempDir()
	l := listing.NewListing("de-DE")
	l.Set(listing.Title, "Meine App")
	if err := tree.Write(dir, listing.Tree{"de-DE": l}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "de-DE", "title.txt")); err != nil {
		t.Errorf("title.txt not created: %v", err)
	}
}

// TestRoundTrip_writeReadIsIdentity is the key invariant: for any Tree
// shaped like one a Read produces, Read(Write(X)) == X at the value
// level — including a managed-empty field that transits via a 0-byte
// file. The "+\n" write and the "strip one \n" read are inverses.
func TestRoundTrip_writeReadIsIdentity(t *testing.T) {
	cases := map[string]listing.Tree{
		"single locale, all fields": {
			"en-US": mk("en-US", map[listing.Field]string{
				listing.Title:            "My App",
				listing.ShortDescription: "Short blurb",
				listing.FullDescription:  "A long description.",
				listing.Video:            "https://youtu.be/x",
			}),
		},
		"managed-empty field (clear)": {
			"en-US": mk("en-US", map[listing.Field]string{
				listing.Title:            "Title",
				listing.ShortDescription: "", // 0-byte file round-trip
			}),
		},
		"multi-line full_description with internal blank lines": {
			"fr-FR": mk("fr-FR", map[listing.Field]string{
				listing.Title:           "Mon appli",
				listing.FullDescription: "Para un.\n\nPara deux.\n", // trailing blank line preserved
			}),
		},
		"value ending in carriage return": {
			// Regression guard (CodeRabbit, PR #110): a value whose last byte
			// is "\r" must survive Write→Read. Write appends "\n" → "x\r\n";
			// Read strips ONLY the "\n" → "x\r". If Read also stripped the
			// "\r", this would silently break the pull→apply no-op invariant
			// for Play-sourced text with CR line endings.
			"en-GB": mk("en-GB", map[listing.Field]string{
				listing.Title:           "Title\r",
				listing.FullDescription: "a\r\nb\r", // internal CR + trailing CR
			}),
		},
		"several locales, partial field coverage": {
			"en-US": mk("en-US", map[listing.Field]string{
				listing.Title:           "App",
				listing.FullDescription: "Desc",
			}),
			"fr-FR": mk("fr-FR", map[listing.Field]string{
				listing.Title: "Appli",
			}),
			"de-DE": mk("de-DE", map[listing.Field]string{
				listing.Title:            "App DE",
				listing.ShortDescription: "Kurz",
				listing.Video:            "",
			}),
		},
	}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := tree.Write(dir, in); err != nil {
				t.Fatalf("Write: %v", err)
			}
			out, err := tree.Read(dir)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if !treeEqual(in, out) {
				t.Errorf("round-trip mismatch:\n in  = %s\n out = %s", dump(in), dump(out))
			}
		})
	}
}

// mk builds a Listing for the round-trip table. The map's presence of a
// key is what makes the field managed (value may be "").
func mk(locale string, fields map[listing.Field]string) listing.Listing {
	l := listing.NewListing(locale)
	for f, v := range fields {
		l.Set(f, v)
	}
	return l
}

// treeEqual compares two Trees at the value level: same locales, and for
// each locale the same managed fields with the same values (managed-empty
// counts as present, matching the codec's contract).
func treeEqual(a, b listing.Tree) bool {
	if !reflect.DeepEqual(a.Locales(), b.Locales()) {
		return false
	}
	for _, loc := range a.Locales() {
		la, lb := a[loc], b[loc]
		if !reflect.DeepEqual(la.Fields, lb.Fields) {
			return false
		}
	}
	return true
}

// dump renders a Tree compactly for failure messages.
func dump(tr listing.Tree) string {
	out := ""
	for _, loc := range tr.Locales() {
		out += loc + "{"
		for _, f := range listing.Fields() {
			if v, ok := tr[loc].Get(f); ok {
				s, _ := listing.SpecOf(f)
				out += s.Key + "=" + quote(v) + " "
			}
		}
		out += "} "
	}
	return out
}

func quote(s string) string {
	return "\"" + s + "\""
}
