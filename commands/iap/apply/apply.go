// Package apply implements `gplay iap apply`: compute the Reconciliation plan
// between the on-disk one-time-product catalog (--dir) and the live one, print
// it, and execute it: writing the v2 model ONLY. Creates ride
// onetimeproducts.patch with allowMissing (the API has no insert); offer
// writes ride the per-purchase-option batch endpoints. Legacy inappproducts
// are inert: an edited, omitted or locally-created legacy file is refused with
// a message naming the way out (the one-way --migrate promotion arrives with
// slice #372): gplay never writes the legacy surface in place (ADR-0041 §8).
// A declared lifecycle state on a purchase option or an offer (slice #541) is
// reconciled through the dedicated state verbs, never a patch: purchase
// options via purchaseOptions:batchUpdateStates (their only write path),
// offers via :activate / :deactivate / :cancel. Any plan containing a delete
// or an offer cancel (irreversible: pending pre-orders are cancelled too)
// refuses without --confirm (exit 3). Edit-free, package axis. MarkMutating so
// GPLAY_READONLY refuses it (exit 4).
// --output json emits the plan (gplay-owned shape, the recorded ADR-0003
// exception family). Ships [experimental] (ADR-0010).
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
	"github.com/PollyGlot/google-play-cli/internal/monetization/reconcile"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/iap"
)

// managedFields is the v2 product-level projection apply reconciles, and the
// widest updateMask a product patch can ever send (ADR-0041 §5).
// purchaseOptions is declarable config riding the product patch (the
// sub-resource only has batch state/delete endpoints); its output-only state
// is reconciled through batchUpdateStates by planStates instead (kept out of
// the diff so a state change never doubles as a phantom patch, the
// subscriptions stance), and the embedded offers array (a catalog-file
// construct) is normalized out.
var managedFields = []reconcile.Field{
	{Name: "listings"},
	{Name: "offerTags"},
	{Name: "restrictedPaymentCountries"},
	{Name: "taxAndComplianceSettings"},
	{Name: "purchaseOptions", Normalize: reconcile.StripKeys("state", "offers")},
}

// offerManagedFields is the offer-level projection: everything declarable on a
// OneTimeProductOffer. Identity, the server-stamped regionsVersion and the
// output-only state (reconciled by planStates through the state verbs) are
// simply not listed, so they can never diff. TestOfferManagedFields_matchDiscovery
// pins this list to the Discovery schema (#537).
var offerManagedFields = []reconcile.Field{
	{Name: "regionalPricingAndAvailabilityConfigs"},
	{Name: "offerTags"},
	{Name: "discountedOffer"},
	{Name: "preOrderOffer"},
	{Name: "gameRewardOffer"},
}

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package        string
	Dir            string
	RegionsVersion string
	DryRun         bool
	Confirm        bool
	Migrate        bool
}

// Payload renders the Reconciliation plan: planned under --dry-run, executed
// otherwise. Migrating marks the creates that are one-way legacy→v2
// promotions (slice #372), rendered as their own `migrate` op.
type Payload struct {
	Package   string
	Dir       string
	Plan      reconcile.Plan
	Migrating map[string]bool
	DryRun    bool
	Requires  []string
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderHuman(w, false) },
		JSON:     func(w io.Writer) error { return p.renderJSON(w) },
		Markdown: func(w io.Writer) error { return p.renderHuman(w, true) },
	}
}

func (p Payload) verb(present, past string) string {
	if p.DryRun {
		return present
	}
	return past
}

func (p Payload) changeCount() int {
	return len(p.Plan.Creates) + len(p.Plan.Patches) + len(p.Plan.Deletes) +
		len(p.Plan.OfferCreates) + len(p.Plan.OfferPatches) + len(p.Plan.OfferDeletes) +
		len(p.Plan.StateChanges)
}

// stateOp names the verb a state change rides, in the plan's op vocabulary
// (activate / deactivate / cancel): the same word in the table and the JSON.
func stateOp(s reconcile.StateChange) string {
	switch s.To {
	case iap.OfferStateInactive:
		return "deactivate"
	case iap.OfferStateCancelled:
		return "cancel"
	default:
		return "activate"
	}
}

