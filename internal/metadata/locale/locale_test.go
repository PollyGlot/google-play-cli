package locale_test

import (
	"sort"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/metadata/locale"
)

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

func TestIsKnown_rejectsTyposAndFabrications(t *testing.T) {
	for _, code := range []string{
		"en_US",
		"pt-XX",
		"",
		"en-us",
		"klingon",
	} {
		if locale.IsKnown(code) {
			t.Errorf("IsKnown(%q) = true, want false", code)
		}
	}
}

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
