package validate_test

import (
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/metadata/validate"
)

// complete returns a valid one-locale tree: a known locale with title and
// fullDescription present, non-empty, and under their limits. Tests start
// from this and break exactly one rule.
func complete(locale string) listing.Tree {
	l := listing.NewListing(locale)
	l.Set(listing.Title, "My App")
	l.Set(listing.FullDescription, "A long enough description.")
	return listing.Tree{locale: l}
}

// kinds extracts the Kind of every problem, for set-style assertions.
func kinds(ps []validate.Problem) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Kind
	}
	return out
}

func TestValidate_validTree_noProblems(t *testing.T) {
	got := validate.Validate(complete("en-US"), nil)
	if len(got) != 0 {
		t.Errorf("Validate(valid) = %v, want no problems", got)
	}
}

func TestValidate_charLimits(t *testing.T) {
	cases := []struct {
		name      string
		field     listing.Field
		value     string
		wantField string
	}{
		{"title 31 chars", listing.Title, strings.Repeat("a", 31), "title"},
		{"short 81 chars", listing.ShortDescription, strings.Repeat("b", 81), "shortDescription"},
		{"full 4001 chars", listing.FullDescription, strings.Repeat("c", 4001), "fullDescription"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := complete("en-US")
			l := tr["en-US"]
			l.Set(tc.field, tc.value)
			tr["en-US"] = l

			got := validate.Validate(tr, nil)
			var found *validate.Problem
			for i := range got {
				if got[i].Kind == validate.KindCharLimit && got[i].Field == tc.wantField {
					found = &got[i]
				}
			}
			if found == nil {
				t.Fatalf("Validate did not flag a char-limit for %s; got %v", tc.wantField, got)
			}
			// Message must be actionable: cite the actual count and the limit.
			if !strings.Contains(found.Message, "exceeds") {
				t.Errorf("char-limit message %q not actionable (missing limit/count)", found.Message)
			}
		})
	}
}

func TestValidate_titleAtLimit_ok(t *testing.T) {
	// Exactly 30 chars is allowed (boundary: > limit, not >=).
	tr := complete("en-US")
	l := tr["en-US"]
	l.Set(listing.Title, strings.Repeat("a", 30))
	tr["en-US"] = l

	if got := validate.Validate(tr, nil); len(got) != 0 {
		t.Errorf("title at exactly 30 chars flagged: %v", got)
	}
}

func TestValidate_charLimit_countsRunesNotBytes(t *testing.T) {
	// 30 multibyte runes (é = 2 bytes each) is at the limit, not over.
	tr := complete("en-US")
	l := tr["en-US"]
	l.Set(listing.Title, strings.Repeat("é", 30))
	tr["en-US"] = l
	if got := validate.Validate(tr, nil); len(got) != 0 {
		t.Errorf("30 multibyte runes flagged (should count runes, not bytes): %v", got)
	}

	// 31 runes is over.
	l.Set(listing.Title, strings.Repeat("é", 31))
	tr["en-US"] = l
	got := validate.Validate(tr, nil)
	if len(got) != 1 || got[0].Kind != validate.KindCharLimit {
		t.Errorf("31 multibyte runes = %v, want one char-limit problem", got)
	}
}

func TestValidate_requiredEmpty(t *testing.T) {
	cases := []struct {
		name  string
		field listing.Field
		key   string
	}{
		{"empty title", listing.Title, "title"},
		{"empty full description", listing.FullDescription, "fullDescription"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := complete("en-US")
			l := tr["en-US"]
			l.Set(tc.field, "") // managed but empty
			tr["en-US"] = l

			got := validate.Validate(tr, nil)
			if len(got) != 1 || got[0].Kind != validate.KindRequiredEmpty || got[0].Field != tc.key {
				t.Fatalf("Validate(empty %s) = %v, want one required-empty for %s", tc.key, got, tc.key)
			}
			if !strings.Contains(got[0].Message, "empty") {
				t.Errorf("required-empty message %q not actionable", got[0].Message)
			}
		})
	}
}

func TestValidate_requiredMissing(t *testing.T) {
	cases := []struct {
		name    string
		present listing.Field // the one required field we DO set
		key     string        // the missing one we expect flagged
	}{
		{"missing title", listing.FullDescription, "title"},
		{"missing full description", listing.Title, "fullDescription"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := listing.NewListing("en-US")
			l.Set(tc.present, "value here")
			tr := listing.Tree{"en-US": l}

			got := validate.Validate(tr, nil)
			if len(got) != 1 || got[0].Kind != validate.KindRequiredMissing || got[0].Field != tc.key {
				t.Fatalf("Validate(missing %s) = %v, want one required-missing for %s", tc.key, got, tc.key)
			}
			if !strings.Contains(got[0].Message, "required") {
				t.Errorf("required-missing message %q not actionable", got[0].Message)
			}
		})
	}
}

func TestValidate_unknownLocale(t *testing.T) {
	// A complete listing under a fabricated locale: only the locale is wrong.
	tr := complete("pt-XX")
	got := validate.Validate(tr, nil)
	if len(got) != 1 || got[0].Kind != validate.KindUnknownLocale {
		t.Fatalf("Validate(unknown locale) = %v, want one unknown-locale", got)
	}
	if got[0].Field != "" {
		t.Errorf("unknown-locale Field = %q, want \"\" (whole-locale problem)", got[0].Field)
	}
	if !strings.Contains(got[0].Message, "--allow-locale") {
		t.Errorf("unknown-locale message %q should point at --allow-locale", got[0].Message)
	}
}

func TestValidate_underscoreLocaleIsUnknown(t *testing.T) {
	tr := complete("en_US") // underscore typo
	got := validate.Validate(tr, nil)
	if len(got) != 1 || got[0].Kind != validate.KindUnknownLocale {
		t.Fatalf("Validate(en_US) = %v, want one unknown-locale", got)
	}
}

func TestValidate_allowLocaleWhitelists(t *testing.T) {
	tr := complete("pt-XX")
	allow := map[string]bool{"pt-XX": true}
	if got := validate.Validate(tr, allow); len(got) != 0 {
		t.Errorf("Validate(unknown locale, allow=pt-XX) = %v, want no problems", got)
	}
}

func TestValidate_deterministicOrder(t *testing.T) {
	// Two broken locales; problems must come out grouped by locale in
	// lexical order. "a-bad" sorts before "z-bad".
	mk := func(loc string) listing.Listing {
		l := listing.NewListing(loc)
		l.Set(listing.Title, strings.Repeat("x", 99)) // char-limit
		l.Set(listing.FullDescription, "ok desc")
		return l
	}
	tr := listing.Tree{"z-bad": mk("z-bad"), "a-bad": mk("a-bad")}
	got := validate.Validate(tr, nil)

	// Both locales are unknown AND have an over-limit title: each yields
	// unknown-locale then char-limit, a-bad before z-bad.
	wantOrder := []string{"a-bad", "a-bad", "z-bad", "z-bad"}
	if len(got) != len(wantOrder) {
		t.Fatalf("got %d problems %v, want %d", len(got), kinds(got), len(wantOrder))
	}
	for i, want := range wantOrder {
		if got[i].Locale != want {
			t.Errorf("problem %d locale = %q, want %q (order: %v)", i, got[i].Locale, want, got)
		}
	}
	// Within each locale, unknown-locale precedes char-limit.
	if got[0].Kind != validate.KindUnknownLocale || got[1].Kind != validate.KindCharLimit {
		t.Errorf("within-locale order = %v, want unknown-locale then char-limit", kinds(got))
	}
}
