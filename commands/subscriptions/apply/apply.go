// Package apply implements `gplay subscriptions apply`: compute the
// Reconciliation plan between the on-disk Monetization catalog (--dir) and the
// live catalog — subscriptions, their base plans, their offers, and the
// declared lifecycle state of base plans and offers — print it, and execute
// it. Creates and patches run directly; any plan containing a delete
// (subscription or offer) refuses without --confirm (exit 3, ADR-0017 family;
// CI=true never auto-confirms). --dry-run prints the plan online and stops.
// State transitions ride the dedicated activate/deactivate endpoints, never a
// patch; they are surfaced prominently in every plan view (they move buyer
// availability) but are not gated — they are reversible. The updateMask of
// every patch is exactly the changed managed fields (ADR-0041). Edit-free,
// package axis. MarkMutating so GPLAY_READONLY refuses it (exit 4). --output
// json emits the plan — a gplay-owned shape, a recorded ADR-0003 exception
// like metadata apply. Ships [experimental] (ADR-0010).
package apply

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/subscriptions/subscriptionscmd"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/monetization/catalog"
	"github.com/PollyGlot/google-play-cli/internal/monetization/reconcile"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/subscriptions"
)

// managedFields is the subscription-level projection apply reconciles — and
// therefore the widest updateMask a parent patch can ever send (ADR-0041 §5).
// basePlans (slice #368) is declarable config riding the parent patch; its
// output-only state subfield is reconciled via activate/deactivate instead
// (slice #369) and its embedded offers array is a catalog-file construct the
// API resource does not carry — both are normalized out of the comparison.
var managedFields = []reconcile.Field{
	{Name: "listings"},
	{Name: "taxAndComplianceSettings"},
	{Name: "restrictedPaymentCountries"},
	{Name: "basePlans", Normalize: reconcile.StripKeys("state", "offers")},
}

