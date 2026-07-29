// Package orchestrator is the side-effecting glue behind `gplay metadata
// apply`. It wires auth → the Edit lifecycle → the pure diff engine →
// listings.patch / listings.delete, applying the ADR-0011 safety model:
// --confirm gates every real write, the sync is additive unless --prune is
// set, and all locales are patched inside a single Edit committed once (so
// the store is never left half-applied). It is the metadata analogue of
// internal/releases/orchestrator.
//
// Two read paths and one write path:
//
//   - Apply(DryRun): opens a read-only Edit, fetches the live Listings,
//     computes the diff, and discards — the online preview ADR-0011 §3
//     mandates (apply reconciles, so a meaningful dry-run must read the
//     other side). Nothing is committed.
//   - Apply(Confirm): opens ONE write Edit, fetches, diffs, patches every
//     changed locale (and deletes pruned ones), and commits once. On any
//     per-locale failure the Edit auto-discards (edits.WithEdit), so 0
//     locales are published — atomicity for free. When the diff is a no-op
//     the Edit is discarded rather than committed, conserving Google's
//     daily publish quota (no empty commits).
package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/metadata/diff"
	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/metadata/validate"
	"github.com/PollyGlot/google-play-cli/internal/play/details"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/listings"
)

// Opts is the input contract for Apply.
type Opts struct {
	Package string

	// DryRun computes and returns the diff against live Play without
	// committing (a read-only Edit). It does NOT require Confirm.
	DryRun bool

	// Confirm authorizes a real publish. Required for any non-dry-run
	// Apply (ADR-0011 §4); without it Apply returns an *exit.SafetyFlagError
	// naming --confirm (exit 3). CI=true must NOT flow into this field — env
	// governs output format, never mutation.
	Confirm bool

	// Prune deletes locales live on Play but absent on disk (disk becomes
	// the source of truth). Opt-in (ADR-0011 §1); refuses to prune the
	// app's defaultLanguage.
	Prune bool

	// AllowLocale whitelists locale codes outside the embedded registry
	// (Google drift), mirroring `metadata validate --allow-locale`.
	AllowLocale []string

	// ExplicitEditID reuses an already-open explicit Edit (`gplay edits begin`)
	// instead of opening/committing a per-apply Edit (docs/DESIGN.md §4): the
	// listings are patched into the pinned Edit and left for the user to
	// `gplay edits commit`. Empty is the implicit default.
	ExplicitEditID string
}

// Result is what Apply returns. Diff is always populated (the computed
// reconciliation). Patched/Pruned are populated only on a real,
// change-bearing apply: Patched maps each written locale to the
// listings.patch response body (the per-locale --output json pass-through,
// ADR-0011 §6), Pruned lists the locales whose Listing was deleted.
type Result struct {
	Package string
	DryRun  bool
	Diff    diff.Result
	Patched map[string]json.RawMessage
	Pruned  []string
}

// confirmRequired builds the refusal for a real apply invoked without
// --confirm. It is an *exit.SafetyFlagError, so it exits 3 ("safety flag
// required", docs/DESIGN.md §9) and names the missing flag in the --output
// json error envelope's requires[] (ADR-0017 / ADR-0023). It points the
// operator at --dry-run, reusing gplay's existing --confirm meaning
// (ADR-0002 / ADR-0011 §4).
func confirmRequired(prune bool) error {
	if prune {
		return exit.SafetyFlag("confirm", "metadata apply --prune publishes to live production and deletes online locales; pass --confirm to proceed (preview first with --dry-run)")
	}
	return exit.SafetyFlag("confirm", "metadata apply publishes Listings live to all users immediately; pass --confirm to proceed (preview first with --dry-run)")
}

// ValidationError signals that the local tree fails an offline lint that
// must block a publish (a char-limit overflow, an unknown locale, or a
// required field cleared). It maps to exit 20. A required field that is
// merely *missing* is NOT blocking here — apply is additive (ADR-0011 §1),
// so an omitted field just leaves the online value untouched; that case is
// filtered out before this error is built.
type ValidationError struct{ Problems []validate.Problem }

func (e *ValidationError) Error() string {
	lines := make([]string, 0, len(e.Problems)+1)
	lines = append(lines, fmt.Sprintf("metadata validation failed (%d problem(s)):", len(e.Problems)))
	for _, p := range e.Problems {
		lines = append(lines, "  - "+p.Message)
	}
	return strings.Join(lines, "\n")
}

func (e *ValidationError) ExitCode() int { return 20 }

// PruneDefaultLanguageError signals that --prune would delete the app's
// defaultLanguage Listing, which Play requires to exist. It maps to exit 2
// (a guardrail refusal, like EmptyTreePrune — no safety flag resolves it, so
// it is not the exit-3 case) and names the locale so the operator can add it
// to the tree or drop --prune.
type PruneDefaultLanguageError struct{ Locale string }

