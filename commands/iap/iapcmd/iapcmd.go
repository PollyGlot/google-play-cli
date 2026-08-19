// Package iapcmd holds the wiring shared by the `gplay iap` leaves: package
// resolution, the default catalog directory, 403/404 hint classification, and
// the shaping helpers between the catalog file schema and the API resources.
// The catalog unions two surfaces: the v2 `monetization.onetimeproducts`
// model (all writes) and the read-only legacy `inappproducts` (recognizable by
// its `sku` field), so nothing is invisible while legacy is never written in
// place (ADR-0041 §8). Mirrors commands/subscriptions/subscriptionscmd.
package iapcmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/iap"
)

// DefaultDir is the catalog directory the iap leaves default to: the sibling
// segment of ./monetization/subscriptions.
const DefaultDir = "./monetization/iap"

// DefaultRegionsVersion is the regions version pin sent with v2 writes unless
// --regions-version overrides it: the latest version Google has published
// (ADR-0041 §7).
const DefaultRegionsVersion = "2022/02"

// ResolvePackage resolves the target package: --package wins, else the project
// pin. The Monetization catalog rides the package/app axis.
func ResolvePackage(rc *kernel.RunContext, flag string) (string, error) {
	pkg := strings.TrimSpace(flag)
	if pkg == "" && rc != nil && rc.Resolved != nil {
		pkg = strings.TrimSpace(rc.Resolved.Pin)
	}
	if pkg == "" {
		return "", exit.Usagef("no package: pass --package <pkg> or run gplay init in your repo")
	}
	return pkg, nil
}

// forbiddenError wraps a 403 with an agent-resolvable hint (no specific Play
// permission is tied to these methods in the Discovery snapshot, so no enum is
// named). It carries no ExitCode of its own, so the wrapped *api.Error (403 →
// exit 11) stays authoritative.
type forbiddenError struct {
	pkg   string
	cause error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account cannot manage the one-time-product catalog for %q: grant it access to the app's monetization setup in Play Console (Users & permissions), then retry: %v", e.pkg, e.cause)
}
func (e *forbiddenError) Unwrap() error { return e.cause }

// notFoundError wraps a 404 on the collection: the package itself is unknown
// to the credential.
type notFoundError struct {
	pkg   string
	cause error
}

func (e *notFoundError) Error() string {
	return fmt.Sprintf("package %q not found: verify --package (or the project pin) names an app this service account can reach: %v", e.pkg, e.cause)
}
func (e *notFoundError) Unwrap() error { return e.cause }

// Classify adds 403/404 hints to an API failure, leaving the *api.Error to
// drive the exit code (403 → 11, 404 → 30). Any other error passes through.
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

// IsLegacy reports whether a catalog file body is a legacy inappproducts
// resource: recognizable by its `sku` field (the v2 resource carries
// `productId` instead). The shape is the origin marker; no gplay-invented
// field is needed.
func IsLegacy(raw json.RawMessage) bool {
	var probe struct {
		SKU string `json:"sku"`
	}
	return json.Unmarshal(raw, &probe) == nil && probe.SKU != ""
}

// OfferKey is the composite identity of one v2 offer in a catalog.
type OfferKey struct {
	ProductID        string
	PurchaseOptionID string
	OfferID          string
}

// String renders the key the way plans display it.
func (k OfferKey) String() string {
	return k.ProductID + "/" + k.PurchaseOptionID + "/" + k.OfferID
}

// EmbedOffers nests each live v2 offer under its purchase option's "offers"
// array inside the product resources, returning the file bodies pull writes.
// The offers' packageName echo is stripped. An offer whose purchase option
// does not exist in its parent product is an integrity error.
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
		b, err := json.Marshal(product)
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

// ExtractOffers walks the declared v2 catalog and returns every embedded offer
// fragment keyed by its composite identity (file position + the fragment's
// offerId).
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
				b, err := json.Marshal(offer)
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

// StripOffersFromProduct removes every embedded purchaseOptions[].offers array
// from a declared product body: offers are not a field of the API resource.
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
	b, err := json.Marshal(product)
	if err != nil {
		return nil, fmt.Errorf("encode declared one-time product: %w", err)
	}
	return b, nil
}

// SortedOfferKeys returns the keys in stable display order.
func SortedOfferKeys[V any](m map[OfferKey]V) []OfferKey {
	keys := make([]OfferKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	return keys
}
