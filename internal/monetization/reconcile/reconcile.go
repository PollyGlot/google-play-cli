package reconcile

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
)

type Field struct {
	Name      string
	Normalize func(any) any
}

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

type Change struct {
	ProductID string   `json:"productId"`
	ParentID  string   `json:"parentId,omitempty"`
	OfferID   string   `json:"offerId,omitempty"`
	Fields    []string `json:"fields,omitempty"`
}

func (c Change) Key() string {
	return Key{ProductID: c.ProductID, ParentID: c.ParentID, OfferID: c.OfferID}.String()
}

type Key struct {
	ProductID, ParentID, OfferID string
}

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

type StateChange struct {
	Kind             string `json:"kind"`
	ProductID        string `json:"productId"`
	BasePlanID       string `json:"basePlanId,omitempty"`
	PurchaseOptionID string `json:"purchaseOptionId,omitempty"`
	OfferID          string `json:"offerId,omitempty"`
	From             string `json:"from"`
	To               string `json:"to"`
}

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

const (
	KindOffer          = "offer"
	KindBasePlan       = "basePlan"
	KindPurchaseOption = "purchaseOption"
)

type Entry struct {
	Op        string
	Kind      string
	ProductID string
	ParentID  string
	OfferID   string
	Fields    []string
	From, To  string
}

func (e Entry) Target() string {
	return Key{ProductID: e.ProductID, ParentID: e.ParentID, OfferID: e.OfferID}.String()
}

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

func (s StateChange) Parent() string {
	if s.BasePlanID != "" {
		return s.BasePlanID
	}
	return s.PurchaseOptionID
}

func (s StateChange) Target() string {
	return Key{ProductID: s.ProductID, ParentID: s.Parent(), OfferID: s.OfferID}.String()
}

func (p Plan) HasChanges() bool {
	return len(p.Creates)+len(p.Patches)+len(p.Deletes)+len(p.BasePlanDeletes)+
		len(p.OfferCreates)+len(p.OfferPatches)+len(p.OfferDeletes)+
		len(p.StateChanges) > 0
}

func (p Plan) HasDeletes() bool {
	return len(p.Deletes)+len(p.BasePlanDeletes)+len(p.OfferDeletes) > 0
}

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

func ComputeChildren(local, live map[Key]json.RawMessage, managed []Field) (creates, patches, deletes []Change, err error) {
	byKey := map[string]Key{}
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
