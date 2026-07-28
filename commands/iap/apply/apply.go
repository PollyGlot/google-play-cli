// Package apply implements `gplay iap apply`: compute the Reconciliation plan
// between the on-disk one-time-product catalog (--dir) and the live one, print
// it, and execute it — writing the v2 model ONLY. Creates ride
// onetimeproducts.patch with allowMissing (the API has no insert); offer
// writes ride the per-purchase-option batch endpoints. Legacy inappproducts
// are inert: an edited, omitted or locally-created legacy file is refused with
// a message naming the way out (the one-way --migrate promotion arrives with
// slice #372) — gplay never writes the legacy surface in place (ADR-0041 §8).
// Any plan containing a delete refuses without --confirm (exit 3); lifecycle
// states (purchase options, offers) are normalized out and not yet reconciled.
// Edit-free, package axis. MarkMutating so GPLAY_READONLY refuses it (exit 4).
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

// managedFields is the v2 product-level projection apply reconciles — and the
// widest updateMask a product patch can ever send (ADR-0041 §5).
// purchaseOptions is declarable config riding the product patch (the
// sub-resource only has batch state/delete endpoints); its output-only state
// and the embedded offers array (a catalog-file construct) are normalized out.
var managedFields = []reconcile.Field{
	{Name: "listings"},
	{Name: "offerTags"},
	{Name: "restrictedPaymentCountries"},
	{Name: "taxAndComplianceSettings"},
	{Name: "purchaseOptions", Normalize: reconcile.StripKeys("state", "offers")},
}

// offerManagedFields is the offer-level projection: everything declarable on a
// OneTimeProductOffer. Identity, output-only state and the server-stamped
// regionsVersion are simply not listed, so they can never diff.
var offerManagedFields = []reconcile.Field{
	{Name: "regionalPricingAndAvailabilityConfigs"},
	{Name: "offerTags"},
	{Name: "discountedOffer"},
	{Name: "preOrderOffer"},
}

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package        string
	Dir            string
	RegionsVersion string
	DryRun         bool
	Confirm        bool
}

// Payload renders the Reconciliation plan — planned under --dry-run, executed
// otherwise.
type Payload struct {
	Package  string
	Dir      string
	Plan     reconcile.Plan
	DryRun   bool
	Requires []string
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
		len(p.Plan.OfferCreates) + len(p.Plan.OfferPatches) + len(p.Plan.OfferDeletes)
}

