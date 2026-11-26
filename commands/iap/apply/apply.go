package apply

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"reflect"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/iap/iapcmd"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/monetization/catalog"
	"github.com/PollyGlot/google-play-cli/internal/monetization/planview"
	"github.com/PollyGlot/google-play-cli/internal/monetization/reconcile"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/iap"
	"github.com/PollyGlot/google-play-cli/internal/play/latency"
)

var managedFields = []reconcile.Field{
	{Name: "listings"},
	{Name: "offerTags"},
	{Name: "restrictedPaymentCountries"},
	{Name: "taxAndComplianceSettings"},
	{Name: "purchaseOptions", Normalize: reconcile.StripKeys("state", "offers")},
}

var offerManagedFields = []reconcile.Field{
	{Name: "regionalPricingAndAvailabilityConfigs"},
	{Name: "offerTags"},
	{Name: "discountedOffer"},
	{Name: "preOrderOffer"},
	{Name: "gameRewardOffer"},
}

type Input struct {
	Package          string
	Dir              string
	RegionsVersion   string
	LatencyTolerance string
	DryRun           bool
	Confirm          bool
	Migrate          bool
}

type Payload struct {
	Package   string
	Dir       string
	Plan      reconcile.Plan
	Migrating map[string]bool
	DryRun    bool
	Requires  []string
}

func (p Payload) Renderers() output.Renderers {
	v := planview.View{Axis: planview.OneTimeProducts, Package: p.Package, Plan: p.Plan, Migrating: p.Migrating, DryRun: p.DryRun, Requires: p.Requires}
	return output.Renderers{
		Table:    func(w io.Writer) error { return v.Human(w, false) },
		JSON:     v.JSON,
		Markdown: func(w io.Writer) error { return v.Human(w, true) },
	}
}

func stateKindLabel(kind string) string {
	if kind == reconcile.KindOffer {
		return "offer"
	}
	return "purchase option"
}

func guardLegacy(pkg string, localLegacy, localV2 map[string]json.RawMessage, liveLegacy []iap.LegacyItem, liveV2 map[string]json.RawMessage, migrate, dryRun bool) (migrating []string, err error) {
	liveBySKU := map[string]json.RawMessage{}
	for _, lg := range liveLegacy {
		liveBySKU[lg.SKU] = lg.Raw
	}
	for sku, raw := range localLegacy {
		liveRaw, ok := liveBySKU[sku]
		if !ok {
			return nil, exit.Usagef("catalog declares legacy product %q that is not live: gplay never creates legacy inappproducts; declare it as a v2 product instead (v2 schema, no sku field)", sku)
		}
		equal, err := legacyEqual(iap.InAppProductsListSource(pkg), sku, raw, liveRaw)
		if err != nil {
			return nil, err
		}
		if !equal {
			return nil, exit.Usagef("catalog edits legacy product %q: gplay never writes the legacy surface in place; to change it, rewrite the file in the v2 schema (productId instead of sku) and apply with --migrate (one-way)", sku)
		}
	}
	skus := make([]string, 0, len(liveBySKU))
	for sku := range liveBySKU {
		skus = append(skus, sku)
	}
	sort.Strings(skus)
	for _, sku := range skus {
		if _, declared := localLegacy[sku]; declared {
			continue
		}
		if _, inLiveV2 := liveV2[sku]; inLiveV2 {
			continue
		}
		if _, redeclared := localV2[sku]; redeclared {
			if !migrate && !dryRun {
				return nil, exit.SafetyFlag("migrate", "catalog declares %q as a v2 product while it is live as a legacy inappproduct: promoting it to v2 is a one-way door (it can never return to inappproducts); pass --migrate to proceed (rehearse first with --dry-run)", sku)
			}
			migrating = append(migrating, sku)
			continue
		}
		return nil, exit.Usagef("catalog omits legacy product %q: gplay never deletes the legacy surface; restore the file (gplay iap pull) or remove the product in Play Console", sku)
	}
	return migrating, nil
}

func legacyEqual(from api.Source, sku string, local, live json.RawMessage) (bool, error) {
	var l, v map[string]any
	if err := json.Unmarshal(live, &v); err != nil {
		return false, from.DecodeFailure(fmt.Sprintf("decode live in-app product %q", sku), err)
	}
	localDecoded := json.Unmarshal(local, &l) == nil
	delete(l, "packageName")
	delete(v, "packageName")
	return localDecoded && reflect.DeepEqual(l, v), nil
}

