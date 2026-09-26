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

// Change is one planned action on one catalog resource. Fields names the
// changed managed fields (patch only): it is both the human diff summary and
// the exact updateMask the executor sends, which is what keeps unmanaged
// nesting out of reach (ADR-0041 §5).
//
// The identity is typed, never a composite string to split again: ProductID
// alone for a product; ParentID (the base plan or purchase option, the middle
// level whose name depends on the catalog) for a base-plan delete; ParentID
// and OfferID for an offer.
type Change struct {
	ProductID string   `json:"productId"`
	ParentID  string   `json:"parentId,omitempty"`
	OfferID   string   `json:"offerId,omitempty"`
	Fields    []string `json:"fields,omitempty"`
}

// Key renders the change's identity as its productId[/parentId[/offerId]]
// display key: the form the plan prints and the order it sorts by.
func (c Change) Key() string {
	return Key{ProductID: c.ProductID, ParentID: c.ParentID, OfferID: c.OfferID}.String()
}

// Key is the typed identity of a nested catalog resource (a base plan, a
// purchase option or an offer) under its product.
type Key struct {
	ProductID, ParentID, OfferID string
}

// String joins the levels with "/": productId, productId/parentId or
// productId/parentId/offerId.
func (k Key) String() string {
	s := k.ProductID
	if k.ParentID != "" || k.OfferID != "" {
		s += "/" + k.ParentID
	}
	if k.OfferID != "" {
		s += "/" + k.OfferID
	}
	return s
}

// StateChange is one planned lifecycle transition (slice #369): a base plan or
// offer whose declared state: differs from live, reconciled via the dedicated
// activate/deactivate endpoints rather than a patch. Kind is "basePlan" or
// "offer"; To is the declared target ("ACTIVE" → activate, "INACTIVE" →
// deactivate). Surfaced prominently in every plan view: a state change moves
// buyer availability. The one-time-product catalog (slice #541) reuses it
// with Kind "purchaseOption"/"offer" and the purchase option in
// PurchaseOptionID instead of BasePlanID: the two families share the plan
// shape, only the middle level of the identity differs. To may also be
// "CANCELLED" there (the pre-order :cancel verb, irreversible).
type StateChange struct {
	Kind             string `json:"kind"`
	ProductID        string `json:"productId"`
	BasePlanID       string `json:"basePlanId,omitempty"`
	PurchaseOptionID string `json:"purchaseOptionId,omitempty"`
	OfferID          string `json:"offerId,omitempty"`
	From             string `json:"from"`
	To               string `json:"to"`
}

// Plan is the Reconciliation plan: what apply would (or did) do. The Offer*
// slices carry offer-level actions (typed productId, parentId and offerId);
// BasePlanDeletes carries productId and parentId. All slices are sorted by
// display key for stable output; Entries flattens them in plan-view order.
//
// Base plans have deletes but no creates or patches of their own: their config
// rides the parent subscription patch, which never removes a plan its body
// omits, so the removal is the one base-plan action that needs its own
// endpoint (slice #542).
type Plan struct {
	Creates         []Change      `json:"-"`
	Patches         []Change      `json:"-"`
	Deletes         []Change      `json:"-"`
	BasePlanDeletes []Change      `json:"-"`
	OfferCreates    []Change      `json:"-"`
	OfferPatches    []Change      `json:"-"`
	OfferDeletes    []Change      `json:"-"`
	StateChanges    []StateChange `json:"-"`
	Unchanged       []string      `json:"-"`
}

// Kinds of a plan entry below the product level.
const (
	KindOffer          = "offer"
	KindBasePlan       = "basePlan"
	KindPurchaseOption = "purchaseOption"
)

// Entry is one typed plan record: every action of a Plan in one shape, so a
// renderer walks a single list instead of re-deriving identities per slice.
// Op is the plan's verb (create, patch, delete, activate, deactivate, cancel);
// Kind is "" for a product, else KindOffer, KindBasePlan or
// KindPurchaseOption. From/To are set on state changes only.
type Entry struct {
	Op        string
	Kind      string
	ProductID string
	ParentID  string
	OfferID   string
	Fields    []string
	From, To  string
}

// Target is the entry's productId[/parentId[/offerId]] display key.
func (e Entry) Target() string {
	return Key{ProductID: e.ProductID, ParentID: e.ParentID, OfferID: e.OfferID}.String()
}

// Entries flattens the plan in the order every plan view lists it: grow
// (creates, patches, offer creates and patches), move state, then shrink
// (offer deletes, base-plan deletes, product deletes), the order apply runs.
func (p Plan) Entries() []Entry {
	out := make([]Entry, 0, len(p.Creates)+len(p.Patches)+len(p.Deletes)+len(p.BasePlanDeletes)+
		len(p.OfferCreates)+len(p.OfferPatches)+len(p.OfferDeletes)+len(p.StateChanges))
	add := func(op, kind string, cs []Change) {
		for _, c := range cs {
			out = append(out, Entry{Op: op, Kind: kind, ProductID: c.ProductID, ParentID: c.ParentID, OfferID: c.OfferID, Fields: c.Fields})
		}
	}
	add("create", "", p.Creates)
	add("patch", "", p.Patches)
	add("create", KindOffer, p.OfferCreates)
	add("patch", KindOffer, p.OfferPatches)
	for _, s := range p.StateChanges {
		out = append(out, Entry{Op: s.Op(), Kind: s.Kind, ProductID: s.ProductID, ParentID: s.Parent(), OfferID: s.OfferID, From: s.From, To: s.To})
	}
	add("delete", KindOffer, p.OfferDeletes)
	add("delete", KindBasePlan, p.BasePlanDeletes)
	add("delete", "", p.Deletes)
	return out
}

