package iapcmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/iap"
)

const DefaultDir = "./monetization/iap"

const DefaultRegionsVersion = "2022/02"

type forbiddenError struct {
	pkg   string
	cause error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account cannot manage the one-time-product catalog for %q: grant it access to the app's monetization setup in Play Console (Users & permissions), then retry: %v", e.pkg, e.cause)
}
func (e *forbiddenError) Unwrap() error { return e.cause }

type notFoundError struct {
	pkg   string
	cause error
}

func (e *notFoundError) Error() string {
	return fmt.Sprintf("package %q not found: verify --package (or the project pin) names an app this service account can reach: %v", e.pkg, e.cause)
}
func (e *notFoundError) Unwrap() error { return e.cause }

func Classify(pkg string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusForbidden:
			return &forbiddenError{pkg: pkg, cause: err}
		case http.StatusNotFound:
			return &notFoundError{pkg: pkg, cause: err}
		}
	}
	return err
}

func ListLegacy(ctx context.Context, hc *http.Client, pkg string) (items []iap.LegacyItem, retired bool, err error) {
	items, err = iap.ListInAppProducts(ctx, hc, pkg)
	if errors.Is(err, iap.ErrLegacyRetired) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, Classify(pkg, err)
	}
	return items, false, nil
}

const LegacyRetiredNote = "Google retired the legacy inappproducts API for %q: its one-time-product catalog is read from the v2 model only"

func IsLegacy(raw json.RawMessage) bool {
	var probe struct {
		SKU string `json:"sku"`
	}
	return json.Unmarshal(raw, &probe) == nil && probe.SKU != ""
}

type OfferKey struct {
	ProductID        string
	PurchaseOptionID string
	OfferID          string
}

func (k OfferKey) String() string {
	return k.ProductID + "/" + k.PurchaseOptionID + "/" + k.OfferID
}

func EmbedOffers(items []iap.Item, offers []iap.OfferItem) (map[string]json.RawMessage, error) {
	byProduct := map[string]map[string][]any{}
	for _, o := range offers {
		var m map[string]any
		if err := json.Unmarshal(o.Raw, &m); err != nil {
			return nil, fmt.Errorf("decode live offer %s/%s/%s: %w", o.ProductID, o.PurchaseOptionID, o.OfferID, err)
		}
		delete(m, "packageName")
		if byProduct[o.ProductID] == nil {
			byProduct[o.ProductID] = map[string][]any{}
		}
		byProduct[o.ProductID][o.PurchaseOptionID] = append(byProduct[o.ProductID][o.PurchaseOptionID], m)
	}
	out := make(map[string]json.RawMessage, len(items))
	for _, it := range items {
		var product map[string]any
		if err := json.Unmarshal(it.Raw, &product); err != nil {
			return nil, fmt.Errorf("decode live one-time product %q: %w", it.ProductID, err)
		}
		perOption := byProduct[it.ProductID]
		if len(perOption) > 0 {
			options, _ := product["purchaseOptions"].([]any)
			seen := map[string]bool{}
			for _, p := range options {
				option, ok := p.(map[string]any)
				if !ok {
					continue
				}
				id, _ := option["purchaseOptionId"].(string)
				seen[id] = true
				if offs := perOption[id]; len(offs) > 0 {
					option["offers"] = offs
				}
			}
			for optionID := range perOption {
				if !seen[optionID] {
					return nil, fmt.Errorf("live offer(s) reference purchase option %q of %q which onetimeproducts.list did not return: refusing an inconsistent catalog", optionID, it.ProductID)
				}
			}
		}
		delete(byProduct, it.ProductID)
		b, err := output.Marshal(product)
		if err != nil {
			return nil, fmt.Errorf("encode one-time product %q: %w", it.ProductID, err)
		}
		out[it.ProductID] = b
	}
	for productID := range byProduct {
		return nil, fmt.Errorf("live offer(s) reference one-time product %q which onetimeproducts.list did not return: refusing an inconsistent catalog", productID)
	}
	return out, nil
}

func ExtractOffers(local map[string]json.RawMessage) (map[OfferKey]json.RawMessage, error) {
	out := map[OfferKey]json.RawMessage{}
	for productID, raw := range local {
		var product map[string]any
		if err := json.Unmarshal(raw, &product); err != nil {
			return nil, fmt.Errorf("decode declared one-time product %q: %w", productID, err)
		}
		options, _ := product["purchaseOptions"].([]any)
		for _, p := range options {
			option, ok := p.(map[string]any)
			if !ok {
				continue
			}
			optionID, _ := option["purchaseOptionId"].(string)
			offs, _ := option["offers"].([]any)
			for _, o := range offs {
				offer, ok := o.(map[string]any)
				if !ok {
					return nil, exit.Usagef("catalog file %s.json: purchaseOptions[%s].offers contains a non-object entry", productID, optionID)
				}
				offerID, _ := offer["offerId"].(string)
				if offerID == "" || optionID == "" {
					return nil, exit.Usagef("catalog file %s.json: an offer under purchase option %q has no offerId (or the purchase option has no purchaseOptionId): the IDs are the reconciliation keys", productID, optionID)
				}
				b, err := output.Marshal(offer)
				if err != nil {
					return nil, fmt.Errorf("encode declared offer %s/%s/%s: %w", productID, optionID, offerID, err)
				}
				key := OfferKey{ProductID: productID, PurchaseOptionID: optionID, OfferID: offerID}
				if _, dup := out[key]; dup {
					return nil, exit.Usagef("catalog file %s.json declares offer %q twice under purchase option %q", productID, offerID, optionID)
				}
				out[key] = b
			}
		}
	}
	return out, nil
}

func StripOffersFromProduct(raw json.RawMessage) (json.RawMessage, error) {
	var product map[string]any
	if err := json.Unmarshal(raw, &product); err != nil {
		return nil, fmt.Errorf("decode declared one-time product: %w", err)
	}
	options, _ := product["purchaseOptions"].([]any)
	for _, p := range options {
		if option, ok := p.(map[string]any); ok {
			delete(option, "offers")
		}
	}
	b, err := output.Marshal(product)
	if err != nil {
		return nil, fmt.Errorf("encode declared one-time product: %w", err)
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
