package subscriptionscmd

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/subscriptions"
)

type OfferKey struct {
	ProductID  string
	BasePlanID string
	OfferID    string
}

func (k OfferKey) String() string { return k.ProductID + "/" + k.BasePlanID + "/" + k.OfferID }

func EmbedOffers(items []subscriptions.Item, offers []subscriptions.OfferItem) (map[string]json.RawMessage, error) {
	byProduct := map[string]map[string][]any{}
	for _, o := range offers {
		var m map[string]any
		if err := json.Unmarshal(o.Raw, &m); err != nil {
			return nil, fmt.Errorf("decode live offer %s/%s/%s: %w", o.ProductID, o.BasePlanID, o.OfferID, err)
		}
		delete(m, "packageName")
		if byProduct[o.ProductID] == nil {
			byProduct[o.ProductID] = map[string][]any{}
		}
		byProduct[o.ProductID][o.BasePlanID] = append(byProduct[o.ProductID][o.BasePlanID], m)
	}
	out := make(map[string]json.RawMessage, len(items))
	for _, it := range items {
		var sub map[string]any
		if err := json.Unmarshal(it.Raw, &sub); err != nil {
			return nil, fmt.Errorf("decode live subscription %q: %w", it.ProductID, err)
		}
		perPlan := byProduct[it.ProductID]
		if len(perPlan) > 0 {
			plans, _ := sub["basePlans"].([]any)
			seen := map[string]bool{}
			for _, p := range plans {
				plan, ok := p.(map[string]any)
				if !ok {
					continue
				}
				id, _ := plan["basePlanId"].(string)
				seen[id] = true
				if offs := perPlan[id]; len(offs) > 0 {
					plan["offers"] = offs
				}
			}
			for planID := range perPlan {
				if !seen[planID] {
					return nil, fmt.Errorf("live offer(s) reference base plan %q of %q which subscriptions.list did not return: refusing an inconsistent catalog", planID, it.ProductID)
				}
			}
		}
		delete(byProduct, it.ProductID)
		b, err := output.Marshal(sub)
		if err != nil {
			return nil, fmt.Errorf("encode subscription %q: %w", it.ProductID, err)
		}
		out[it.ProductID] = b
	}
	for productID := range byProduct {
		return nil, fmt.Errorf("live offer(s) reference subscription %q which subscriptions.list did not return: refusing an inconsistent catalog", productID)
	}
	return out, nil
}

func ExtractOffers(local map[string]json.RawMessage) (map[OfferKey]json.RawMessage, error) {
	out := map[OfferKey]json.RawMessage{}
	for productID, raw := range local {
		var sub map[string]any
		if err := json.Unmarshal(raw, &sub); err != nil {
			return nil, fmt.Errorf("decode declared subscription %q: %w", productID, err)
		}
		plans, _ := sub["basePlans"].([]any)
		for _, p := range plans {
			plan, ok := p.(map[string]any)
			if !ok {
				continue
			}
			planID, _ := plan["basePlanId"].(string)
			offs, _ := plan["offers"].([]any)
			for _, o := range offs {
				offer, ok := o.(map[string]any)
				if !ok {
					return nil, exit.Usagef("catalog file %s.json: basePlans[%s].offers contains a non-object entry", productID, planID)
				}
				offerID, _ := offer["offerId"].(string)
				if offerID == "" || planID == "" {
					return nil, exit.Usagef("catalog file %s.json: an offer under base plan %q has no offerId (or the base plan has no basePlanId): the IDs are the reconciliation keys", productID, planID)
				}
				b, err := output.Marshal(offer)
				if err != nil {
					return nil, fmt.Errorf("encode declared offer %s/%s/%s: %w", productID, planID, offerID, err)
				}
				key := OfferKey{ProductID: productID, BasePlanID: planID, OfferID: offerID}
				if _, dup := out[key]; dup {
					return nil, exit.Usagef("catalog file %s.json declares offer %q twice under base plan %q", productID, offerID, planID)
				}
				out[key] = b
			}
		}
	}
	return out, nil
}

func StripOffersFromSubscription(raw json.RawMessage) (json.RawMessage, error) {
	var sub map[string]any
	if err := json.Unmarshal(raw, &sub); err != nil {
		return nil, fmt.Errorf("decode declared subscription: %w", err)
	}
	plans, _ := sub["basePlans"].([]any)
	for _, p := range plans {
		if plan, ok := p.(map[string]any); ok {
			delete(plan, "offers")
		}
	}
	b, err := output.Marshal(sub)
	if err != nil {
		return nil, fmt.Errorf("encode declared subscription: %w", err)
	}
	return b, nil
}

func SortedOfferKeys[V any](m map[OfferKey]V) []OfferKey {
	keys := make([]OfferKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	return keys
}