// Op names the verb a state change rides: cancel, deactivate or activate.
func (s StateChange) Op() string {
	switch s.To {
	case "CANCELLED":
		return "cancel"
	case "INACTIVE":
		return "deactivate"
	default:
		return "activate"
	}
}

// Parent is the middle level of the identity, whichever catalog it belongs to.
func (s StateChange) Parent() string {
	if s.BasePlanID != "" {
		return s.BasePlanID
	}
	return s.PurchaseOptionID
}

// Target is the productId/parentId[/offerId] display key of the resource the
// state change moves.
func (s StateChange) Target() string {
	return Key{ProductID: s.ProductID, ParentID: s.Parent(), OfferID: s.OfferID}.String()
}

// HasChanges reports whether the plan does anything at all.
func (p Plan) HasChanges() bool {
	return len(p.Creates)+len(p.Patches)+len(p.Deletes)+len(p.BasePlanDeletes)+
		len(p.OfferCreates)+len(p.OfferPatches)+len(p.OfferDeletes)+
		len(p.StateChanges) > 0
}

// HasDeletes reports whether the plan is destructive: the condition that
// gates execution behind --confirm (ADR-0041 §3). Base-plan and offer deletes
// count: they remove catalog state just like subscription deletes. State
// changes do not: activate/deactivate are reversible.
func (p Plan) HasDeletes() bool {
	return len(p.Deletes)+len(p.BasePlanDeletes)+len(p.OfferDeletes) > 0
}

// BasePlanDeletes computes the base-plan shrink set (slice #542): for every
// product declared locally and present live, each live base plan the file no
// longer declares becomes a delete keyed productId/basePlanId. Products the
// plan deletes outright are skipped (the parent delete takes their plans), as
// are products being created (nothing live to remove). Sorted for stable
// output.
func BasePlanDeletes(local, live map[string]json.RawMessage) ([]Change, error) {
	var out []Change
	for id, rawLocal := range local {
		rawLive, ok := live[id]
		if !ok {
			continue
		}
		localIDs, err := basePlanIDs(id, "declared", rawLocal)
		if err != nil {
			return nil, err
		}
		liveIDs, err := basePlanIDs(id, "live", rawLive)
		if err != nil {
			return nil, err
		}
		for bp := range liveIDs {
			if _, declared := localIDs[bp]; !declared {
				out = append(out, Change{ProductID: id, ParentID: bp})
			}
		}
	}
	sortChanges(out)
	return out, nil
}

// basePlanIDs returns the set of basePlanId values of one subscription
// resource.
func basePlanIDs(id, side string, raw json.RawMessage) (map[string]struct{}, error) {
	var sub struct {
		BasePlans []struct {
			BasePlanID string `json:"basePlanId"`
		} `json:"basePlans"`
	}
	if err := json.Unmarshal(raw, &sub); err != nil {
		return nil, fmt.Errorf("decode %s subscription %q: %w", side, id, err)
	}
	ids := make(map[string]struct{}, len(sub.BasePlans))
	for _, bp := range sub.BasePlans {
		if bp.BasePlanID == "" {
			return nil, fmt.Errorf("%s subscription %q carries a base plan without a basePlanId: refusing a plan entry that cannot be addressed", side, id)
		}
		ids[bp.BasePlanID] = struct{}{}
	}
	return ids, nil
}

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

// ComputeChildren diffs a nested level (the offers of a catalog) with the same
// engine as Compute. The comparison runs on the display keys, so the order
// (and any error naming a resource) follows the productId/parentId/offerId
// string, and the changes come back with their identity typed.
func ComputeChildren(local, live map[Key]json.RawMessage, managed []Field) (creates, patches, deletes []Change, err error) {
	byKey := make(map[string]Key, len(local)+len(live))
	flatten := func(in map[Key]json.RawMessage) map[string]json.RawMessage {
		out := make(map[string]json.RawMessage, len(in))
		for k, raw := range in {
			byKey[k.String()] = k
			out[k.String()] = raw
		}
		return out
	}
	plan, err := Compute(flatten(local), flatten(live), managed)
	if err != nil {
		return nil, nil, nil, err
	}
	typed := func(cs []Change) []Change {
		for i, c := range cs {
			k := byKey[c.ProductID]
			cs[i] = Change{ProductID: k.ProductID, ParentID: k.ParentID, OfferID: k.OfferID, Fields: c.Fields}
		}
		return cs
	}
	return typed(plan.Creates), typed(plan.Patches), typed(plan.Deletes), nil
}

func sortChanges(cs []Change) {
	sort.Slice(cs, func(i, j int) bool { return cs[i].Key() < cs[j].Key() })
}