// statePast is the past tense of stateOp for an executed plan ("cancel"
// doubles its consonant, so the suffix is not mechanical).
func statePast(s reconcile.StateChange) string {
	if s.To == iap.OfferStateCancelled {
		return "cancelled"
	}
	return stateOp(s) + "d"
}

// stateTarget renders the identity a state change moves, in the composite
// form the plan displays (productId/purchaseOptionId[/offerId]).
func stateTarget(s reconcile.StateChange) string {
	t := s.ProductID + "/" + s.PurchaseOptionID
	if s.OfferID != "" {
		t += "/" + s.OfferID
	}
	return t
}

// stateKindLabel is the human name of a state change's kind.
func stateKindLabel(kind string) string {
	if kind == "offer" {
		return "offer"
	}
	return "purchase option"
}

func (p Payload) renderHuman(w io.Writer, markdown bool) error {
	if markdown {
		if _, err := fmt.Fprintf(w, "## iap apply: %s\n\n", p.Package); err != nil {
			return err
		}
	}
	if !p.Plan.HasChanges() {
		_, err := fmt.Fprintln(w, "no changes to apply (catalog directory already matches Play)")
		return err
	}
	header := "applied to"
	if p.DryRun {
		header = "plan for"
	}
	if _, err := fmt.Fprintf(w, "%s %s (%d change(s)):\n", header, p.Package, p.changeCount()); err != nil {
		return err
	}
	line := func(format string, a ...any) error {
		_, err := fmt.Fprintf(w, "  "+format+"\n", a...)
		return err
	}
	for _, c := range p.Plan.Creates {
		if p.Migrating[c.ProductID] {
			if err := line("%s %s (legacy → v2, one-way)", p.verb("migrate", "migrated"), c.ProductID); err != nil {
				return err
			}
			continue
		}
		if err := line("%s %s", p.verb("create", "created"), c.ProductID); err != nil {
			return err
		}
	}
	for _, c := range p.Plan.Patches {
		if err := line("%s %s (%s)", p.verb("patch", "patched"), c.ProductID, strings.Join(c.Fields, ", ")); err != nil {
			return err
		}
	}
	for _, c := range p.Plan.OfferCreates {
		if err := line("%s offer %s", p.verb("create", "created"), c.ProductID); err != nil {
			return err
		}
	}
	for _, c := range p.Plan.OfferPatches {
		if err := line("%s offer %s (%s)", p.verb("patch", "patched"), c.ProductID, strings.Join(c.Fields, ", ")); err != nil {
			return err
		}
	}
	for _, s := range p.Plan.StateChanges {
		if err := line("%s %s %s (%s → %s)", p.verb(stateOp(s), statePast(s)), stateKindLabel(s.Kind), stateTarget(s), s.From, s.To); err != nil {
			return err
		}
	}
	for _, c := range p.Plan.OfferDeletes {
		if err := line("%s offer %s", p.verb("delete", "deleted"), c.ProductID); err != nil {
			return err
		}
	}
	for _, c := range p.Plan.Deletes {
		if err := line("%s %s", p.verb("delete", "deleted"), c.ProductID); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "summary: create=%d migrate=%d patch=%d delete=%d offerCreate=%d offerPatch=%d offerDelete=%d state=%d unchanged=%d\n",
		len(p.Plan.Creates)-len(p.Migrating), len(p.Migrating), len(p.Plan.Patches), len(p.Plan.Deletes),
		len(p.Plan.OfferCreates), len(p.Plan.OfferPatches), len(p.Plan.OfferDeletes), len(p.Plan.StateChanges), len(p.Plan.Unchanged)); err != nil {
		return err
	}
	if len(p.Requires) > 0 {
		if _, err := fmt.Fprintf(w, "requires: %s\n", strings.Join(p.Requires, ", ")); err != nil {
			return err
		}
	}
	return nil
}