func planStates(pkg string, localV2 map[string]json.RawMessage, liveV2 map[string]json.RawMessage, liveOffers []iap.OfferItem, localOffers map[iapcmd.OfferKey]json.RawMessage, plan *reconcile.Plan) error {
	deletedProducts := map[string]bool{}
	for _, c := range plan.Deletes {
		deletedProducts[c.ProductID] = true
	}
	type optionsFrag struct {
		PurchaseOptions []struct {
			PurchaseOptionID string `json:"purchaseOptionId"`
			State            string `json:"state"`
		} `json:"purchaseOptions"`
	}
	liveOptionState := map[string]string{}
	for productID, raw := range liveV2 {
		var frag optionsFrag
		if err := json.Unmarshal(raw, &frag); err != nil {
			return iap.ListSource(pkg).DecodeFailure(fmt.Sprintf("decode live one-time product %q", productID), err)
		}
		for _, po := range frag.PurchaseOptions {
			liveOptionState[productID+"/"+po.PurchaseOptionID] = po.State
		}
	}
	productIDs := make([]string, 0, len(localV2))
	for id := range localV2 {
		productIDs = append(productIDs, id)
	}
	sort.Strings(productIDs)
	for _, productID := range productIDs {
		if deletedProducts[productID] {
			continue
		}
		var frag optionsFrag
		if err := json.Unmarshal(localV2[productID], &frag); err != nil {
			return fmt.Errorf("decode declared one-time product %q: %w", productID, err)
		}
		for _, po := range frag.PurchaseOptions {
			live, ok := liveOptionState[productID+"/"+po.PurchaseOptionID]
			if !ok {
				live = iap.StateDraft
			}
			sc, err := stateTransition("purchaseOption", productID, po.PurchaseOptionID, "", po.State, live)
			if err != nil {
				return err
			}
			if sc != nil {
				plan.StateChanges = append(plan.StateChanges, *sc)
			}
		}
	}

	liveOfferState := map[iapcmd.OfferKey]string{}
	for _, o := range liveOffers {
		var frag struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal(o.Raw, &frag); err != nil {
			return iap.OffersListSource(pkg).DecodeFailure(fmt.Sprintf("decode live offer %s/%s/%s", o.ProductID, o.PurchaseOptionID, o.OfferID), err)
		}
		liveOfferState[iapcmd.OfferKey{ProductID: o.ProductID, PurchaseOptionID: o.PurchaseOptionID, OfferID: o.OfferID}] = frag.State
	}
	for _, key := range iapcmd.SortedOfferKeys(localOffers) {
		if deletedProducts[key.ProductID] {
			continue
		}
		var frag struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal(localOffers[key], &frag); err != nil {
			return fmt.Errorf("decode declared offer %s: %w", key, err)
		}
		live, ok := liveOfferState[key]
		if !ok {
			live = iap.StateDraft
		}
		sc, err := stateTransition("offer", key.ProductID, key.PurchaseOptionID, key.OfferID, frag.State, live)
		if err != nil {
			return err
		}
		if sc != nil {
			plan.StateChanges = append(plan.StateChanges, *sc)
		}
	}
	return nil
}

