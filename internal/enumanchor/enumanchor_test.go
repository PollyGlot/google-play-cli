package enumanchor_test

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/subscriptions/prices/migrate"
	"github.com/PollyGlot/google-play-cli/internal/play/appsigning"
	"github.com/PollyGlot/google-play-cli/internal/play/appstore"
	"github.com/PollyGlot/google-play-cli/internal/play/appstorecatalog"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/expansionfiles"
	"github.com/PollyGlot/google-play-cli/internal/play/iap"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
	"github.com/PollyGlot/google-play-cli/internal/play/latency"
	"github.com/PollyGlot/google-play-cli/internal/play/mappings"
	"github.com/PollyGlot/google-play-cli/internal/play/subscriptions"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

type source interface {
	enums(idx schemaindex.Index) (map[string][]string, error)
}

type property struct{ schema, name string }

func (p property) enums(idx schemaindex.Index) (map[string][]string, error) {
	where := p.schema + "." + p.name
	prop, ok := idx.Schemas[p.schema].Properties[p.name]
	if !ok {
		return nil, fmt.Errorf("%s: missing from the Schema index", where)
	}
	if len(prop.Enum) == 0 {
		return nil, fmt.Errorf("%s: no enum in the Schema index", where)
	}
	return map[string][]string{where: prop.Enum}, nil
}

type param struct {
	name    string
	methods []string
}

func (p param) enums(idx schemaindex.Index) (map[string][]string, error) {
	out := map[string][]string{}
	for _, id := range p.methods {
		where := id + " " + p.name
		m, ok := idx.Methods[id]
		if !ok {
			return nil, fmt.Errorf("%s: method missing from the Schema index", where)
		}
		i := slices.IndexFunc(m.Parameters, func(q schemaindex.Param) bool { return q.Name == p.name })
		if i < 0 {
			return nil, fmt.Errorf("%s: parameter missing from the Schema index", where)
		}
		if len(m.Parameters[i].Enum) == 0 {
			return nil, fmt.Errorf("%s: no enum in the Schema index", where)
		}
		out[where] = m.Parameters[i].Enum
	}
	return out, nil
}

type anchor struct {
	goList    string
	src       source
	code      []string
	unhandled map[string]string
}

func strs[T ~string](vs []T) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = string(v)
	}
	return out
}

func rotationWireValues(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, choice := range appsigning.RotationReasons() {
		v, ok := appsigning.RotationReason(choice)
		if !ok {
			t.Fatalf("appsigning.RotationReason(%q) refuses a choice RotationReasons lists", choice)
		}
		out = append(out, v)
	}
	return out
}

