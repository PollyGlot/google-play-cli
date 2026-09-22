package apply

import (
	"slices"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

// offerNotDeclarable lists the OneTimeProductOffer properties that are not
// declarable offer config, each with the reason it stays out of the diff.
// Everything else the schema carries must be in offerManagedFields.
var offerNotDeclarable = map[string]string{
	"packageName":      "identity: Required. Immutable; the parent app, carried by the request path",
	"productId":        "identity: Required. Immutable; the parent product, part of the offer key",
	"purchaseOptionId": "identity: Required. Immutable; the parent purchase option, part of the offer key",
	"offerId":          "identity: Required. Immutable; the offer key itself",
	"regionsVersion":   "Output only: server-stamped, sent as the request-level regionsVersion instead",
	"state":            "Output only: reconciled by planStates through the dedicated state verbs, never a patch",
}

// TestOfferManagedFields_matchDiscovery pins offerManagedFields to the
// declarable set of OneTimeProductOffer in the embedded Schema index (itself
// gated byte-identical to the committed Discovery snapshots). A field Google
// adds turns this red until it is either managed or listed above with its
// reason: an unlisted declarable field is otherwise invisible to both the
// diff and the updateMask, a silent no-op on patch (#537).
func TestOfferManagedFields_matchDiscovery(t *testing.T) {
	idx, err := schemaindex.Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	schema, ok := idx.Schemas["OneTimeProductOffer"]
	if !ok {
		t.Fatal("embedded Schema index has no OneTimeProductOffer")
	}
	for name := range offerNotDeclarable {
		if _, ok := schema.Properties[name]; !ok {
			t.Errorf("offerNotDeclarable lists %q, which OneTimeProductOffer no longer has: drop it", name)
		}
	}
	var want []string
	for name := range schema.Properties {
		if _, excluded := offerNotDeclarable[name]; !excluded {
			want = append(want, name)
		}
	}
	got := make([]string, 0, len(offerManagedFields))
	for _, f := range offerManagedFields {
		got = append(got, f.Name)
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("offerManagedFields = %v, want the declarable OneTimeProductOffer fields %v: manage the new field or list it in offerNotDeclarable with its reason", got, want)
	}
}
