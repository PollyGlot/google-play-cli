// Package validate is the pure, offline linter for a Metadata tree. It
// takes the typed listing.Tree (already read off disk by
// internal/metadata/tree) and reports every store-listing rule violation
// it can check WITHOUT touching Play: unknown locales, character-limit
// overflows, and required fields that are empty or missing.
//
// It has no I/O, no auth, and no HTTP — every input arrives as arguments,
// every output is a deterministic []Problem. The command layer
// (commands/metadata/validate) maps a non-empty result to exit 20
// (client-side validation, docs/DESIGN.md §9).
//
// Relationship to `metadata apply`. `apply` is ADDITIVE: it upserts only
// the locales/fields on disk and leaves everything else on Play
// untouched (ADR-0011 §1), and inside a present locale a *missing* field
// file means "leave the online value alone" (ADR-0011 §2). `validate` is
// deliberately STRICTER: it is a pre-publication lint that treats every
// locale present in the tree as one that must be a COMPLETE Listing —
// title and fullDescription present and non-empty — because Play rejects
// a Listing missing either (ADR-0011 §2: "title/fullDescription are
// required non-empty by Play, so an empty file for those is a validation
// error, not a clear"). So a locale dir that omits title.txt is fine for
// an additive `apply` (it patches only what is there) but is a
// validation error here: shipping that locale to Play would fail. This
// is the intended reading of the issue's AC "title/full_description vide
// ou manquant → exit 20", and it means a tree that `apply` would accept
// can still fail `validate` — `validate` answers "is each locale in this
// tree publishable on its own?", not "will apply's PATCH succeed?".
package validate

import (
	"fmt"

	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/metadata/locale"
)

// Problem kinds. Stable strings so a machine-readable consumer (or a test)
// can switch on Kind rather than parse Message.
const (
	// KindUnknownLocale: the locale code is not a known Play store locale
	// and was not whitelisted via --allow-locale.
	KindUnknownLocale = "unknown-locale"
	// KindCharLimit: a managed field exceeds its Play character limit.
	KindCharLimit = "char-limit"
	// KindRequiredEmpty: a required field (title, fullDescription) is
	// managed but empty.
	KindRequiredEmpty = "required-empty"
	// KindRequiredMissing: a required field (title, fullDescription) is
	// absent from the locale entirely.
	KindRequiredMissing = "required-missing"
)

// Problem is one rule violation found in the tree. Field is the API JSON
// key of the offending field (e.g. "title", "fullDescription"), or ""
// when the problem is about the whole locale (an unknown locale). Message
// is operator-facing and actionable: it names the limit and the actual
// count, or names the override flag.
type Problem struct {
	Locale  string
	Field   string
	Kind    string
	Message string
}

// Validate lints tr offline and returns every violation it finds. allow
// is the set of locale codes whitelisted via --allow-locale (a non-nil
// map whose true keys override the unknown-locale check); nil is treated
// as "nothing whitelisted".
//
// Output order is deterministic: problems are grouped by locale in
// lexical order (listing.Tree.Locales), and within a locale the
// unknown-locale problem (if any) comes first, then per-field problems in
// the canonical field order (listing.Fields: title, short, full, video).
func Validate(tr listing.Tree, allow map[string]bool) []Problem {
	var problems []Problem
	for _, loc := range tr.Locales() {
		l := tr[loc]

		// 1. Locale known? An unknown, non-whitelisted locale is reported
		// once for the whole locale (Field=""). We still go on to check
		// its fields so the operator sees every issue in one run.
		if !locale.IsKnown(loc) && !allow[loc] {
			problems = append(problems, Problem{
				Locale: loc,
				Kind:   KindUnknownLocale,
				Message: fmt.Sprintf(
					"locale %q is not a known Google Play store locale; use --allow-locale %q to override if Google added it",
					loc, loc),
			})
		}

		// 2 & 3. Per-field checks, in canonical field order.
		for _, f := range listing.Fields() {
			spec, ok := listing.SpecOf(f)
			if !ok {
				continue
			}
			value, managed := l.Get(f)

			// 3. Required fields (title, fullDescription) must be present
			// AND non-empty in every locale of the tree (see package doc
			// for why validate is stricter than additive apply).
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
					continue // nothing to char-count for an absent field
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
					continue // an empty value cannot exceed any limit
				}
			}

			// 2. Character limits, for any managed field with a finite
			// limit (MaxChars > 0; video is unlimited). An unmanaged
			// non-required field is simply skipped.
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