func (p Payload) renderHuman(w io.Writer, markdown bool) error {
	if markdown {
		if _, err := fmt.Fprintf(w, "## iap apply — %s\n\n", p.Package); err != nil {
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
	if _, err := fmt.Fprintf(w, "summary: create=%d patch=%d delete=%d offerCreate=%d offerPatch=%d offerDelete=%d unchanged=%d\n",
		len(p.Plan.Creates), len(p.Plan.Patches), len(p.Plan.Deletes),
		len(p.Plan.OfferCreates), len(p.Plan.OfferPatches), len(p.Plan.OfferDeletes), len(p.Plan.Unchanged)); err != nil {
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
// ADR-0003 exception family). Offer entries carry purchaseOptionId/offerId.
type jsonChange struct {
	Op               string   `json:"op"`
	Kind             string   `json:"kind,omitempty"`
	ProductID        string   `json:"productId"`
	PurchaseOptionID string   `json:"purchaseOptionId,omitempty"`
	OfferID          string   `json:"offerId,omitempty"`
	Fields           []string `json:"fields,omitempty"`
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
		changes = append(changes, jsonChange{Op: "create", ProductID: c.ProductID})
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
			"create":      len(p.Plan.Creates),
			"patch":       len(p.Plan.Patches),
			"delete":      len(p.Plan.Deletes),
			"offerCreate": len(p.Plan.OfferCreates),
			"offerPatch":  len(p.Plan.OfferPatches),
			"offerDelete": len(p.Plan.OfferDeletes),
			"unchanged":   len(p.Plan.Unchanged),
		},
		Requires: p.Requires,
	})
}

// guardLegacy enforces the legacy surface's inertness: every legacy file must
// match its live row, every live legacy row must keep its file (unless the v2
// model shadows it), and a legacy→v2 redeclaration names the #372 gate.
func guardLegacy(localLegacy map[string]json.RawMessage, localV2 map[string]json.RawMessage, liveLegacy []iap.LegacyItem, liveV2 map[string]json.RawMessage) error {
	liveBySKU := map[string]json.RawMessage{}
	for _, lg := range liveLegacy {
		liveBySKU[lg.SKU] = lg.Raw
	}
	for sku, raw := range localLegacy {
		liveRaw, ok := liveBySKU[sku]
		if !ok {
			return exit.Usagef("catalog declares legacy product %q that is not live — gplay never creates legacy inappproducts; declare it as a v2 product instead (v2 schema, no sku field)", sku)
		}
		if !legacyEqual(raw, liveRaw) {
			return exit.Usagef("catalog edits legacy product %q — gplay never writes the legacy surface in place; the one-way --migrate promotion to v2 arrives with slice #372 (until then, edit it in Play Console)", sku)
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
			continue // pre-migration shadow of a live v2 product — v2 owns it
		}
		if _, redeclared := localV2[sku]; redeclared {
			return exit.Usagef("catalog declares %q as a v2 product while it is live as a legacy inappproduct — the one-way --migrate promotion arrives with slice #372", sku)
		}
		return exit.Usagef("catalog omits legacy product %q — gplay never deletes the legacy surface; restore the file (gplay iap pull) or remove the product in Play Console", sku)
	}
	return nil
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

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	dir := in.Dir
	if dir == "" {
		dir = iapcmd.DefaultDir
	}
	local, err := catalog.Read(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, exit.Usagef("catalog directory %q does not exist — run gplay iap pull first, or pass --dir", dir)
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
	pkg, err := iapcmd.ResolvePackage(rc, in.Package)
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
		return nil, exit.Usagef("catalog directory %q holds no .json catalog files while %q has %d live one-time product(s) — applying it would delete them all; run gplay iap pull first, or pass the right --dir", dir, pkg, len(liveV2)+len(liveLegacy))
	}

	if err := guardLegacy(localLegacy, localV2, liveLegacy, liveV2); err != nil {
		return nil, err
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

	var requires []string
	if plan.HasDeletes() {
		requires = []string{"confirm"}
	}
	if in.DryRun || !plan.HasChanges() {
		return Payload{Package: pkg, Dir: dir, Plan: plan, DryRun: in.DryRun, Requires: requires}, nil
	}
	if plan.HasDeletes() && !in.Confirm {
		return nil, exit.SafetyFlag("confirm", "this plan deletes %d one-time product(s) and %d offer(s) from the live catalog of %q and deletion cannot be undone; pass --confirm to proceed (rehearse first with --dry-run)", len(plan.Deletes), len(plan.OfferDeletes), pkg)
	}

	regionsVersion := strings.TrimSpace(in.RegionsVersion)
	if regionsVersion == "" {
		regionsVersion = iapcmd.DefaultRegionsVersion
	}
	// Execution order: grow before shrinking — product upserts, offer batch
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
	rc.Confirmf("one-time products applied to %q (%d created, %d patched, %d deleted; offers: %d created, %d patched, %d deleted)", pkg,
		len(plan.Creates), len(plan.Patches), len(plan.Deletes),
		len(plan.OfferCreates), len(plan.OfferPatches), len(plan.OfferDeletes))
	return Payload{Package: pkg, Dir: dir, Plan: plan}, nil
}

// NewCommand returns the cobra command for `gplay iap apply`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "[experimental] Reconcile the live one-time-product catalog to the catalog files (v2 writes only)",
		Long: `Compute the create/patch/delete plan between the on-disk catalog (--dir,
default ` + iapcmd.DefaultDir + `) and the app's live one-time products, then
execute it — writing the v2 model (monetization.onetimeproducts) only. The
directory is the complete declared catalog: a live v2 product or offer with no
declaration is a delete in the plan (mirror semantics).

Legacy inappproducts (files with a "sku" field) are inert: gplay never
creates, edits or deletes the legacy surface — an edited, omitted or
locally-invented legacy file refuses with the way out (the one-way --migrate
promotion to v2 arrives with slice #372). A legacy product shadowed by a live
v2 product of the same ID is owned by the v2 file.

--dry-run reads live Play and prints the plan without changing anything.
Creates and patches run directly (a v2 create is a patch with allowMissing —
the API has no insert); a plan containing any delete refuses without
--confirm (exit 3) — CI=true never auto-confirms. Purchase-option and offer
lifecycle states are not yet reconciled (normalized out of the diff).

--regions-version pins the regions version sent with writes (default
` + iapcmd.DefaultRegionsVersion + `). GPLAY_READONLY refuses the command
(exit 4).

[experimental] — the surface may still evolve (ADR-0010).`,
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
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "authorize a destructive plan (required when the plan deletes products or offers)")
	return cmd
}
