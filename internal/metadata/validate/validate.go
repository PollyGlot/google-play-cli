package validate

import (
	"fmt"

	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/metadata/locale"
)

const (
	KindUnknownLocale   = "unknown-locale"
	KindCharLimit       = "char-limit"
	KindRequiredEmpty   = "required-empty"
	KindRequiredMissing = "required-missing"
)

type Problem struct {
	Locale  string
	Field   string
	Kind    string
	Message string
}

func Validate(tr listing.Tree, allow map[string]bool) []Problem {
	var problems []Problem
	for _, loc := range tr.Locales() {
		l := tr[loc]

		if !locale.IsKnown(loc) && !allow[loc] {
			problems = append(problems, Problem{
				Locale: loc,
				Kind:   KindUnknownLocale,
				Message: fmt.Sprintf(
					"locale %q is not a known Google Play store locale; use --allow-locale %q to override if Google added it",
					loc, loc),
			})
		}

		for _, f := range listing.Fields() {
			spec, ok := listing.SpecOf(f)
			if !ok {
				continue
			}
			value, managed := l.Get(f)

			if spec.Required {
				if !managed {
					problems = append(problems, Problem{
						Locale: loc,
						Field:  spec.Key,
						Kind:   KindRequiredMissing,
						Message: fmt.Sprintf(
							"%s is required but missing in %s",
							spec.Key, loc),
					})
					continue
				}
				if value == "" {
					problems = append(problems, Problem{
						Locale: loc,
						Field:  spec.Key,
						Kind:   KindRequiredEmpty,
						Message: fmt.Sprintf(
							"%s in %s must not be empty (an empty file is not a clear for a required field)",
							spec.Key, loc),
					})
					continue
				}
			}

			if !managed {
				continue
			}
			if spec.MaxChars > 0 {
				if n := listing.CharCount(value); n > spec.MaxChars {
					problems = append(problems, Problem{
						Locale: loc,
						Field:  spec.Key,
						Kind:   KindCharLimit,
						Message: fmt.Sprintf(
							"%s in %s is %d chars, exceeds the %d limit",
							spec.Key, loc, n, spec.MaxChars),
					})
				}
			}
		}
	}
	return problems
}