func (e *PruneDefaultLanguageError) Error() string {
	return fmt.Sprintf(
		"refusing to prune the defaultLanguage Listing %q — Play requires it to exist; add %q to your metadata tree, or run apply without --prune",
		e.Locale, e.Locale)
}

func (e *PruneDefaultLanguageError) ExitCode() int { return 2 }

// EmptyTreePruneError signals --prune was requested against an empty local
// tree. Pruning an empty tree classifies EVERY online locale as a delete
// (only the defaultLanguage is otherwise spared), so it would wipe the
// app's entire Store presence — almost never the intent, and the classic
// symptom of a mis-pointed --dir (a typo'd path that still resolves to a
// readable-but-empty directory, or a dir holding only a README / files the
// codec does not recognize). It is refused before any network, in dry-run
// too, and maps to exit 2 (a guardrail refusal, like PruneDefaultLanguage).
// It stays 2 rather than 3: no safety flag resolves it — the fix is to point
// --dir somewhere else or drop --prune, not to acknowledge anything.
type EmptyTreePruneError struct{}

func (e *EmptyTreePruneError) Error() string {
	return "refusing to --prune against an empty local metadata tree: this would delete every online locale's Listing except the defaultLanguage. Check --dir points at your metadata directory (run `gplay metadata pull` to populate it), or drop --prune"
}

func (e *EmptyTreePruneError) ExitCode() int { return 2 }

// errNoChanges is an internal sentinel: returned from the write Edit's
// closure when the diff is a no-op so edits.WithEdit auto-discards instead
// of committing an empty Edit (quota conservation). It never escapes Apply.
var errNoChanges = errors.New("metadata: no changes to apply")

// Apply reconciles the local tree against live Play per opts. See the
// package doc for the dry-run vs. real-apply paths.
func Apply(ctx context.Context, hc *http.Client, local listing.Tree, opts Opts) (*Result, error) {
	// 1. Confirm gate (real writes only). Evaluated before any network so a
	// missing --confirm fails instantly, never after opening an Edit.
	if !opts.DryRun && !opts.Confirm {
		return nil, confirmRequired(opts.Prune)
	}

	// 2. Offline lint, reusing the validate engine. Required-MISSING is
	// dropped (additive apply leaves an omitted field's online value
	// untouched); char-limit, unknown-locale, and required-EMPTY (clearing
	// a required field) still block. Runs in both dry-run and real mode so
	// an invalid tree never reaches the diff.
	if blocking := blockingProblems(local, allowSet(opts.AllowLocale)); len(blocking) > 0 {
		return nil, &ValidationError{Problems: blocking}
	}

	// 3. Empty-tree prune guard. With no locale on disk, --prune would
	// classify every online locale as a delete (sparing only the
	// defaultLanguage) and wipe the app's Store presence — almost always a
	// mis-pointed --dir, never a real intent. Refuse before any network, in
	// dry-run too, so the preview also says "no" rather than rendering a
	// delete-everything plan. (Without --prune an empty tree is a legitimate
	// no-op and is left to flow through.)
	if opts.Prune && len(local) == 0 {
		return nil, &EmptyTreePruneError{}
	}

	result := &Result{Package: opts.Package, DryRun: opts.DryRun}

	if opts.DryRun {
		// Read-only: open, fetch+diff+guard, discard. Never commits.
		if err := edits.WithReadOnlyEdit(ctx, hc, opts.Package, func(editID string) error {
			d, err := plan(ctx, hc, opts, editID, local)
			if err != nil {
				return err
			}
			result.Diff = d
			return nil
		}); err != nil {
			return nil, err
		}
		return result, nil
	}

	// Real apply: ONE write Edit. Fetch+diff+guard inside it; patch/delete
	// the changes; commit once. In IMPLICIT mode a no-op diff returns
	// errNoChanges so the Edit auto-discards (no empty commit) and any
	// per-locale failure also auto-discards → 0 locales published (atomic). In
	// EXPLICIT mode (opts.ExplicitEditID set) WithEdit reuses the pinned Edit
	// and never commits or discards — the staged changes stay in the open Edit
	// for the user to `gplay edits commit`/`discard`.
	patched := make(map[string]json.RawMessage)
	var pruned []string
	err := edits.WithEdit(ctx, hc, opts.Package, edits.Options{ExplicitEditID: opts.ExplicitEditID}, func(editID string) error {
		d, err := plan(ctx, hc, opts, editID, local)
		if err != nil {
			return err
		}
		result.Diff = d
		if !d.HasChanges() {
			return errNoChanges
		}
		// Patch every changed locale (sorted for determinism), then delete
		// every pruned locale, all inside this one Edit.
		for _, loc := range changedLocales(d) {
			body := patchBody(local, loc, d)
			raw, e := listings.Patch(ctx, hc, opts.Package, editID, loc, body)
			if e != nil {
				return e
			}
			patched[loc] = raw
		}
		for _, loc := range deleteLocales(d) {
			if e := listings.Delete(ctx, hc, opts.Package, editID, loc); e != nil {
				return e
			}
			pruned = append(pruned, loc)
		}
		return nil
	})
	if err != nil && !errors.Is(err, errNoChanges) {
		return nil, err
	}
	if len(patched) > 0 {
		result.Patched = patched
	}
	result.Pruned = pruned
	return result, nil
}