func stateTransition(kind, productID, purchaseOptionID, offerID, declared, live string) (*reconcile.StateChange, error) {
	if declared == "" || declared == live {
		return nil, nil
	}
	sc := reconcile.StateChange{Kind: kind, ProductID: productID, PurchaseOptionID: purchaseOptionID, OfferID: offerID, From: live, To: declared}
	label, target := stateKindLabel(kind), sc.Target()
	switch declared {
	case iap.StateActive:
		if live == iap.StateCancelled {
			return nil, exit.Usagef("%s %s declares state ACTIVE while live is CANCELLED: a cancelled offer never comes back; fix the state: field or declare a new offer", label, target)
		}
	case iap.StateInactive:
		if live == iap.StateInactivePublished {
			return nil, exit.Usagef("%s %s declares state INACTIVE while live is INACTIVE_PUBLISHED: Play migrated this option and it is already off sale; declare INACTIVE_PUBLISHED or omit the state: field", label, target)
		}
		if live != iap.StateActive {
			return nil, exit.Usagef("%s %s declares state INACTIVE while live is %s: only an ACTIVE %s can be deactivated; fix the state: field or activate it first", label, target, live, label)
		}
	case iap.StateCancelled:
		if kind != "offer" {
			return nil, exit.Usagef("%s %s declares state CANCELLED: only a pre-order offer can be cancelled (purchase options reach ACTIVE or INACTIVE); fix the state: field", label, target)
		}
		if live != iap.StateActive {
			return nil, exit.Usagef("%s %s declares state CANCELLED while live is %s: only an ACTIVE pre-order offer can be cancelled; fix the state: field", label, target, live)
		}
	default:
		return nil, exit.Usagef("%s %s declares state %q while live is %s: the API can only reach ACTIVE (:activate), INACTIVE (:deactivate) or, for a pre-order offer, CANCELLED (:cancel); fix the state: field", label, target, declared, live)
	}
	return &sc, nil
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	lt, err := latency.Parse(in.LatencyTolerance)
	if err != nil {
		return nil, err
	}
	dir := in.Dir
	if dir == "" {
		dir = iapcmd.DefaultDir
	}
	local, err := catalog.Read(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, exit.Usagef("catalog directory %q does not exist: run gplay iap pull first, or pass --dir", dir)
		}
		return nil, err
	}
	localV2 := map[string]json.RawMessage{}
	localLegacy := map[string]json.RawMessage{}
	for id, raw := range local {
		if iapcmd.IsLegacy(raw) {
			localLegacy[id] = raw
		} else {
			localV2[id] = raw
		}
	}
	localOffers, err := iapcmd.ExtractOffers(localV2)
	if err != nil {
		return nil, err
	}
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	items, err := iap.ListOneTimeProducts(rc.Ctx, httpClient, pkg)
	if err != nil {
		return nil, iapcmd.Classify(pkg, err)
	}
	liveV2 := make(map[string]json.RawMessage, len(items))
	for _, it := range items {
		liveV2[it.ProductID] = it.Raw
	}
	liveLegacy, retired, err := iapcmd.ListLegacy(rc.Ctx, httpClient, pkg)
	if err != nil {
		return nil, err
	}
	if retired {
		if len(localLegacy) > 0 {
			return nil, exit.Usagef("catalog %q holds %d legacy file(s) (sku field) while Google retired the legacy inappproducts API for %q: its products now live in the v2 model; run gplay iap pull to rewrite the catalog in the v2 schema (it mirrors the live catalog into --dir, so commit or set aside local edits first), then re-apply them", dir, len(localLegacy), pkg)
		}
		rc.Notef(iapcmd.LegacyRetiredNote, pkg)
	}

	if len(local) == 0 && (len(liveV2) > 0 || len(liveLegacy) > 0) {
		return nil, exit.Usagef("catalog directory %q holds no .json catalog files while %q has %d live one-time product(s): applying it would delete them all; run gplay iap pull first, or pass the right --dir", dir, pkg, len(liveV2)+len(liveLegacy))
	}

	migrating, err := guardLegacy(pkg, localLegacy, localV2, liveLegacy, liveV2, in.Migrate, in.DryRun)
	if err != nil {
		return nil, err
	}
	migratingSet := make(map[string]bool, len(migrating))
	for _, id := range migrating {
		migratingSet[id] = true
	}

	liveOffers, err := iap.ListAllOffers(rc.Ctx, httpClient, pkg)
	if err != nil {
		return nil, iapcmd.Classify(pkg, err)
	}

	plan, err := reconcile.Compute(localV2, liveV2, iap.ListSource(pkg), managedFields)
	if err != nil {
		return nil, err
	}
	deletedProducts := map[string]bool{}
	for _, c := range plan.Deletes {
		deletedProducts[c.ProductID] = true
	}
	localKeyed := map[reconcile.Key]json.RawMessage{}
	for key, raw := range localOffers {
		localKeyed[reconcile.Key{ProductID: key.ProductID, ParentID: key.PurchaseOptionID, OfferID: key.OfferID}] = raw
	}
	liveKeyed := map[reconcile.Key]json.RawMessage{}
	for _, o := range liveOffers {
		if deletedProducts[o.ProductID] {
			continue
		}
		liveKeyed[reconcile.Key{ProductID: o.ProductID, ParentID: o.PurchaseOptionID, OfferID: o.OfferID}] = o.Raw
	}
	plan.OfferCreates, plan.OfferPatches, plan.OfferDeletes, err = reconcile.ComputeChildren(localKeyed, liveKeyed, iap.OffersListSource(pkg), offerManagedFields)
	if err != nil {
		return nil, err
	}
	if err := planStates(pkg, localV2, liveV2, liveOffers, localOffers, &plan); err != nil {
		return nil, err
	}
	cancels := 0
	for _, s := range plan.StateChanges {
		if s.To == iap.StateCancelled {
			cancels++
		}
	}
	destructive := plan.HasDeletes() || cancels > 0

	var requires []string
	if destructive {
		requires = append(requires, "confirm")
	}
	if len(migrating) > 0 && !in.Migrate {
		requires = append(requires, "migrate")
	}
	if in.DryRun || !plan.HasChanges() {
		return Payload{Package: pkg, Dir: dir, Plan: plan, Migrating: migratingSet, DryRun: in.DryRun, Requires: requires}, nil
	}
	if destructive && !in.Confirm {
		return nil, exit.SafetyFlag("confirm", "this plan deletes %d one-time product(s) and %d offer(s) and cancels %d pre-order offer(s) in the live catalog of %q, and neither can be undone; pass --confirm to proceed (rehearse first with --dry-run)", len(plan.Deletes), len(plan.OfferDeletes), cancels, pkg)
	}

	regionsVersion := strings.TrimSpace(in.RegionsVersion)
	if regionsVersion == "" {
		regionsVersion = iapcmd.DefaultRegionsVersion
	}
	for _, c := range plan.Creates {
		body, err := iapcmd.StripOffersFromProduct(localV2[c.ProductID])
		if err != nil {
			return nil, err
		}
		if _, err := iap.PatchOneTimeProduct(rc.Ctx, httpClient, pkg, c.ProductID, regionsVersion, nil, true, body, lt); err != nil {
			return nil, iapcmd.Classify(pkg, err)
		}
	}
	for _, c := range plan.Patches {
		body, err := iapcmd.StripOffersFromProduct(localV2[c.ProductID])
		if err != nil {
			return nil, err
		}
		if _, err := iap.PatchOneTimeProduct(rc.Ctx, httpClient, pkg, c.ProductID, regionsVersion, c.Fields, false, body, lt); err != nil {
			return nil, iapcmd.Classify(pkg, err)
		}
	}
	type optionKey struct{ product, option string }
	updates := map[optionKey][]iap.OfferUpdate{}
	var updateOrder []optionKey
	addUpdate := func(k optionKey, u iap.OfferUpdate) {
		if _, ok := updates[k]; !ok {
			updateOrder = append(updateOrder, k)
		}
		updates[k] = append(updates[k], u)
	}
	for _, c := range plan.OfferCreates {
		addUpdate(optionKey{c.ProductID, c.ParentID}, iap.OfferUpdate{Offer: localOffers[iapcmd.OfferKey{ProductID: c.ProductID, PurchaseOptionID: c.ParentID, OfferID: c.OfferID}], AllowMissing: true})
	}
	for _, c := range plan.OfferPatches {
		addUpdate(optionKey{c.ProductID, c.ParentID}, iap.OfferUpdate{Offer: localOffers[iapcmd.OfferKey{ProductID: c.ProductID, PurchaseOptionID: c.ParentID, OfferID: c.OfferID}], UpdateMask: c.Fields})
	}
	for _, k := range updateOrder {
		if err := iap.BatchUpdateOffers(rc.Ctx, httpClient, pkg, k.product, k.option, updates[k], regionsVersion, lt); err != nil {
			return nil, iapcmd.Classify(pkg, err)
		}
	}
	for _, phase := range []string{iap.StateActive, iap.StateInactive, iap.StateCancelled} {
		optionUpdates := map[string][]iap.PurchaseOptionStateUpdate{}
		var (
			productOrder []string
			offers       []reconcile.StateChange
		)
		for _, s := range plan.StateChanges {
			if s.To != phase {
				continue
			}
			if s.Kind == "offer" {
				offers = append(offers, s)
				continue
			}
			if _, ok := optionUpdates[s.ProductID]; !ok {
				productOrder = append(productOrder, s.ProductID)
			}
			optionUpdates[s.ProductID] = append(optionUpdates[s.ProductID], iap.PurchaseOptionStateUpdate{PurchaseOptionID: s.PurchaseOptionID, Activate: s.To == iap.StateActive})
		}
		moveOptions := func() error {
			for _, productID := range productOrder {
				if err := iap.BatchUpdatePurchaseOptionStates(rc.Ctx, httpClient, pkg, productID, optionUpdates[productID], lt); err != nil {
					return iapcmd.Classify(pkg, err)
				}
			}
			return nil
		}
		moveOffers := func() error {
			for _, s := range offers {
				if _, err := iap.SetOfferState(rc.Ctx, httpClient, pkg, s.ProductID, s.PurchaseOptionID, s.OfferID, s.To, lt); err != nil {
					return iapcmd.Classify(pkg, err)
				}
			}
			return nil
		}
		steps := []func() error{moveOffers, moveOptions}
		if phase == iap.StateActive {
			steps = []func() error{moveOptions, moveOffers}
		}
		for _, step := range steps {
			if err := step(); err != nil {
				return nil, err
			}
		}
	}
	deletes := map[optionKey][]string{}
	var deleteOrder []optionKey
	for _, c := range plan.OfferDeletes {
		k := optionKey{c.ProductID, c.ParentID}
		if _, ok := deletes[k]; !ok {
			deleteOrder = append(deleteOrder, k)
		}
		deletes[k] = append(deletes[k], c.OfferID)
	}
	for _, k := range deleteOrder {
		if err := iap.BatchDeleteOffers(rc.Ctx, httpClient, pkg, k.product, k.option, deletes[k], lt); err != nil {
			return nil, iapcmd.Classify(pkg, err)
		}
	}
	for _, c := range plan.Deletes {
		if err := iap.DeleteOneTimeProduct(rc.Ctx, httpClient, pkg, c.ProductID, lt); err != nil {
			return nil, iapcmd.Classify(pkg, err)
		}
	}
	rc.Confirmf("one-time products applied to %q (%d created, %d migrated legacy→v2, %d patched, %d deleted; offers: %d created, %d patched, %d deleted; %d state change(s))", pkg,
		len(plan.Creates)-len(migrating), len(migrating), len(plan.Patches), len(plan.Deletes),
		len(plan.OfferCreates), len(plan.OfferPatches), len(plan.OfferDeletes), len(plan.StateChanges))
	return Payload{Package: pkg, Dir: dir, Plan: plan, Migrating: migratingSet}, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Reconcile the live one-time-product catalog to the catalog files (v2 writes only)",
		Long: `Compute the create/patch/delete plan between the on-disk catalog (--dir,
default ` + iapcmd.DefaultDir + `) and the app's live one-time products, then
execute it: writing the v2 model (monetization.onetimeproducts) only. The
directory is the complete declared catalog: a live v2 product or offer with no
declaration is a delete in the plan (mirror semantics).

Legacy inappproducts (files with a "sku" field) are inert: gplay never
creates, edits or deletes the legacy surface. To change a legacy product,
rewrite its file in the v2 schema (productId instead of sku) and apply with
--migrate: the promotion to v2 is a ONE-WAY door (the product can never
return to inappproducts), so it refuses without the flag (exit 3, naming it)
and shows as a distinct "migrate" op in the plan. A legacy product shadowed
by a live v2 product of the same ID is owned by the v2 file. When Google has
retired the legacy API for the app ("Please migrate to the new publishing
API"), the live catalog is v2 only: a note says so on stderr, and a legacy
file in --dir is refused (exit 2) until gplay iap pull rewrites it in v2.

A purchase option's or an offer's "state" field is reconciled through the
dedicated state verbs, never a patch (omit the field to leave state
unmanaged): a purchase option reaches ACTIVE or INACTIVE
(purchaseOptions:batchUpdateStates), an offer reaches ACTIVE, INACTIVE (a
discounted offer) or CANCELLED (a pre-order offer; its pending orders are
cancelled too). State changes are listed in the plan as their own
activate/deactivate/cancel entries and sent after creates and patches, so a
product declared with an ACTIVE purchase option activates in the same run.

--dry-run reads live Play and prints the plan without changing anything.
Creates and patches run directly (a v2 create is a patch with allowMissing:
the API has no insert); a plan containing any delete, or any offer cancel
(irreversible: a cancelled offer never comes back), refuses without
--confirm (exit 3): CI=true never auto-confirms.

--regions-version pins the regions version sent with writes (default
` + iapcmd.DefaultRegionsVersion + `).

--latency-tolerance tolerant trades propagation speed for throughput on a
large catalog: Play takes up to 24 hours to show the writes to buyers, against
minutes by default. It rides every write of the plan. GPLAY_READONLY refuses
the command (exit 4).`,
		Example: `  # Show the plan against live Play (online, nothing changes)
  gplay iap apply --dry-run

  # Run it, deletes and offer cancels included
  gplay iap apply --confirm

  # Promote legacy products redeclared in the v2 schema (one-way)
  gplay iap apply --migrate --dry-run`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.Dir, "dir", iapcmd.DefaultDir, "catalog directory to reconcile from")
	cmd.Flags().StringVar(&in.RegionsVersion, "regions-version", iapcmd.DefaultRegionsVersion, "regions version pin sent with writes")
	cmd.Flags().StringVar(&in.LatencyTolerance, latency.Flag, "", latency.Help)
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "read live Play and print the plan without committing (online)")
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "authorize a destructive plan (required when the plan deletes products or offers, or cancels a pre-order offer)")
	cmd.Flags().BoolVar(&in.Migrate, "migrate", false, "authorize one-way legacy→v2 promotions (required when a live legacy product is redeclared as v2)")
	return cmd
}