// offerManagedFields is the offer-level projection (slice #369): everything
// declarable on a SubscriptionOffer. Identity (offerId & the path echoes) and
// the output-only state are simply not listed, so they can never diff.
var offerManagedFields = []reconcile.Field{
	{Name: "phases"},
	{Name: "regionalConfigs"},
	{Name: "offerTags"},
	{Name: "targeting"},
	{Name: "otherRegionsConfig"},
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
// otherwise. Requires lists the safety gate when the plan is destructive
// (ADR-0017 §4).
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

// verb conjugates one plan action for the human views.
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

func (p Payload) renderHuman(w io.Writer, markdown bool) error {
	if markdown {
		if _, err := fmt.Fprintf(w, "## subscriptions apply — %s\n\n", p.Package); err != nil {
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
	for _, s := range p.Plan.StateChanges {
		verb := p.verb("activate", "activated")
		if s.To == "INACTIVE" {
			verb = p.verb("deactivate", "deactivated")
		}
		target := s.ProductID + "/" + s.BasePlanID
		kind := "base plan"
		if s.Kind == "offer" {
			target += "/" + s.OfferID
			kind = "offer"
		}
		if err := line("%s %s %s (%s → %s)", verb, kind, target, s.From, s.To); err != nil {
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
	if _, err := fmt.Fprintf(w, "summary: create=%d patch=%d delete=%d offerCreate=%d offerPatch=%d offerDelete=%d state=%d unchanged=%d\n",
		len(p.Plan.Creates), len(p.Plan.Patches), len(p.Plan.Deletes),
		len(p.Plan.OfferCreates), len(p.Plan.OfferPatches), len(p.Plan.OfferDeletes),
		len(p.Plan.StateChanges), len(p.Plan.Unchanged)); err != nil {
		return err
	}
	if len(p.Requires) > 0 {
		if _, err := fmt.Fprintf(w, "requires: %s\n", strings.Join(p.Requires, ", ")); err != nil {
			return err
		}
	}
	return nil
}

// jsonChange is one plan entry of the flat --output json schema (a gplay-owned
// shape — ADR-0003 exception — kept flat so a CI gate is one jq line). Offer
// entries carry basePlanId/offerId; state entries use op activate/deactivate
// with kind and from/to.
type jsonChange struct {
	Op         string   `json:"op"`
	Kind       string   `json:"kind,omitempty"`
	ProductID  string   `json:"productId"`
	BasePlanID string   `json:"basePlanId,omitempty"`
	OfferID    string   `json:"offerId,omitempty"`
	Fields     []string `json:"fields,omitempty"`
	From       string   `json:"from,omitempty"`
	To         string   `json:"to,omitempty"`
}

type jsonView struct {
	Package  string         `json:"package"`
	DryRun   bool           `json:"dryRun"`
	Changes  []jsonChange   `json:"changes"`
	Summary  map[string]int `json:"summary"`
	Requires []string       `json:"requires,omitempty"`
}

// splitOfferKey undoes the composite productId/basePlanId/offerId display key
// the offer diff is computed under.
func splitOfferKey(composite string) (productID, basePlanID, offerID string) {
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
		pid, bid, oid := splitOfferKey(c.ProductID)
		changes = append(changes, jsonChange{Op: "create", Kind: "offer", ProductID: pid, BasePlanID: bid, OfferID: oid})
	}
	for _, c := range p.Plan.OfferPatches {
		pid, bid, oid := splitOfferKey(c.ProductID)
		changes = append(changes, jsonChange{Op: "patch", Kind: "offer", ProductID: pid, BasePlanID: bid, OfferID: oid, Fields: c.Fields})
	}
	for _, s := range p.Plan.StateChanges {
		op := "activate"
		if s.To == "INACTIVE" {
			op = "deactivate"
		}
		changes = append(changes, jsonChange{Op: op, Kind: s.Kind, ProductID: s.ProductID, BasePlanID: s.BasePlanID, OfferID: s.OfferID, From: s.From, To: s.To})
	}
	for _, c := range p.Plan.OfferDeletes {
		pid, bid, oid := splitOfferKey(c.ProductID)
		changes = append(changes, jsonChange{Op: "delete", Kind: "offer", ProductID: pid, BasePlanID: bid, OfferID: oid})
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
			"state":       len(p.Plan.StateChanges),
			"unchanged":   len(p.Plan.Unchanged),
		},
		Requires: p.Requires,
	})
}

// planStates computes the lifecycle transitions (slice #369): for every base
// plan and offer whose file declares a state, compare with the live state
// (DRAFT for anything being created) and plan the activate/deactivate. A
// declared state the endpoints cannot reach (DRAFT from anything, INACTIVE
// from DRAFT) is a usage error naming the impossible transition; a file that
// omits state declares nothing (missing = unmanaged, the metadata stance).
func planStates(local map[string]json.RawMessage, liveItems []subscriptions.Item, liveOffers []subscriptions.OfferItem, localOffers map[subscriptionscmd.OfferKey]json.RawMessage, plan *reconcile.Plan) error {
	deletedProducts := map[string]bool{}
	for _, c := range plan.Deletes {
		deletedProducts[c.ProductID] = true
	}

	liveBPState := map[string]string{} // productId/basePlanId → state
	for _, it := range liveItems {
		var sub struct {
			BasePlans []struct {
				BasePlanID string `json:"basePlanId"`
				State      string `json:"state"`
			} `json:"basePlans"`
		}
		if err := json.Unmarshal(it.Raw, &sub); err != nil {
			return fmt.Errorf("decode live subscription %q: %w", it.ProductID, err)
		}
		for _, bp := range sub.BasePlans {
			liveBPState[it.ProductID+"/"+bp.BasePlanID] = bp.State
		}
	}

	for productID, raw := range local {
		if deletedProducts[productID] {
			continue
		}
		var sub struct {
			BasePlans []struct {
				BasePlanID string `json:"basePlanId"`
				State      string `json:"state"`
			} `json:"basePlans"`
		}
		if err := json.Unmarshal(raw, &sub); err != nil {
			return fmt.Errorf("decode declared subscription %q: %w", productID, err)
		}
		for _, bp := range sub.BasePlans {
			live, ok := liveBPState[productID+"/"+bp.BasePlanID]
			if !ok {
				live = "DRAFT" // being created by this very plan
			}
			sc, err := stateTransition("basePlan", productID, bp.BasePlanID, "", bp.State, live)
			if err != nil {
				return err
			}
			if sc != nil {
				plan.StateChanges = append(plan.StateChanges, *sc)
			}
		}
	}

	liveOfferState := map[subscriptionscmd.OfferKey]string{}
	for _, o := range liveOffers {
		var frag struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal(o.Raw, &frag); err != nil {
			return fmt.Errorf("decode live offer %s/%s/%s: %w", o.ProductID, o.BasePlanID, o.OfferID, err)
		}
		liveOfferState[subscriptionscmd.OfferKey{ProductID: o.ProductID, BasePlanID: o.BasePlanID, OfferID: o.OfferID}] = frag.State
	}
	for _, key := range subscriptionscmd.SortedOfferKeys(localOffers) {
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
		sc, err := stateTransition("offer", key.ProductID, key.BasePlanID, key.OfferID, frag.State, live)
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
// planned change (nil when nothing to do).
func stateTransition(kind, productID, basePlanID, offerID, declared, live string) (*reconcile.StateChange, error) {
	if declared == "" || declared == live {
		return nil, nil
	}
	target := productID + "/" + basePlanID
	if offerID != "" {
		target += "/" + offerID
	}
	switch declared {
	case "ACTIVE":
		// reachable from DRAFT and INACTIVE via :activate
	case "INACTIVE":
		if live != "ACTIVE" {
			return nil, exit.Usagef("%s %s declares state INACTIVE while live is %s — only an ACTIVE %s can be deactivated; fix the state: field or activate it first", kind, target, live, kind)
		}
	default:
		return nil, exit.Usagef("%s %s declares state %q while live is %s — the API can only reach ACTIVE (:activate) or INACTIVE (:deactivate); fix the state: field", kind, target, declared, live)
	}
	return &reconcile.StateChange{Kind: kind, ProductID: productID, BasePlanID: basePlanID, OfferID: offerID, From: live, To: declared}, nil
}

// Run is the business function the kernel invokes: read the declared catalog,
// list the live one (subscriptions + offers), compute the three-level plan,
// then print (--dry-run), refuse (destructive without --confirm) or execute it.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	dir := in.Dir
	if dir == "" {
		dir = subscriptionscmd.DefaultDir
	}
	local, err := catalog.Read(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, exit.Usagef("catalog directory %q does not exist — run gplay subscriptions pull first, or pass --dir", dir)
		}
		return nil, err
	}
	localOffers, err := subscriptionscmd.ExtractOffers(local)
	if err != nil {
		return nil, err
	}
	pkg, err := subscriptionscmd.ResolvePackage(rc, in.Package)
	if err != nil {
		return nil, err
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	items, err := subscriptions.List(rc.Ctx, httpClient, pkg)
	if err != nil {
		return nil, subscriptionscmd.Classify(pkg, err)
	}
	live := make(map[string]json.RawMessage, len(items))
	for _, it := range items {
		live[it.ProductID] = it.Raw
	}

	// A declared catalog that is empty while Play holds subscriptions is far
	// more likely a mis-pointed --dir than an intent to delete the whole
	// catalog — refuse even under --dry-run (the metadata --prune empty-tree
	// guard, ported).
	if len(local) == 0 && len(live) > 0 {
		return nil, exit.Usagef("catalog directory %q holds no .json catalog files while %q has %d live subscription(s) — applying it would delete them all; run gplay subscriptions pull first, or pass the right --dir", dir, pkg, len(live))
	}

	liveOffers, err := subscriptions.ListAllOffers(rc.Ctx, httpClient, pkg)
	if err != nil {
		return nil, subscriptionscmd.Classify(pkg, err)
	}

	plan, err := reconcile.Compute(local, live, managedFields)
	if err != nil {
		return nil, err
	}

	// Offer diff: same engine, composite keys. Offers of a subscription the
	// plan deletes are not individually deleted — the parent delete takes them.
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
			continue
		}
		liveComposite[subscriptionscmd.OfferKey{ProductID: o.ProductID, BasePlanID: o.BasePlanID, OfferID: o.OfferID}.String()] = o.Raw
	}
	offerPlan, err := reconcile.Compute(localComposite, liveComposite, offerManagedFields)
	if err != nil {
		return nil, err
	}
	plan.OfferCreates, plan.OfferPatches, plan.OfferDeletes = offerPlan.Creates, offerPlan.Patches, offerPlan.Deletes

	if err := planStates(local, items, liveOffers, localOffers, &plan); err != nil {
		return nil, err
	}

	var requires []string
	if plan.HasDeletes() {
		requires = []string{"confirm"}
	}
	if in.DryRun || !plan.HasChanges() {
		// DryRun reflects the invocation, not the branch: a real apply that
		// found nothing to do reports dryRun:false with zero changes.
		return Payload{Package: pkg, Dir: dir, Plan: plan, DryRun: in.DryRun, Requires: requires}, nil
	}
	if plan.HasDeletes() && !in.Confirm {
		return nil, exit.SafetyFlag("confirm", "this plan deletes %d subscription(s) and %d offer(s) from the live catalog of %q and deletion cannot be undone; pass --confirm to proceed (rehearse first with --dry-run)", len(plan.Deletes), len(plan.OfferDeletes), pkg)
	}

	regionsVersion := strings.TrimSpace(in.RegionsVersion)
	if regionsVersion == "" {
		regionsVersion = subscriptionscmd.DefaultRegionsVersion
	}
	// Execution order: grow, then move state, then shrink — parent
	// creates/patches, offer creates/patches, activations, deactivations,
	// offer deletes, parent deletes. Not transactional (no batch spans these
	// verbs); a failure surfaces immediately and a re-run converges.
	for _, c := range plan.Creates {
		body, err := subscriptionscmd.StripOffersFromSubscription(local[c.ProductID])
		if err != nil {
			return nil, err
		}
		if _, err := subscriptions.Create(rc.Ctx, httpClient, pkg, c.ProductID, regionsVersion, body); err != nil {
			return nil, subscriptionscmd.Classify(pkg, err)
		}
	}
	for _, c := range plan.Patches {
		body, err := subscriptionscmd.StripOffersFromSubscription(local[c.ProductID])
		if err != nil {
			return nil, err
		}
		if _, err := subscriptions.Patch(rc.Ctx, httpClient, pkg, c.ProductID, regionsVersion, c.Fields, body); err != nil {
			return nil, subscriptionscmd.Classify(pkg, err)
		}
	}
	for _, c := range plan.OfferCreates {
		pid, bid, oid := splitOfferKey(c.ProductID)
		if _, err := subscriptions.CreateOffer(rc.Ctx, httpClient, pkg, pid, bid, oid, regionsVersion, localOffers[subscriptionscmd.OfferKey{ProductID: pid, BasePlanID: bid, OfferID: oid}]); err != nil {
			return nil, subscriptionscmd.Classify(pkg, err)
		}
	}
	for _, c := range plan.OfferPatches {
		pid, bid, oid := splitOfferKey(c.ProductID)
		if _, err := subscriptions.PatchOffer(rc.Ctx, httpClient, pkg, pid, bid, oid, regionsVersion, c.Fields, localOffers[subscriptionscmd.OfferKey{ProductID: pid, BasePlanID: bid, OfferID: oid}]); err != nil {
			return nil, subscriptionscmd.Classify(pkg, err)
		}
	}
	// Activations before deactivations: an offer can only go ACTIVE under an
	// active base plan, and nothing below depends on a deactivation.
	for _, phase := range []string{"ACTIVE", "INACTIVE"} {
		for _, s := range plan.StateChanges {
			if s.To != phase {
				continue
			}
			var err error
			if s.Kind == "basePlan" {
				_, err = subscriptions.SetBasePlanState(rc.Ctx, httpClient, pkg, s.ProductID, s.BasePlanID, s.To == "ACTIVE")
			} else {
				_, err = subscriptions.SetOfferState(rc.Ctx, httpClient, pkg, s.ProductID, s.BasePlanID, s.OfferID, s.To == "ACTIVE")
			}
			if err != nil {
				return nil, subscriptionscmd.Classify(pkg, err)
			}
		}
	}
	for _, c := range plan.OfferDeletes {
		pid, bid, oid := splitOfferKey(c.ProductID)
		if err := subscriptions.DeleteOffer(rc.Ctx, httpClient, pkg, pid, bid, oid); err != nil {
			return nil, subscriptionscmd.Classify(pkg, err)
		}
	}
	for _, c := range plan.Deletes {
		if err := subscriptions.Delete(rc.Ctx, httpClient, pkg, c.ProductID); err != nil {
			return nil, subscriptionscmd.Classify(pkg, err)
		}
	}
	rc.Confirmf("subscriptions applied to %q (%d created, %d patched, %d deleted; offers: %d created, %d patched, %d deleted; %d state change(s))", pkg,
		len(plan.Creates), len(plan.Patches), len(plan.Deletes),
		len(plan.OfferCreates), len(plan.OfferPatches), len(plan.OfferDeletes),
		len(plan.StateChanges))
	return Payload{Package: pkg, Dir: dir, Plan: plan}, nil
}

// NewCommand returns the cobra command for `gplay subscriptions apply`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Reconcile the live subscription catalog to the catalog files",
		Long: `Compute the plan between the on-disk catalog (--dir, default
` + subscriptionscmd.DefaultDir + `) and the app's live subscription catalog —
subscriptions, base plans (config + per-territory prices), offers, and the
declared lifecycle state — then execute it. The directory is the complete
declared catalog: a live subscription or offer with no declaration is a delete
in the plan (mirror semantics — deliberately not the additive stance of
metadata apply).

--dry-run reads live Play and prints the plan without changing anything.
Creates and patches run directly; a plan containing any delete refuses without
--confirm (exit 3, naming the flag) — CI=true never auto-confirms. A base
plan's or offer's "state" field is reconciled through the dedicated
activate/deactivate endpoints (declare ACTIVE or INACTIVE; omit the field to
leave state unmanaged) — state changes are listed prominently in the plan
because they move buyer availability, but are not gated: they are reversible.
Use "subscriptions prices convert" to derive regional prices in bulk. Editing
prices in files affects new purchases only — migrating existing subscribers is
a separate, gated command.

--regions-version pins the regions version sent with creates and patches
(default ` + subscriptionscmd.DefaultRegionsVersion + `, the latest Google has
published). GPLAY_READONLY refuses the command (exit 4).`,
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
	cmd.Flags().StringVar(&in.Dir, "dir", subscriptionscmd.DefaultDir, "catalog directory to reconcile from")
	cmd.Flags().StringVar(&in.RegionsVersion, "regions-version", subscriptionscmd.DefaultRegionsVersion, "regions version pin sent with subscription writes")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "read live Play and print the plan without committing (online)")
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "authorize a destructive plan (required when the plan deletes subscriptions or offers)")
	return cmd
}