// plan fetches the live Listings inside the already-open editID, computes
// the diff, and enforces the --prune defaultLanguage guard. Shared by the
// dry-run and real-apply paths so the preview and the publish reconcile
// identically.
func plan(ctx context.Context, hc *http.Client, opts Opts, editID string, local listing.Tree) (diff.Result, error) {
	apiListings, _, err := listings.List(ctx, hc, opts.Package, editID)
	if err != nil {
		return diff.Result{}, err
	}
	online := onlineTree(apiListings)
	d := diff.Compute(opts.Package, local, online, opts.Prune)

	if opts.Prune && d.Summary.Delete > 0 {
		defLang, err := details.GetDefaultLanguage(ctx, hc, opts.Package, editID)
		if err != nil {
			return diff.Result{}, err
		}
		for _, c := range d.Changes {
			if c.Op == diff.OpDelete && c.Locale == defLang {
				return diff.Result{}, &PruneDefaultLanguageError{Locale: defLang}
			}
		}
	}
	return d, nil
}

// onlineTree projects the play-layer's []listings.Listing onto a
// listing.Tree: one entry per online locale (so untouchedLocale/delete
// detection is by key presence), with each non-empty field managed. An
// empty online field is left unmanaged — there is no value to diff against.
func onlineTree(apiListings []listings.Listing) listing.Tree {
	tr := make(listing.Tree, len(apiListings))
	for _, al := range apiListings {
		l := listing.NewListing(al.Language)
		if al.Title != "" {
			l.Set(listing.Title, al.Title)
		}
		if al.ShortDescription != "" {
			l.Set(listing.ShortDescription, al.ShortDescription)
		}
		if al.FullDescription != "" {
			l.Set(listing.FullDescription, al.FullDescription)
		}
		if al.Video != "" {
			l.Set(listing.Video, al.Video)
		}
		// Key present even when every field is empty: the locale still
		// exists online, which is what prune/untouched detection keys on.
		tr[al.Language] = l
	}
	return tr
}

// blockingProblems runs the offline validator and drops required-missing
// problems, which are legitimate under apply's additive model.
func blockingProblems(local listing.Tree, allow map[string]bool) []validate.Problem {
	var out []validate.Problem
	for _, p := range validate.Validate(local, allow) {
		if p.Kind == validate.KindRequiredMissing {
			continue
		}
		out = append(out, p)
	}
	return out
}

// allowSet turns the --allow-locale slice into a set for validate.Validate.
func allowSet(locales []string) map[string]bool {
	if len(locales) == 0 {
		return nil
	}
	m := make(map[string]bool, len(locales))
	for _, l := range locales {
		m[l] = true
	}
	return m
}

// changedLocales returns the locales carrying at least one field change
// (create/update/clear), sorted, so patches run in a deterministic order.
func changedLocales(d diff.Result) []string {
	seen := make(map[string]bool)
	for _, c := range d.Changes {
		switch c.Op {
		case diff.OpCreate, diff.OpUpdate, diff.OpClear:
			seen[c.Locale] = true
		}
	}
	out := make([]string, 0, len(seen))
	for loc := range seen {
		out = append(out, loc)
	}
	sort.Strings(out)
	return out
}

// deleteLocales returns the locales to prune (op delete), sorted.
func deleteLocales(d diff.Result) []string {
	var out []string
	for _, c := range d.Changes {
		if c.Op == diff.OpDelete {
			out = append(out, c.Locale)
		}
	}
	sort.Strings(out)
	return out
}

// patchBody builds the edits.listings.patch body for one locale: a JSON
// object carrying each changed field's target value (the disk value for a
// create/update, "" for a clear) plus "language". PATCH merge semantics
// mean fields absent from the body are left untouched online — the
// missing ≠ empty rule enforced on the wire (ADR-0011 §2).
func patchBody(local listing.Tree, loc string, d diff.Result) []byte {
	m := map[string]string{"language": loc}
	ll := local[loc]
	for _, c := range d.Changes {
		if c.Locale != loc {
			continue
		}
		switch c.Op {
		case diff.OpCreate, diff.OpUpdate, diff.OpClear:
			spec, ok := listing.SpecByKey(c.Field)
			if !ok {
				continue
			}
			v, _ := ll.Get(spec.Field)
			m[c.Field] = v
		}
	}
	// map[string]string of small, known keys — Marshal cannot fail.
	body, _ := json.Marshal(m)
	return body
}
