package listing_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
)

// TestSpecs_canonicalTable pins the four managed fields' file names, API
// keys, char limits, and required-ness — the contract every metadata
// module builds on. A change here is a deliberate contract change.
func TestSpecs_canonicalTable(t *testing.T) {
	want := []struct {
		field    listing.Field
		file     string
		key      string
		maxChars int
		required bool
	}{
		{listing.Title, "title.txt", "title", 30, true},
		{listing.ShortDescription, "short_description.txt", "shortDescription", 80, false},
		{listing.FullDescription, "full_description.txt", "fullDescription", 4000, true},
		{listing.Video, "video.txt", "video", 0, false},
	}
	specs := listing.Specs()
	if len(specs) != len(want) {
		t.Fatalf("Specs() len = %d, want %d", len(specs), len(want))
	}
	for i, w := range want {
		s := specs[i]
		if s.Field != w.field || s.File != w.file || s.Key != w.key || s.MaxChars != w.maxChars || s.Required != w.required {
			t.Errorf("spec[%d] = %+v, want field=%v file=%q key=%q max=%d req=%v",
				i, s, w.field, w.file, w.key, w.maxChars, w.required)
		}
	}
}

// TestSpecs_returnsCopy guards that mutating the returned slice cannot
// corrupt the package-level table.
func TestSpecs_returnsCopy(t *testing.T) {
	got := listing.Specs()
	got[0].File = "hacked.txt"
	if s, _ := listing.SpecByFile("title.txt"); s.File != "title.txt" {
		t.Errorf("mutating Specs() leaked into package table: %+v", s)
	}
}

// TestSpecByFile_knownAndUnknown asserts the file→Spec lookup recognizes
// the fastlane names and rejects everything else (so the codec ignores
// strays and changelogs/).
func TestSpecByFile_knownAndUnknown(t *testing.T) {
	if s, ok := listing.SpecByFile("full_description.txt"); !ok || s.Field != listing.FullDescription {
		t.Errorf("SpecByFile(full_description.txt) = %+v, %v; want FullDescription, true", s, ok)
	}
	for _, bad := range []string{"README.md", "title", "fulldescription.txt", "default.txt", ""} {
		if _, ok := listing.SpecByFile(bad); ok {
			t.Errorf("SpecByFile(%q) = ok, want not recognized", bad)
		}
	}
}

// TestSpecByKey_mapsAPIJSONKeys asserts the API-key lookup, used when
// projecting an edits.listings response onto the model.
func TestSpecByKey_mapsAPIJSONKeys(t *testing.T) {
	if s, ok := listing.SpecByKey("shortDescription"); !ok || s.Field != listing.ShortDescription {
		t.Errorf("SpecByKey(shortDescription) = %+v, %v", s, ok)
	}
	if _, ok := listing.SpecByKey("short_description"); ok {
		t.Error("SpecByKey(short_description) recognized a snake_case key, want API camelCase only")
	}
}

// TestCharCount_countsRunesNotBytes asserts char limits are measured in
// Unicode code points the way Play measures them.
func TestCharCount_countsRunesNotBytes(t *testing.T) {
	cases := map[string]int{
		"":      0,
		"hello": 5,
		"café":  4, // é is one rune, two bytes
		"日本語":   3,
	}
	for in, want := range cases {
		if got := listing.CharCount(in); got != want {
			t.Errorf("CharCount(%q) = %d, want %d", in, got, want)
		}
	}
}

// TestListing_missingVsEmpty asserts the model distinguishes an unmanaged
// field (absent from Fields) from a managed-but-empty one (present, "").
func TestListing_missingVsEmpty(t *testing.T) {
	l := listing.NewListing("en-US")
	l.Set(listing.Title, "Hello")
	l.Set(listing.ShortDescription, "") // managed clear

	if v, ok := l.Get(listing.Title); !ok || v != "Hello" {
		t.Errorf("Get(Title) = %q, %v; want Hello, true", v, ok)
	}
	if v, ok := l.Get(listing.ShortDescription); !ok || v != "" {
		t.Errorf("Get(ShortDescription) = %q, %v; want \"\", true (managed clear)", v, ok)
	}
	if _, ok := l.Get(listing.FullDescription); ok {
		t.Error("Get(FullDescription) = managed, want unmanaged (absent)")
	}
}

// TestZeroListing_setIsSafe asserts Set lazily allocates so a zero
// Listing is usable without NewListing.
func TestZeroListing_setIsSafe(t *testing.T) {
	var l listing.Listing
	l.Set(listing.Video, "https://youtu.be/x")
	if v, ok := l.Get(listing.Video); !ok || v != "https://youtu.be/x" {
		t.Errorf("zero Listing Set/Get failed: %q, %v", v, ok)
	}
	if l.Empty() {
		t.Error("Empty() = true after Set, want false")
	}
}

// TestTree_localesSorted asserts Locales returns a deterministic, sorted
// view for stable output across runs.
func TestTree_localesSorted(t *testing.T) {
	tr := listing.Tree{
		"fr-FR": listing.NewListing("fr-FR"),
		"en-US": listing.NewListing("en-US"),
		"de-DE": listing.NewListing("de-DE"),
	}
	got := tr.Locales()
	want := []string{"de-DE", "en-US", "fr-FR"}
	if len(got) != len(want) {
		t.Fatalf("Locales() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Locales()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