// jsonChange is one plan entry of the flat --output json schema (gplay-owned,
// ADR-0003 exception family). Offer entries carry purchaseOptionId/offerId;
// state entries use op activate/deactivate/cancel with kind
// purchaseOption/offer and the from/to states.
type jsonChange struct {
	Op               string   `json:"op"`
	Kind             string   `json:"kind,omitempty"`
	ProductID        string   `json:"productId"`
	PurchaseOptionID string   `json:"purchaseOptionId,omitempty"`
	OfferID          string   `json:"offerId,omitempty"`
	Fields           []string `json:"fields,omitempty"`
	From             string   `json:"from,omitempty"`
	To               string   `json:"to,omitempty"`
}

type jsonView struct {
	Package  string         `json:"package"`
	DryRun   bool           `json:"dryRun"`
	Changes  []jsonChange   `json:"changes"`
	Summary  map[string]int `json:"summary"`
	Requires []string       `json:"requires,omitempty"`
}

// splitOfferKey undoes the composite productId/purchaseOptionId/offerId key.
func splitOfferKey(composite string) (productID, purchaseOptionID, offerID string) {
	parts := strings.SplitN(composite, "/", 3)
	for len(parts) < 3 {
		parts = append(parts, "")
	}
	return parts[0], parts[1], parts[2]
}

func (p Payload) renderJSON(w io.Writer) error {
	changes := make([]jsonChange, 0, p.changeCount())
	for _, c := range p.Plan.Creates {
		op := "create"
		if p.Migrating[c.ProductID] {
			op = "migrate"
		}
		changes = append(changes, jsonChange{Op: op, ProductID: c.ProductID})
	}
	for _, c := range p.Plan.Patches {
		changes = append(changes, jsonChange{Op: "patch", ProductID: c.ProductID, Fields: c.Fields})
	}
	for _, c := range p.Plan.OfferCreates {
		pid, oid, off := splitOfferKey(c.ProductID)
		changes = append(changes, jsonChange{Op: "create", Kind: "offer", ProductID: pid, PurchaseOptionID: oid, OfferID: off})
	}
	for _, c := range p.Plan.OfferPatches {
		pid, oid, off := splitOfferKey(c.ProductID)
		changes = append(changes, jsonChange{Op: "patch", Kind: "offer", ProductID: pid, PurchaseOptionID: oid, OfferID: off, Fields: c.Fields})
	}
	for _, s := range p.Plan.StateChanges {
		changes = append(changes, jsonChange{Op: stateOp(s), Kind: s.Kind, ProductID: s.ProductID, PurchaseOptionID: s.PurchaseOptionID, OfferID: s.OfferID, From: s.From, To: s.To})
	}
	for _, c := range p.Plan.OfferDeletes {
		pid, oid, off := splitOfferKey(c.ProductID)
		changes = append(changes, jsonChange{Op: "delete", Kind: "offer", ProductID: pid, PurchaseOptionID: oid, OfferID: off})
	}
	for _, c := range p.Plan.Deletes {
		changes = append(changes, jsonChange{Op: "delete", ProductID: c.ProductID})
	}
	return output.WriteJSON(w, jsonView{
		Package: p.Package,
		DryRun:  p.DryRun,
		Changes: changes,
		Summary: map[string]int{
			"create":      len(p.Plan.Creates) - len(p.Migrating),
			"migrate":     len(p.Migrating),
			"patch":       len(p.Plan.Patches),
			"delete":      len(p.Plan.Deletes),
			"offerCreate": len(p.Plan.OfferCreates),
			"offerPatch":  len(p.Plan.OfferPatches),
			"offerDelete": len(p.Plan.OfferDeletes),
			"state":       len(p.Plan.StateChanges),
			"unchanged":   len(p.Plan.Unchanged),
		},
		Requires: p.Requires,
	})
}

