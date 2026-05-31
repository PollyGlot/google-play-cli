// Package diff is the pure reconciliation engine at the heart of
// `gplay metadata apply`. Given the local Metadata tree and the Listings
// live on Play — both as listing.Tree values — it computes the per-locale,
// per-field delta: what would be created, updated, cleared, left
// unchanged, or (with --prune) deleted, plus which online-only locales are
// left untouched. It is the single source of truth shared by
// `apply --dry-run` (which renders the delta) and `apply` (which executes
// it), so the preview and the publish can never disagree.
//
// The engine encodes the ADR-0011 sync model exactly:
//
//   - Additive. Only locales/fields present on disk are considered for an
//     upsert. A locale live on Play but absent on disk is reported as
//     untouchedLocale (or, under --prune, delete) — never silently
//     dropped.
//   - Missing ≠ empty. Inside a locale on disk, a field that is *not
//     managed* (absent from listing.Listing.Fields) is ignored entirely —
//     it never appears in the diff. A field that is *managed but empty*
//     ("" value) is a clear.
//
// It is pure: no I/O, no auth, no clock. The orchestrator projects the
// play-layer's []listings.Listing onto an online listing.Tree (one entry
// per online locale, each non-empty field managed) and hands both trees
// here.
package diff

import (
	"sort"

	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
)

// Op is the classification of a single field change (or, for a locale-level
// record, a whole-locale action). The five field/locale ops below are the
// ADR-0011 set; Delete is added for --prune.
type Op string

const (
	// OpCreate: the field is non-empty on disk and has no value online.
	OpCreate Op = "create"
	// OpUpdate: the field is non-empty on disk and differs from online.
	OpUpdate Op = "update"
	// OpClear: the field is managed-empty on disk and has a value online
	// (the empty file means "clear it"). Requires PATCH semantics.
	OpClear Op = "clear"
	// OpUnchanged: the managed field already matches online (including
	// clearing an already-absent field). Counted, but not listed in
	// Changes — a diff shows what differs.
	OpUnchanged Op = "unchanged"
	// OpUntouchedLocale: the locale is live on Play but absent on disk.
	// Left intact (additive default), reported so the operator knows it
	// exists.
	OpUntouchedLocale Op = "untouchedLocale"
	// OpDelete: the locale is live on Play, absent on disk, and --prune was
	// requested — its whole Listing would be removed.
	OpDelete Op = "delete"
)

// untouchedReason / prunedReason are the human-facing reasons attached to
// the two locale-level records, so a --output json consumer (and the table
// view) sees why a locale appears with no field.
const (
	untouchedReason = "on Play, absent locally"
	prunedReason    = "pruned: on Play, absent locally"
)

