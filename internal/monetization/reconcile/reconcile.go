// Package reconcile computes the Reconciliation plan (CONTEXT.md) of the
// Monetization catalog: the pure create/patch/delete diff between a declared
// catalog (files) and the live one, restricted to the managed fields of the
// calling slice. No I/O, no clock, no auth: the same split as
// internal/metadata/diff vs its orchestrator. Later slices widen the managed
// field set (basePlans with #368); the engine itself does not change
// (ADR-0041).
package reconcile

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
)

// Field is one managed top-level field of the resource. Normalize, when set,
// rewrites the decoded value on both sides before comparison: the hook that
// keeps server-derived output-only subfields (basePlans[].state) from
// producing phantom patches while the field itself stays declarable. It never
// affects what apply sends (the file body travels verbatim; the server ignores
// output-only fields on write).
type Field struct {
	Name      string
	Normalize func(any) any
}

// StripKeys returns a Normalize func that removes the named keys from every
// object in the value, walking objects and arrays recursively.
func StripKeys(keys ...string) func(any) any {
	drop := map[string]struct{}{}
	for _, k := range keys {
		drop[k] = struct{}{}
	}
	var walk func(any) any
	walk = func(v any) any {
		switch t := v.(type) {
		case map[string]any:
			out := make(map[string]any, len(t))
			for k, val := range t {
				if _, gone := drop[k]; gone {
					continue
				}
				out[k] = walk(val)
			}
			return out
		case []any:
			out := make([]any, len(t))
			for i, val := range t {
				out[i] = walk(val)
			}
			return out
		default:
			return v
		}
	}
	return walk
}

// Change is one planned action on one product. Fields names the changed managed
// fields (patch only): it is both the human diff summary and the exact
// updateMask the executor sends, which is what keeps unmanaged nesting out of
// reach (ADR-0041 §5).
type Change struct {
	ProductID string   `json:"productId"`
	Fields    []string `json:"fields,omitempty"`
}

// StateChange is one planned lifecycle transition (slice #369): a base plan or
// offer whose declared state: differs from live, reconciled via the dedicated
// activate/deactivate endpoints rather than a patch. Kind is "basePlan" or
// "offer"; To is the declared target ("ACTIVE" → activate, "INACTIVE" →
// deactivate). Surfaced prominently in every plan view: a state change moves
// buyer availability.
type StateChange struct {
	Kind       string `json:"kind"`
	ProductID  string `json:"productId"`
	BasePlanID string `json:"basePlanId"`
	OfferID    string `json:"offerId,omitempty"`
	From       string `json:"from"`
	To         string `json:"to"`
}

// Plan is the Reconciliation plan: what apply would (or did) do. The Offer*
// slices carry offer-level actions (their Change.ProductID holds the composite
// productId/basePlanId/offerId display key). All slices are sorted for stable
// output.
type Plan struct {
	Creates      []Change      `json:"-"`
	Patches      []Change      `json:"-"`
	Deletes      []Change      `json:"-"`
	OfferCreates []Change      `json:"-"`
	OfferPatches []Change      `json:"-"`
	OfferDeletes []Change      `json:"-"`
	StateChanges []StateChange `json:"-"`
	Unchanged    []string      `json:"-"`
}

// HasChanges reports whether the plan does anything at all.
func (p Plan) HasChanges() bool {
	return len(p.Creates)+len(p.Patches)+len(p.Deletes)+
		len(p.OfferCreates)+len(p.OfferPatches)+len(p.OfferDeletes)+
		len(p.StateChanges) > 0
}

// HasDeletes reports whether the plan is destructive: the condition that
// gates execution behind --confirm (ADR-0041 §3). Offer deletes count: they
// remove catalog state just like subscription deletes. State changes do not:
// activate/deactivate are reversible.
func (p Plan) HasDeletes() bool { return len(p.Deletes)+len(p.OfferDeletes) > 0 }

// Compute diffs the declared catalog against the live one over the managed
// fields only: local-only products become creates, live-only products become
// deletes, and a product whose managed projection differs becomes a patch
// naming exactly the differing fields.
func Compute(local, live map[string]json.RawMessage, managed []Field) (Plan, error) {
	var plan Plan
	for id, rawLocal := range local {
		rawLive, ok := live[id]
		if !ok {
			plan.Creates = append(plan.Creates, Change{ProductID: id})
			continue
		}
		changed, err := changedFields(id, rawLocal, rawLive, managed)
		if err != nil {
			return Plan{}, err
		}
		if len(changed) == 0 {
			plan.Unchanged = append(plan.Unchanged, id)
			continue
		}
		plan.Patches = append(plan.Patches, Change{ProductID: id, Fields: changed})
	}
	for id := range live {
		if _, ok := local[id]; !ok {
			plan.Deletes = append(plan.Deletes, Change{ProductID: id})
		}
	}
	sortChanges(plan.Creates)
	sortChanges(plan.Patches)
	sortChanges(plan.Deletes)
	sort.Strings(plan.Unchanged)
	return plan, nil
}

// changedFields projects both resources onto the managed fields and returns
// the ones whose values differ. A field absent on both sides is equal;
// comparison is on decoded values, so formatting differences never count.
func changedFields(id string, rawLocal, rawLive json.RawMessage, managed []Field) ([]string, error) {
	var localM, liveM map[string]any
	if err := json.Unmarshal(rawLocal, &localM); err != nil {
		return nil, fmt.Errorf("decode declared subscription %q: %w", id, err)
	}
	if err := json.Unmarshal(rawLive, &liveM); err != nil {
		return nil, fmt.Errorf("decode live subscription %q: %w", id, err)
	}
	var changed []string
	for _, f := range managed {
		lv, ov := localM[f.Name], liveM[f.Name]
		if f.Normalize != nil {
			if lv != nil {
				lv = f.Normalize(lv)
			}
			if ov != nil {
				ov = f.Normalize(ov)
			}
		}
		if !reflect.DeepEqual(lv, ov) {
			changed = append(changed, f.Name)
		}
	}
	return changed, nil
}

func sortChanges(cs []Change) {
	sort.Slice(cs, func(i, j int) bool { return cs[i].ProductID < cs[j].ProductID })
}
