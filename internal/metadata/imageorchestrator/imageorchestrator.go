// Package imageorchestrator is the side-effecting glue behind `gplay metadata
// images apply`. It wires auth → the Edit lifecycle → the pure imagediff
// engine → images.upload / deleteall / delete, applying the ADR-0011 stance
// with ADR-0013 mechanics: --confirm gates every real write, the offline
// imagevalidate runs as a fail-fast pre-check (bypassable with --no-validate),
// the sync is additive (online-only images are never deleted — that is
// --prune, #135), and every slot is reconciled inside a single Edit committed
// once. It is the image analogue of internal/metadata/orchestrator.
//
// Two read paths and one write path:
//
//   - Apply(DryRun): open a read-only Edit, fetch live per slot, compute the
//     diff, discard. Nothing is committed.
//   - Apply(Confirm): open ONE write Edit, fetch+diff, execute each slot's plan
//     (append / rewrite = deleteall+reupload), and commit once. A no-op diff
//     discards instead of committing; any per-slot failure auto-discards, so 0
//     slots are published (atomic).
package imageorchestrator

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"github.com/PollyGlot/google-play-cli/internal/metadata/imagediff"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagevalidate"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

// Opts is the input contract for Apply.
type Opts struct {
	Package string

	// DryRun computes the diff against live Play without committing (a
	// read-only Edit). It does NOT require Confirm.
	DryRun bool

	// Confirm authorizes a real publish. Required for any non-dry-run Apply;
	// without it Apply returns *ConfirmRequiredError (exit 2). CI=true must NOT
	// flow into this field.
	Confirm bool

	// Locales/Types restrict the reconciliation to a subset (the --locale /
	// --type flags). Empty means "all".
	Locales []string
	Types   []string

	// Prune deletes a managed slot's online-only images (sha256 in no local
	// file) and reconciles the slot exactly to local. Opt-in (destructive);
	// never touches an unmanaged slot (ADR-0013 §1/§3).
	Prune bool

	// NoValidate bypasses the offline imagevalidate fail-fast pre-check
	// (ADR-0013 §4 — gplay must never be permanently stricter than Play).
	NoValidate bool

	// ExplicitEditID reuses an already-open explicit Edit (`gplay edits begin`)
	// instead of opening/committing a per-apply Edit (docs/DESIGN.md §4). Empty
	// is the implicit default.
	ExplicitEditID string
}

// Result is what Apply returns. Diff is always populated. On a real apply the
// summary counters double as the applied tally.
type Result struct {
	Package string
	DryRun  bool
	Diff    imagediff.Result
}

// ConfirmRequiredError signals a real apply invoked without --confirm. It maps
// to exit 2 and points at --dry-run. Images are live-on-commit with no
// track/draft fallback, so --confirm is always required for a real apply.
type ConfirmRequiredError struct{}

func (e *ConfirmRequiredError) Error() string {
	return "metadata images apply publishes images live to the store immediately; pass --confirm to proceed (preview first with --dry-run)"
}
func (e *ConfirmRequiredError) ExitCode() int { return 2 }

// ValidationError signals the local tree fails the offline image lint that must
// block a publish. It maps to exit 20.
type ValidationError struct{ Problems []imagevalidate.Problem }

func (e *ValidationError) Error() string {
	n := len(e.Problems)
	msg := "image validation failed before apply"
	if n > 0 {
		msg += ": " + e.Problems[0].ImageType + " (" + e.Problems[0].Rule + ") " + e.Problems[0].Message
		if n > 1 {
			msg += " (+ more; run `gplay metadata images validate`)"
		}
	}
	return msg
}
func (e *ValidationError) ExitCode() int { return 20 }

// errNoChanges is an internal sentinel: returned from the write Edit's closure
// when the diff is a no-op so edits.WithEdit auto-discards instead of
// committing an empty Edit (quota conservation). It never escapes Apply.
type noChanges struct{}

func (noChanges) Error() string { return "metadata images: no changes to apply" }

var errNoChanges = noChanges{}

