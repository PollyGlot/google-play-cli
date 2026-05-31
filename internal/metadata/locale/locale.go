// Package locale is the embedded registry of Google Play store-listing
// locale codes gplay's `metadata validate` lints against. It is the
// locale analogue of commands/tracks/list's StandardTracks: a documented
// Go constant baked into the binary so the offline validator needs no
// network and no credentials to tell `en-US` (valid) from `en_US`
// (underscore typo) or `pt-XX` (non-existent).
//
// Source: Google Play Console help, "Supported languages and locales"
// (https://support.google.com/googleplay/android-developer/answer/9844778).
// The list below transcribes the Play store-listing locale codes from
// that page — the exact `language-REGION` strings the edits.listings API
// accepts (e.g. `en-US`, `es-419`, `pt-BR`, `zh-CN`). The codes are
// CASE-SENSITIVE on Play's side (`en-US`, never `en-us`), so IsKnown
// matches exactly.
//
// This list is a point-in-time snapshot. Google occasionally adds a new
// store locale before a gplay release can ship an updated table; when
// that happens the validator would wrongly flag the new (legitimate)
// locale as unknown. The designed escape hatch is the `--allow-locale
// xx-YY` flag on `gplay metadata validate`, which whitelists a code
// without a gplay release. The goal of this table is to catch typos and
// fabricated codes, not to be exhaustive to the last locale: when in
// doubt, a code is included.
package locale

import "sort"

// knownLocales is the embedded set of Google Play store-listing locale
// codes. Transcribed from Google Play Console help, "Supported languages
// and locales"
// (https://support.google.com/googleplay/android-developer/answer/9844778).
// Codes are case-sensitive (Play uses `en-US`, not `en-us`). Listed
// roughly by language family for readability; All() returns them sorted.
var knownLocales = []string{
	// English
	"en-US", "en-GB", "en-AU", "en-CA", "en-IN", "en-SG", "en-ZA",
	// French
	"fr-FR", "fr-CA", "fr-CH",
	// German
	"de-DE", "de-AT", "de-CH",
	// Spanish
	"es-ES", "es-419", "es-US",
	// Portuguese
	"pt-BR", "pt-PT",
	// Chinese
	"zh-CN", "zh-TW", "zh-HK",
	// Japanese / Korean
	"ja-JP", "ko-KR",
	// Arabic / Hebrew / Persian / Urdu
	"ar", "he-IL", "fa", "ur",
	// South Asian
	"hi-IN", "bn-BD", "ta-IN", "te-IN", "mr-IN", "ml-IN", "kn-IN",
	"gu-IN", "pa", "ne-NP", "si-LK",
	// Russian / Ukrainian / Belarusian
	"ru-RU", "uk", "be",
	// Western & Southern Europe
	"it-IT", "nl-NL", "ca", "gl-ES", "eu-ES", "rm",
	// Nordics
	"sv-SE", "da-DK", "fi-FI", "nb-NO", "is-IS",
	// Central & Eastern Europe
	"pl-PL", "cs-CZ", "sk", "sl", "hr", "sr", "bg", "ro", "hu-HU",
	"el-GR", "mk-MK", "sq", "lt", "lv", "et",
	// Turkic & Caucasus & Central Asia
	"tr-TR", "az-AZ", "kk", "ky-KG", "uz", "hy-AM", "ka-GE", "mn-MN",
	// Southeast Asia
	"th", "vi", "id", "ms", "fil", "km-KH", "my-MM", "lo-LA",
	// African
	"af", "zu", "am", "sw",
}

// known is the lookup set built once from knownLocales, plus the
// non-region "values" defaults Play also accepts where present in the
// help page. Exact, case-sensitive keys.
var known = func() map[string]bool {
	m := make(map[string]bool, len(knownLocales))
	for _, c := range knownLocales {
		m[c] = true
	}
	return m
}()

// IsKnown reports whether code is a Google Play store-listing locale this
// build recognizes. The match is EXACT and case-sensitive: Play's codes
// are case-sensitive (`en-US`, not `en-us`), and an underscore form
// (`en_US`) or a fabricated region (`pt-XX`) is intentionally rejected so
// the validator catches it. An empty string is never known.
func IsKnown(code string) bool {
	if code == "" {
		return false
	}
	return known[code]
}

// All returns the recognized locale codes as a sorted copy, for help text
// and tests. The returned slice is the caller's to mutate.
func All() []string {
	out := make([]string, 0, len(known))
	for c := range known {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