// Change is one record in the diff. For a field-level op (create / update /
// clear) Field is the API JSON key and LiveChars/LocalChars carry the
// online/local character counts. For a locale-level op (untouchedLocale /
// delete) Field is empty, the char counts are nil, and Reason explains it.
//
// LiveChars/LocalChars are pointers so a genuine zero (a create has
// LiveChars 0; a clear has LocalChars 0) is emitted, while a locale-level
// record omits them entirely.
type Change struct {
	Locale     string `json:"locale"`
	Field      string `json:"field,omitempty"`
	Op         Op     `json:"op"`
	LiveChars  *int   `json:"liveChars,omitempty"`
	LocalChars *int   `json:"localChars,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// Summary is the per-op tally. The five ADR-0011 counters plus Delete
// (only ever non-zero under --prune). Flat by design (ADR-0011 §6): a CI
// gate is one jq line — `.summary.create + .summary.update > 0`.
type Summary struct {
	Create           int `json:"create"`
	Update           int `json:"update"`
	Clear            int `json:"clear"`
	Unchanged        int `json:"unchanged"`
	UntouchedLocales int `json:"untouchedLocales"`
	Delete           int `json:"delete"`
}

// Result is the whole diff: the package, the actionable change list, and
// the summary tally. Changes lists every actionable field op (create /
// update / clear), every untouchedLocale, and — under --prune — every
// delete. Unchanged fields are counted in Summary.Unchanged but NOT listed
// (a diff shows what differs, not what stays); this keeps the output
// bounded by the real delta even for an app with many fully-synced
// locales.
type Result struct {
	Package string   `json:"package"`
	Changes []Change `json:"changes"`
	Summary Summary  `json:"summary"`
}

// HasChanges reports whether the diff would alter Play: any create, update,
// clear, or delete. untouchedLocale and unchanged do not count.
func (r Result) HasChanges() bool {
	s := r.Summary
	return s.Create+s.Update+s.Clear+s.Delete > 0
}

// intPtr returns a pointer to n (the char-count fields are *int so a
// genuine 0 is distinguishable from "omitted").
func intPtr(n int) *int { return &n }

// Compute reconciles the local tree against the online tree and returns the
// classified diff. online must carry one entry per locale live on Play (so
// untouchedLocale/delete detection is by key presence), each with its
// non-empty fields managed. When prune is true, an online-only locale is
// classified OpDelete (and counted in Summary.Delete); otherwise it is
// OpUntouchedLocale (counted in Summary.UntouchedLocales).
//
// Iteration is deterministic: locales are visited in lexical order and,
// within a locale, fields in canonical order (listing.Fields).
func Compute(pkg string, local, online listing.Tree, prune bool) Result {
	res := Result{Package: pkg, Changes: []Change{}}

	// Locales present on disk: classify each managed field.
	for _, loc := range local.Locales() {
		ll := local[loc]
		ol, onlineHasLocale := online[loc]
		for _, f := range listing.Fields() {
			localVal, managed := ll.Get(f)
			if !managed {
				// Unmanaged field — additive: leave the online value alone,
				// never even mention it.
				continue
			}
			spec, ok := listing.SpecOf(f)
			if !ok {
				continue
			}

			var onlineVal string
			var onlineHasField bool
			if onlineHasLocale {
				onlineVal, onlineHasField = ol.Get(f)
			}
			localChars := listing.CharCount(localVal)
			liveChars := listing.CharCount(onlineVal) // 0 when no online value

			switch {
			case localVal != "":
				// Local has content.
				switch {
				case !onlineHasField:
					res.Summary.Create++
					res.Changes = append(res.Changes, Change{
						Locale: loc, Field: spec.Key, Op: OpCreate,
						LiveChars: intPtr(0), LocalChars: intPtr(localChars),
					})
				case onlineVal != localVal:
					res.Summary.Update++
					res.Changes = append(res.Changes, Change{
						Locale: loc, Field: spec.Key, Op: OpUpdate,
						LiveChars: intPtr(liveChars), LocalChars: intPtr(localChars),
					})
				default:
					res.Summary.Unchanged++ // identical — counted, not listed
				}
			default:
				// Local is managed-empty: a clear intent.
				if onlineHasField {
					res.Summary.Clear++
					res.Changes = append(res.Changes, Change{
						Locale: loc, Field: spec.Key, Op: OpClear,
						LiveChars: intPtr(liveChars), LocalChars: intPtr(0),
					})
				} else {
					// Clearing a field that is already absent online: no-op.
					res.Summary.Unchanged++
				}
			}
		}
	}

	// Locales live on Play but absent on disk: untouched (additive) or, with
	// --prune, deleted. Sorted for determinism.
	onlineOnly := make([]string, 0)
	for _, loc := range online.Locales() {
		if _, onDisk := local[loc]; !onDisk {
			onlineOnly = append(onlineOnly, loc)
		}
	}
	sort.Strings(onlineOnly)
	for _, loc := range onlineOnly {
		if prune {
			res.Summary.Delete++
			res.Changes = append(res.Changes, Change{
				Locale: loc, Op: OpDelete, Reason: prunedReason,
			})
		} else {
			res.Summary.UntouchedLocales++
			res.Changes = append(res.Changes, Change{
				Locale: loc, Op: OpUntouchedLocale, Reason: untouchedReason,
			})
		}
	}

	return res
}
