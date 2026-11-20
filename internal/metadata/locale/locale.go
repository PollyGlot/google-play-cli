package locale

import "sort"

var knownLocales = []string{
	"en-US", "en-GB", "en-AU", "en-CA", "en-IN", "en-SG", "en-ZA",
	"fr-FR", "fr-CA", "fr-CH",
	"de-DE", "de-AT", "de-CH",
	"es-ES", "es-419", "es-US",
	"pt-BR", "pt-PT",
	"zh-CN", "zh-TW", "zh-HK",
	"ja-JP", "ko-KR",
	"ar", "he-IL", "fa", "ur",
	"hi-IN", "bn-BD", "ta-IN", "te-IN", "mr-IN", "ml-IN", "kn-IN",
	"gu-IN", "pa", "ne-NP", "si-LK",
	"ru-RU", "uk", "be",
	"it-IT", "nl-NL", "ca", "gl-ES", "eu-ES", "rm",
	"sv-SE", "da-DK", "fi-FI", "nb-NO", "is-IS",
	"pl-PL", "cs-CZ", "sk", "sl", "hr", "sr", "bg", "ro", "hu-HU",
	"el-GR", "mk-MK", "sq", "lt", "lv", "et",
	"tr-TR", "az-AZ", "kk", "ky-KG", "uz", "hy-AM", "ka-GE", "mn-MN",
	"th", "vi", "id", "ms", "fil", "km-KH", "my-MM", "lo-LA",
	"af", "zu", "am", "sw",
}

var known = func() map[string]bool {
	m := make(map[string]bool, len(knownLocales))
	for _, c := range knownLocales {
		m[c] = true
	}
	return m
}()

func IsKnown(code string) bool {
	if code == "" {
		return false
	}
	return known[code]
}

func All() []string {
	out := make([]string, 0, len(known))
	for c := range known {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