func anchors(t *testing.T) []anchor {
	t.Helper()
	rows := []anchor{
		{
			goList: "images.Types()",
			src: param{name: "imageType", methods: []string{
				"androidpublisher.edits.images.list",
				"androidpublisher.edits.images.upload",
				"androidpublisher.edits.images.delete",
				"androidpublisher.edits.images.deleteall",
			}},
			code: strs(images.Types()),
		},
		{
			goList: "images.AIGeneratedState",
			src:    param{name: "aiGeneratedState", methods: []string{"androidpublisher.edits.images.upload"}},
			code:   strs([]images.AIGeneratedState{images.NotAIGenerated, images.AIGenerated}),
		},
		{
			goList: "expansionfiles.Type*",
			src: param{name: "expansionFileType", methods: []string{
				"androidpublisher.edits.expansionfiles.get",
				"androidpublisher.edits.expansionfiles.patch",
				"androidpublisher.edits.expansionfiles.update",
				"androidpublisher.edits.expansionfiles.upload",
			}},
			code: []string{expansionfiles.TypeMain, expansionfiles.TypePatch},
		},
		{
			goList: "mappings.Type*",
			src:    param{name: "deobfuscationFileType", methods: []string{"androidpublisher.edits.deobfuscationfiles.upload"}},
			code:   []string{mappings.TypeProguard, mappings.TypeNativeCode},
		},
		{
			goList: "appsigning.RotationReason(s)",
			src:    property{"RotateAppSigningKeyRequest", "keyRotationReason"},
			code:   rotationWireValues(t),
		},
		{
			goList: "tracks.FormFactor*",
			src:    property{"TrackConfig", "formFactor"},
			code:   []string{tracks.FormFactorDefault, tracks.FormFactorWear, tracks.FormFactorAutomotive},
		},
		{
			goList: "tracks.TrackType*",
			src:    property{"TrackConfig", "type"},
			code:   []string{tracks.TrackTypeClosedTesting},
		},
		{
			goList: "tracks.Status*",
			src:    property{"TrackRelease", "status"},
			code:   []string{tracks.StatusDraft, tracks.StatusInProgress, tracks.StatusHalted, tracks.StatusCompleted},
		},
		{
			goList: "appstore.PublishState*",
			src:    property{"UpdateAppStoreHostedAppPublishStatusRequest", "publishState"},
			code:   []string{appstore.PublishStatePublished, appstore.PublishStateUnpublished},
		},
		{
			goList: "appstore.DeclarationFileType*",
			src:    property{"UploadAppStoreAppPolicyDeclarationFileRequest", "fileType"},
			code:   []string{appstore.DeclarationFileTypeDocument},
		},
		{
			goList: "edits.ChangesInReview.Wire",
			src:    param{name: "changesInReviewBehavior", methods: []string{"androidpublisher.edits.commit"}},
			code:   []string{edits.ChangesInReviewCancel.Wire(), edits.ChangesInReviewError.Wire()},
		},
		{
			goList: "migrate.PriceIncreaseTypes()",
			src:    property{"RegionalPriceMigrationConfig", "priceIncreaseType"},
			code:   migrate.PriceIncreaseTypes(),
		},
		{
			goList: "subscriptions.State*",
			src:    property{"BasePlan", "state"},
			code:   []string{subscriptions.StateDraft, subscriptions.StateActive, subscriptions.StateInactive},
		},
		{
			goList: "subscriptions.State*",
			src:    property{"SubscriptionOffer", "state"},
			code:   []string{subscriptions.StateDraft, subscriptions.StateActive, subscriptions.StateInactive},
		},
		{
			goList: "iap.State*",
			src:    property{"OneTimeProductOffer", "state"},
			code:   []string{iap.StateDraft, iap.StateActive, iap.StateInactive, iap.StateCancelled},
		},
		{
			goList: "iap.State*",
			src:    property{"OneTimeProductPurchaseOption", "state"},
			code:   []string{iap.StateDraft, iap.StateActive, iap.StateInactive, iap.StateInactivePublished},
		},
		{
			goList: "latency.Tolerance",
			src: param{name: "latencyTolerance", methods: []string{
				"androidpublisher.monetization.onetimeproducts.patch",
				"androidpublisher.monetization.onetimeproducts.delete",
				"androidpublisher.monetization.subscriptions.patch",
				"androidpublisher.monetization.subscriptions.basePlans.offers.patch",
			}},
			code: strs([]latency.Tolerance{latency.Sensitive, latency.Tolerant}),
		},
		{
			goList: "appstorecatalog.UpdateType*",
			src:    property{"RecentUpdateEvent", "updateType"},
			code:   []string{appstorecatalog.UpdateTypeModification, appstorecatalog.UpdateTypeDeletion},
		},
	}
	for _, schema := range []string{
		"UpdateOneTimeProductOfferRequest", "DeleteOneTimeProductOfferRequest",
		"ActivatePurchaseOptionRequest", "DeactivatePurchaseOptionRequest",
		"ActivateOneTimeProductOfferRequest", "DeactivateOneTimeProductOfferRequest", "CancelOneTimeProductOfferRequest",
		"ActivateBasePlanRequest", "DeactivateBasePlanRequest",
		"ActivateSubscriptionOfferRequest", "DeactivateSubscriptionOfferRequest",
	} {
		rows = append(rows, anchor{
			goList: "latency.Tolerance",
			src:    property{schema, "latencyTolerance"},
			code:   strs([]latency.Tolerance{latency.Sensitive, latency.Tolerant}),
		})
	}
	return rows
}

func unspecified(v string) bool {
	return strings.Contains(v, "UNSPECIFIED") || strings.HasSuffix(v, "Unspecified")
}

func (a anchor) mismatches(enum []string) []string {
	var e []string
	for _, v := range enum {
		if !unspecified(v) {
			e = append(e, v)
		}
	}
	if len(e) == 0 {
		return []string{"the enum holds only its unspecified sentinel"}
	}
	var out []string
	for _, v := range a.code {
		if !slices.Contains(e, v) {
			out = append(out, fmt.Sprintf("the code sends or expects %q, which Discovery no longer lists: update %s", v, a.goList))
		}
		if _, ok := a.unhandled[v]; ok {
			out = append(out, fmt.Sprintf("%q is both in %s and unhandled: drop the unhandled entry", v, a.goList))
		}
	}
	for _, v := range e {
		_, skipped := a.unhandled[v]
		if !skipped && !slices.Contains(a.code, v) {
			out = append(out, fmt.Sprintf("Discovery lists %q but %s does not handle it: add it there, or to unhandled with the reason", v, a.goList))
		}
	}
	for v := range a.unhandled {
		if !slices.Contains(e, v) {
			out = append(out, fmt.Sprintf("unhandled lists %q, which Discovery no longer lists: remove the stale entry", v))
		}
	}
	slices.Sort(out)
	return out
}

func TestCodeListsMatchDiscovery(t *testing.T) {
	idx, err := schemaindex.Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	for _, a := range anchors(t) {
		found, err := a.src.enums(idx)
		if err != nil {
			t.Errorf("%s: %v", a.goList, err)
			continue
		}
		for _, where := range slices.Sorted(maps.Keys(found)) {
			enum := found[where]
			t.Run(where, func(t *testing.T) {
				for _, msg := range a.mismatches(enum) {
					t.Error(msg)
				}
			})
		}
	}
}
