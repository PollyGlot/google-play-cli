// Package listing is the leaf model shared by every metadata module: the
// codec (internal/metadata/tree), the validator
// (internal/metadata/validate), and the diff engine
// (internal/metadata/diff). It defines the four Listing fields gplay
// manages: their fastlane-style on-disk file names, their Google Play
// API JSON keys, their character limits, and whether Play requires them
// non-empty, plus the in-memory Listing/Tree model those modules read
// and write.
//
// The model encodes the ADR-0011 "missing ≠ empty" rule structurally: a
// Field present in a Listing's Fields map is *managed* (its value may be
// the empty string, which means "clear online"); a Field absent from the
// map is *not managed* ("leave the online value untouched"). This package
// has no dependencies and performs no I/O.
package listing

import (
	"sort"
	"unicode/utf8"
)

// Field identifies one of the four Listing fields gplay manages. The
// concrete set mirrors the text side of edits.listings; store images
// (edits.images) are reserved future scope (ADR-0011) and not modeled
// here.
type Field int

const (
	// Title is the store-listing title (Play limit: 30 chars, required).
	Title Field = iota
	// ShortDescription is the short promo blurb (Play limit: 80 chars).
	ShortDescription
	// FullDescription is the long description (Play limit: 4000 chars,
	// required).
	FullDescription
	// Video is the optional promo video URL (no character limit).
	Video
)

// Spec is the static metadata of one Field: its on-disk file name
// (snake_case, byte-identical to `fastlane supply` so an existing tree is
// a drop-in: CONTEXT.md "Listing"), its Google Play API JSON key
// (camelCase, the edits.listings field name), its character limit
// (0 = unlimited), and whether Play requires it non-empty.
type Spec struct {
	Field    Field
	File     string // disk file name, e.g. "title.txt"
	Key      string // API JSON key, e.g. "title"
	MaxChars int    // character limit; 0 = unlimited (video)
	Required bool   // Play rejects an empty value (title, fullDescription)
}

// specs is the canonical, ordered field table. The order (title, short,
// full, video) is the display order used by `metadata list`, the diff
// changes[] within a locale, and the validator's reporting. The char
// limits cite Google's documented Listing limits:
// https://support.google.com/googleplay/android-developer/answer/9866151
// (title 30, short description 80, full description 4000).
var specs = []Spec{
	{Field: Title, File: "title.txt", Key: "title", MaxChars: 30, Required: true},
	{Field: ShortDescription, File: "short_description.txt", Key: "shortDescription", MaxChars: 80, Required: false},
	{Field: FullDescription, File: "full_description.txt", Key: "fullDescription", MaxChars: 4000, Required: true},
	{Field: Video, File: "video.txt", Key: "video", MaxChars: 0, Required: false},
}

// Specs returns the canonical field table in display order. The returned
// slice is a copy, so callers cannot mutate the package-level table.
func Specs() []Spec {
	out := make([]Spec, len(specs))
	copy(out, specs)
	return out
}

// Fields returns the four managed Fields in canonical display order.
func Fields() []Field {
	out := make([]Field, len(specs))
	for i, s := range specs {
		out[i] = s.Field
	}
	return out
}

// SpecOf returns the Spec for f. f is always one of the four constants,
// so the lookup never fails for a valid Field; an out-of-range Field
// returns the zero Spec and false.
func SpecOf(f Field) (Spec, bool) {
	for _, s := range specs {
		if s.Field == f {
			return s, true
		}
	}
	return Spec{}, false
}

// SpecByFile maps an on-disk file name (e.g. "title.txt") to its Spec.
// The second return is false for any file the codec must ignore (a stray
// README, a changelogs/ entry, an unknown *.txt) so callers can skip
// rather than guess.
func SpecByFile(name string) (Spec, bool) {
	for _, s := range specs {
		if s.File == name {
			return s, true
		}
	}
	return Spec{}, false
}

// SpecByKey maps an API JSON key (e.g. "fullDescription") to its Spec.
// Used when projecting an edits.listings response onto the managed-field
// model.
func SpecByKey(key string) (Spec, bool) {
	for _, s := range specs {
		if s.Key == key {
			return s, true
		}
	}
	return Spec{}, false
}

// CharCount returns the Play-facing length of s: the number of Unicode
// code points, not bytes. Google's Listing limits and the diff schema's
// liveChars/localChars are both character counts, so a 4000-character
// fullDescription with multibyte runes is measured the way Play measures
// it. Centralized here so the validator and the diff engine never drift.
func CharCount(s string) int {
	return utf8.RuneCountInString(s)
}

// Listing is one locale's managed Listing fields. A Field present in
// Fields is *managed*; its value may be the empty string (a clear). A
// Field absent from Fields is *not managed*: the ADR-0011 missing ≠
// empty rule, encoded as map presence. Locale is the BCP-47-ish Play
// locale code (e.g. "en-US", "fr-FR").
type Listing struct {
	Locale string
	Fields map[Field]string
}

// NewListing returns an empty, ready-to-populate Listing for locale.
func NewListing(locale string) Listing {
	return Listing{Locale: locale, Fields: make(map[Field]string)}
}

// Set marks f as managed with value v (v may be ""). It lazily allocates
// the Fields map so a zero Listing is usable.
func (l *Listing) Set(f Field, v string) {
	if l.Fields == nil {
		l.Fields = make(map[Field]string)
	}
	l.Fields[f] = v
}

// Get returns f's value and whether f is managed (present on disk /
// online). A managed field with an empty value returns ("", true); an
// unmanaged field returns ("", false).
func (l Listing) Get(f Field) (string, bool) {
	v, ok := l.Fields[f]
	return v, ok
}

// Empty reports whether the Listing manages no fields at all.
func (l Listing) Empty() bool {
	return len(l.Fields) == 0
}

// Tree is the in-memory form of a metadata tree (or of a set of online
// Listings): one Listing per locale, keyed by locale code.
type Tree map[string]Listing

// Locales returns the tree's locale codes in lexical order, for
// deterministic iteration and output.
func (t Tree) Locales() []string {
	out := make([]string, 0, len(t))
	for loc := range t {
		out = append(out, loc)
	}
	sort.Strings(out)
	return out
}