// Apply reconciles the local image tree against live Play per opts.
func Apply(ctx context.Context, hc *http.Client, local imagetree.Tree, opts Opts) (*Result, error) {
	// 1. Confirm gate (real writes only), before any network.
	if !opts.DryRun && !opts.Confirm {
		return nil, &ConfirmRequiredError{}
	}

	locales := filterLocales(local, opts.Locales)
	types := filterTypes(opts.Types)

	// 2. Offline lint as a fail-fast pre-check (unless bypassed). Only the
	// managed slots in scope are validated. Runs in dry-run and real mode so an
	// invalid tree never reaches the diff or a write.
	if !opts.NoValidate {
		if problems := imagevalidate.Validate(scopedTree(local, locales, types)); len(problems) > 0 {
			return nil, &ValidationError{Problems: problems}
		}
	}

	result := &Result{Package: opts.Package, DryRun: opts.DryRun}

	if opts.DryRun {
		if err := edits.WithReadOnlyEdit(ctx, hc, opts.Package, func(editID string) error {
			plans, err := planSlots(ctx, hc, opts.Package, editID, local, locales, types, opts.Prune)
			if err != nil {
				return err
			}
			result.Diff = imagediff.Aggregate(opts.Package, plans)
			return nil
		}); err != nil {
			return nil, err
		}
		return result, nil
	}

	// Real apply: ONE write Edit. Plan inside it, execute, commit once. A no-op
	// diff returns errNoChanges so the Edit auto-discards. Any per-slot failure
	// also auto-discards → 0 slots published (atomic).
	err := edits.WithEdit(ctx, hc, opts.Package, edits.Options{ExplicitEditID: opts.ExplicitEditID}, func(editID string) error {
		plans, err := planSlots(ctx, hc, opts.Package, editID, local, locales, types, opts.Prune)
		if err != nil {
			return err
		}
		result.Diff = imagediff.Aggregate(opts.Package, plans)
		if !result.Diff.HasChanges() {
			return errNoChanges
		}
		for _, p := range plans {
			if err := executePlan(ctx, hc, opts.Package, editID, p); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errNoChanges) {
		return nil, err
	}
	return result, nil
}

// planSlots fetches live images for every considered (locale, type) slot inside
// the open Edit and reconciles each against the local tree.
func planSlots(ctx context.Context, hc *http.Client, pkg, editID string, local imagetree.Tree, locales []string, types []images.Type, prune bool) ([]imagediff.Plan, error) {
	inputs := make([]imagediff.SlotInput, 0, len(locales)*len(types))
	for _, loc := range locales {
		for _, ty := range types {
			live, _, err := images.List(ctx, hc, pkg, editID, loc, ty)
			if err != nil {
				return nil, err
			}
			inputs = append(inputs, imagediff.SlotInput{
				Locale: loc, Type: ty,
				Local: local[loc][ty],
				Live:  live,
			})
		}
	}
	return imagediff.Plans(inputs, prune), nil
}

// executePlan runs one slot's plan inside the open Edit.
func executePlan(ctx context.Context, hc *http.Client, pkg, editID string, p imagediff.Plan) error {
	switch p.Kind {
	case imagediff.KindNone:
		return nil
	case imagediff.KindAppend:
		return uploadAll(ctx, hc, pkg, editID, p)
	case imagediff.KindRewrite:
		if err := images.DeleteAll(ctx, hc, pkg, editID, p.Locale, p.Type); err != nil {
			return err
		}
		return uploadAll(ctx, hc, pkg, editID, p)
	case imagediff.KindDeleteIDs:
		for _, id := range p.DeleteIDs {
			if err := images.Delete(ctx, hc, pkg, editID, p.Locale, p.Type, id); err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

func uploadAll(ctx context.Context, hc *http.Client, pkg, editID string, p imagediff.Plan) error {
	for _, data := range p.Uploads {
		if _, err := images.Upload(ctx, hc, pkg, editID, p.Locale, p.Type, data); err != nil {
			return err
		}
	}
	return nil
}

// filterLocales returns the managed local locales restricted to the --locale
// allow-list (empty = all), sorted.
func filterLocales(local imagetree.Tree, allow []string) []string {
	allowSet := toSet(allow)
	out := make([]string, 0, len(local))
	for loc := range local {
		if len(allowSet) == 0 || allowSet[loc] {
			out = append(out, loc)
		}
	}
	sort.Strings(out)
	return out
}

// filterTypes returns the 9 image types restricted to the --type allow-list
// (empty = all), in canonical order. An unknown --type value is silently
// dropped (the command validates the flag and refuses unknown types upfront).
func filterTypes(allow []string) []images.Type {
	allowSet := toSet(allow)
	out := make([]images.Type, 0, 9)
	for _, ty := range images.Types() {
		if len(allowSet) == 0 || allowSet[string(ty)] {
			out = append(out, ty)
		}
	}
	return out
}

// scopedTree builds the subset of local restricted to the considered
// locales/types — the managed slots the validator should check.
func scopedTree(local imagetree.Tree, locales []string, types []images.Type) imagetree.Tree {
	tySet := make(map[images.Type]bool, len(types))
	for _, ty := range types {
		tySet[ty] = true
	}
	out := make(imagetree.Tree)
	for _, loc := range locales {
		for ty, seq := range local[loc] {
			if !tySet[ty] || len(seq) == 0 {
				continue
			}
			if out[loc] == nil {
				out[loc] = make(map[images.Type][][]byte)
			}
			out[loc][ty] = seq
		}
	}
	return out
}

func toSet(ss []string) map[string]bool {
	if len(ss) == 0 {
		return nil
	}
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}