// guardLegacy enforces the legacy surface's inertness: every legacy file must
// match its live row, every live legacy row must keep its file (unless the v2
// model shadows it), and drives the one-way legacy→v2 promotion (slice #372):
// a live legacy product redeclared as a v2 file migrates when --migrate is
// passed (the returned list feeds the plan's migrate ops), previews under
// --dry-run without it, and refuses with the named flag (exit 3) on a real
// run. Editing a legacy file in place stays refused: the promotion gesture IS
// rewriting the file in the v2 schema.
func guardLegacy(localLegacy, localV2 map[string]json.RawMessage, liveLegacy []iap.LegacyItem, liveV2 map[string]json.RawMessage, migrate, dryRun bool) (migrating []string, err error) {
	liveBySKU := map[string]json.RawMessage{}
	for _, lg := range liveLegacy {
		liveBySKU[lg.SKU] = lg.Raw
	}
	for sku, raw := range localLegacy {
		liveRaw, ok := liveBySKU[sku]
		if !ok {
			return nil, exit.Usagef("catalog declares legacy product %q that is not live: gplay never creates legacy inappproducts; declare it as a v2 product instead (v2 schema, no sku field)", sku)
		}
		if !legacyEqual(raw, liveRaw) {
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
			continue // pre-migration shadow of a live v2 product: v2 owns it
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

// legacyEqual compares a declared legacy file with its live row, ignoring the
// packageName echo pull strips.
func legacyEqual(local, live json.RawMessage) bool {
	var l, v map[string]any
	if json.Unmarshal(local, &l) != nil || json.Unmarshal(live, &v) != nil {
		return false
	}
	delete(l, "packageName")
	delete(v, "packageName")
	return reflect.DeepEqual(l, v)
}

// planStates computes the lifecycle transitions (slice #541): for every
// purchase option and offer whose file declares a state, compare with the
// live state (DRAFT for anything this very plan creates) and plan the
// matching verb. A file that omits state declares nothing (missing =
// unmanaged, the metadata stance); a declared state the verbs cannot reach is
// a usage error naming the impossible transition. Products the plan deletes
// are skipped: the parent delete takes their options and offers. Output is
// sorted by target so plans are stable.
func planStates(localV2 map[string]json.RawMessage, liveV2 map[string]json.RawMessage, liveOffers []iap.OfferItem, localOffers map[iapcmd.OfferKey]json.RawMessage, plan *reconcile.Plan) error {
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
	liveOptionState := map[string]string{} // productId/purchaseOptionId → state
	for productID, raw := range liveV2 {
		var frag optionsFrag
		if err := json.Unmarshal(raw, &frag); err != nil {
			return fmt.Errorf("decode live one-time product %q: %w", productID, err)
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
				live = "DRAFT" // being created by this very plan
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
			return fmt.Errorf("decode live offer %s/%s/%s: %w", o.ProductID, o.PurchaseOptionID, o.OfferID, err)
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
			live = "DRAFT" // being created by this very plan
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

// stateTransition validates one declared-vs-live state pair and returns the
// planned change (nil when nothing to do). Reachability follows the verbs:
// ACTIVE from anything but CANCELLED (terminal on the API side), INACTIVE
// only from ACTIVE (:deactivate, and purchaseOptions batch deactivate, both
// refuse otherwise), CANCELLED only on an ACTIVE offer (:cancel exists for
// pre-orders, purchase options have no such verb).
func stateTransition(kind, productID, purchaseOptionID, offerID, declared, live string) (*reconcile.StateChange, error) {
	if declared == "" || declared == live {
		return nil, nil
	}
	sc := reconcile.StateChange{Kind: kind, ProductID: productID, PurchaseOptionID: purchaseOptionID, OfferID: offerID, From: live, To: declared}
	label, target := stateKindLabel(kind), stateTarget(sc)
	switch declared {
	case iap.OfferStateActive:
		if live == iap.OfferStateCancelled {
			return nil, exit.Usagef("%s %s declares state ACTIVE while live is CANCELLED: a cancelled offer never comes back; fix the state: field or declare a new offer", label, target)
		}
	case iap.OfferStateInactive:
		if live != iap.OfferStateActive {
			return nil, exit.Usagef("%s %s declares state INACTIVE while live is %s: only an ACTIVE %s can be deactivated; fix the state: field or activate it first", label, target, live, label)
		}
	case iap.OfferStateCancelled:
		if kind != "offer" {
			return nil, exit.Usagef("%s %s declares state CANCELLED: only a pre-order offer can be cancelled (purchase options reach ACTIVE or INACTIVE); fix the state: field", label, target)
		}
		if live != iap.OfferStateActive {
			return nil, exit.Usagef("%s %s declares state CANCELLED while live is %s: only an ACTIVE pre-order offer can be cancelled; fix the state: field", label, target, live)
		}
	default:
		return nil, exit.Usagef("%s %s declares state %q while live is %s: the API can only reach ACTIVE (:activate), INACTIVE (:deactivate) or, for a pre-order offer, CANCELLED (:cancel); fix the state: field", label, target, declared, live)
	}
	return &sc, nil
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
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
	liveLegacy, err := iap.ListInAppProducts(rc.Ctx, httpClient, pkg)
	if err != nil {
		return nil, iapcmd.Classify(pkg, err)
	}

	// Mis-pointed --dir guard (the subscriptions apply stance), against the
	// union of both live surfaces.
	if len(local) == 0 && (len(liveV2) > 0 || len(liveLegacy) > 0) {
		return nil, exit.Usagef("catalog directory %q holds no .json catalog files while %q has %d live one-time product(s): applying it would delete them all; run gplay iap pull first, or pass the right --dir", dir, pkg, len(liveV2)+len(liveLegacy))
	}

	migrating, err := guardLegacy(localLegacy, localV2, liveLegacy, liveV2, in.Migrate, in.DryRun)
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

	plan, err := reconcile.Compute(localV2, liveV2, managedFields)
	if err != nil {
		return nil, err
	}
	deletedProducts := map[string]bool{}
	for _, c := range plan.Deletes {
		deletedProducts[c.ProductID] = true
	}
	localComposite := map[string]json.RawMessage{}
	for key, raw := range localOffers {
		localComposite[key.String()] = raw
	}
	liveComposite := map[string]json.RawMessage{}
	for _, o := range liveOffers {
		if deletedProducts[o.ProductID] {
			continue // the parent delete takes its offers
		}
		liveComposite[iapcmd.OfferKey{ProductID: o.ProductID, PurchaseOptionID: o.PurchaseOptionID, OfferID: o.OfferID}.String()] = o.Raw
	}
	offerPlan, err := reconcile.Compute(localComposite, liveComposite, offerManagedFields)
	if err != nil {
		return nil, err
	}
	plan.OfferCreates, plan.OfferPatches, plan.OfferDeletes = offerPlan.Creates, offerPlan.Patches, offerPlan.Deletes
	if err := planStates(localV2, liveV2, liveOffers, localOffers, &plan); err != nil {
		return nil, err
	}
	// An offer cancel is as irreversible as a delete (the offer never comes
	// back and its pending pre-orders are cancelled), so it rides the same
	// --confirm gate.
	cancels := 0
	for _, s := range plan.StateChanges {
		if s.To == iap.OfferStateCancelled {
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
	// Execution order: grow before shrinking: product upserts, offer batch
	// upserts, offer batch deletes, product deletes. Not transactional; a
	// failure surfaces immediately and a re-run converges.
	for _, c := range plan.Creates {
		body, err := iapcmd.StripOffersFromProduct(localV2[c.ProductID])
		if err != nil {
			return nil, err
		}
		if _, err := iap.PatchOneTimeProduct(rc.Ctx, httpClient, pkg, c.ProductID, regionsVersion, nil, true, body); err != nil {
			return nil, iapcmd.Classify(pkg, err)
		}
	}
	for _, c := range plan.Patches {
		body, err := iapcmd.StripOffersFromProduct(localV2[c.ProductID])
		if err != nil {
			return nil, err
		}
		if _, err := iap.PatchOneTimeProduct(rc.Ctx, httpClient, pkg, c.ProductID, regionsVersion, c.Fields, false, body); err != nil {
			return nil, iapcmd.Classify(pkg, err)
		}
	}
	// Offer upserts, grouped per purchase option (the only write path).
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
		pid, oid, off := splitOfferKey(c.ProductID)
		addUpdate(optionKey{pid, oid}, iap.OfferUpdate{Offer: localOffers[iapcmd.OfferKey{ProductID: pid, PurchaseOptionID: oid, OfferID: off}], AllowMissing: true})
	}
	for _, c := range plan.OfferPatches {
		pid, oid, off := splitOfferKey(c.ProductID)
		addUpdate(optionKey{pid, oid}, iap.OfferUpdate{Offer: localOffers[iapcmd.OfferKey{ProductID: pid, PurchaseOptionID: oid, OfferID: off}], UpdateMask: c.Fields})
	}
	for _, k := range updateOrder {
		if err := iap.BatchUpdateOffers(rc.Ctx, httpClient, pkg, k.product, k.option, updates[k], regionsVersion); err != nil {
			return nil, iapcmd.Classify(pkg, err)
		}
	}
	// State moves, after the upserts so a resource this run creates can be
	// activated in the same pass. Activations first, then deactivations, then
	// the irreversible cancels last. Within a phase the tree order follows the
	// direction: parents before children when growing (an offer only goes
	// ACTIVE under an active purchase option), children first when shrinking.
	// Purchase options are grouped per product into one batchUpdateStates
	// call, their only write path; offers ride their unary verbs.
	for _, phase := range []string{iap.OfferStateActive, iap.OfferStateInactive, iap.OfferStateCancelled} {
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
			optionUpdates[s.ProductID] = append(optionUpdates[s.ProductID], iap.PurchaseOptionStateUpdate{PurchaseOptionID: s.PurchaseOptionID, Activate: s.To == iap.OfferStateActive})
		}
		moveOptions := func() error {
			for _, productID := range productOrder {
				if _, err := iap.BatchUpdatePurchaseOptionStates(rc.Ctx, httpClient, pkg, productID, optionUpdates[productID]); err != nil {
					return iapcmd.Classify(pkg, err)
				}
			}
			return nil
		}
		moveOffers := func() error {
			for _, s := range offers {
				if _, err := iap.SetOfferState(rc.Ctx, httpClient, pkg, s.ProductID, s.PurchaseOptionID, s.OfferID, s.To); err != nil {
					return iapcmd.Classify(pkg, err)
				}
			}
			return nil
		}
		steps := []func() error{moveOffers, moveOptions}
		if phase == iap.OfferStateActive {
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
		pid, oid, off := splitOfferKey(c.ProductID)
		k := optionKey{pid, oid}
		if _, ok := deletes[k]; !ok {
			deleteOrder = append(deleteOrder, k)
		}
		deletes[k] = append(deletes[k], off)
	}
	for _, k := range deleteOrder {
		if err := iap.BatchDeleteOffers(rc.Ctx, httpClient, pkg, k.product, k.option, deletes[k]); err != nil {
			return nil, iapcmd.Classify(pkg, err)
		}
	}
	for _, c := range plan.Deletes {
		if err := iap.DeleteOneTimeProduct(rc.Ctx, httpClient, pkg, c.ProductID); err != nil {
			return nil, iapcmd.Classify(pkg, err)
		}
	}
	rc.Confirmf("one-time products applied to %q (%d created, %d migrated legacy→v2, %d patched, %d deleted; offers: %d created, %d patched, %d deleted; %d state change(s))", pkg,
		len(plan.Creates)-len(migrating), len(migrating), len(plan.Patches), len(plan.Deletes),
		len(plan.OfferCreates), len(plan.OfferPatches), len(plan.OfferDeletes), len(plan.StateChanges))
	return Payload{Package: pkg, Dir: dir, Plan: plan, Migrating: migratingSet}, nil
}

// NewCommand returns the cobra command for `gplay iap apply`.
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
by a live v2 product of the same ID is owned by the v2 file.

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
` + iapcmd.DefaultRegionsVersion + `). GPLAY_READONLY refuses the command
(exit 4).`,
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
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "read live Play and print the plan without committing (online)")
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "authorize a destructive plan (required when the plan deletes products or offers, or cancels a pre-order offer)")
	cmd.Flags().BoolVar(&in.Migrate, "migrate", false, "authorize one-way legacy→v2 promotions (required when a live legacy product is redeclared as v2)")
	return cmd
}
