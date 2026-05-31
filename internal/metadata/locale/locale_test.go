package locale_test

import (
	"sort"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/metadata/locale"
)

// TestIsKnown_recognizesRealPlayLocales asserts a sample of real Google
// Play store-listing codes are recognized, exactly as Play spells them.
func TestIsKnown_recognizesRealPlayLocales(t *testing.T) {
	for _, code := range []string{
		"en-US", "en-GB", "fr-FR", "fr-CA", "de-DE",
		"es-ES", "es-419", "pt-BR", "pt-PT",
		"zh-CN", "zh-TW", "zh-HK", "ja-JP", "ko-KR",
		"ar", "hi-IN", "ru-RU", "it-IT", "nl-NL",
	} {
		if !locale.IsKnown(code) {
			t.Errorf("IsKnown(%q) = false, want true (real Play locale)", code)
		}
	}
}

// TestIsKnown_rejectsTyposAndFabrications asserts the validator's whole
// reason to exist: an underscore form, a fabricated region, and the empty
// string are all NOT known.
func TestIsKnown_rejectsTyposAndFabrications(t *testing.T) {
	for _, code := range []string{
		"en_US", // underscore instead of hyphen
		"pt-XX", // fabricated region
		"",      // empty
		"en-us", // wrong case (Play codes are case-sensitive)
		"klingon",
	} {
		if locale.IsKnown(code) {
			t.Errorf("IsKnown(%q) = true, want false", code)
		}
	}
}

// TestAll_nonEmptyAndNoDuplicates asserts All() returns a non-empty,
// sorted, duplicate-free list, and that every entry round-trips through
// IsKnown.
func TestAll_nonEmptyAndNoDuplicates(t *testing.T) {
	all := locale.All()
	if len(all) == 0 {
		t.Fatal("All() returned an empty list")
	}
	if !sort.StringsAreSorted(all) {
		t.Errorf("All() is not sorted: %v", all)
	}
	seen := make(map[string]bool, len(all))
	for _, c := range all {
		if seen[c] {
			t.Errorf("All() contains duplicate %q", c)
		}
		seen[c] = true
		if !locale.IsKnown(c) {
			t.Errorf("All() returned %q but IsKnown(%q) = false", c, c)
		}
	}
}
