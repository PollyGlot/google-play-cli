package apply

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/subscriptions/subscriptionscmd"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/monetization/catalog"
	"github.com/PollyGlot/google-play-cli/internal/monetization/planview"
	"github.com/PollyGlot/google-play-cli/internal/monetization/reconcile"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/latency"
	"github.com/PollyGlot/google-play-cli/internal/play/subscriptions"
)

var managedFields = []reconcile.Field{
	{Name: "listings"},
	{Name: "taxAndComplianceSettings"},
	{Name: "restrictedPaymentCountries"},
	{Name: "basePlans", Normalize: reconcile.StripKeys("state", "offers")},
}

var offerManagedFields = []reconcile.Field{
	{Name: "phases"},
	{Name: "regionalConfigs"},
	{Name: "offerTags"},
	{Name: "targeting"},
	{Name: "otherRegionsConfig"},
}

type Input struct {
	Package          string
	Dir              string
	RegionsVersion   string
	LatencyTolerance string
	DryRun           bool
	Confirm          bool
}

type Payload struct {
	Package  string
	Dir      string
	Plan     reconcile.Plan
	DryRun   bool
	Requires []string
}

func (p Payload) Renderers() output.Renderers {
	v := planview.View{Axis: planview.Subscriptions, Package: p.Package, Plan: p.Plan, DryRun: p.DryRun, Requires: p.Requires}
	return output.Renderers{
		Table:    func(w io.Writer) error { return v.Human(w, false) },
		JSON:     v.JSON,
		Markdown: func(w io.Writer) error { return v.Human(w, true) },
	}
}

func planStates(local map[string]json.RawMessage, liveBPState map[string]string, liveOfferState map[subscriptionscmd.OfferKey]string, localOffers map[subscriptionscmd.OfferKey]json.RawMessage, plan *reconcile.Plan) error {
	deletedProducts := map[string]bool{}
	for _, c := range plan.Deletes {
		deletedProducts[c.ProductID] = true
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
				live = subscriptions.StateDraft
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
			live = subscriptions.StateDraft
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

func liveBasePlanStates(liveItems []subscriptions.Item) (map[string]string, error) {
	states := map[string]string{}
	for _, it := range liveItems {
		var sub struct {
			BasePlans []struct {
				BasePlanID string `json:"basePlanId"`
				State      string `json:"state"`
			} `json:"basePlans"`
		}
		if err := json.Unmarshal(it.Raw, &sub); err != nil {
			return nil, fmt.Errorf("decode live subscription %q: %w", it.ProductID, err)
		}
		for _, bp := range sub.BasePlans {
			states[it.ProductID+"/"+bp.BasePlanID] = bp.State
		}
	}
	return states, nil
}

func liveOfferStates(liveOffers []subscriptions.OfferItem) (map[subscriptionscmd.OfferKey]string, error) {
	states := map[subscriptionscmd.OfferKey]string{}
	for _, o := range liveOffers {
		var frag struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal(o.Raw, &frag); err != nil {
			return nil, fmt.Errorf("decode live offer %s/%s/%s: %w", o.ProductID, o.BasePlanID, o.OfferID, err)
		}
		states[subscriptionscmd.OfferKey{ProductID: o.ProductID, BasePlanID: o.BasePlanID, OfferID: o.OfferID}] = frag.State
	}
	return states, nil
}

func refuseActiveOfferDeletes(deletes []reconcile.Change, liveOfferState map[subscriptionscmd.OfferKey]string) error {
	var active []string
	for _, c := range deletes {
		if liveOfferState[subscriptionscmd.OfferKey{ProductID: c.ProductID, BasePlanID: c.ParentID, OfferID: c.OfferID}] == subscriptions.StateActive {
			active = append(active, c.Key())
		}
	}
	if len(active) == 0 {
		return nil
	}
	return exit.Usagef("the catalog files drop %d ACTIVE offer(s): %s; offers.delete only takes a DRAFT offer, so for each, %s", len(active), strings.Join(active, ", "), keepOfferInactiveHint)
}

const keepOfferInactiveHint = "keep the offer under its base plan in the subscription file with state: INACTIVE instead (apply deactivates it if it is ACTIVE: new subscribers can no longer get it, existing ones keep it, and the next run has nothing to do)"

func refuseActiveBasePlanDeletes(deletes []reconcile.Change, liveBPState map[string]string) error {
	var active []string
	for _, c := range deletes {
		if liveBPState[c.Key()] == subscriptions.StateActive {
			active = append(active, c.Key())
		}
	}
	if len(active) == 0 {
		return nil
	}
	return exit.Usagef("the catalog files drop %d ACTIVE base plan(s): %s; basePlans.delete never takes an ACTIVE base plan (Discovery documents DRAFT, or at most DRAFT and INACTIVE), so for each, %s", len(active), strings.Join(active, ", "), keepInactiveHint)
}

const keepInactiveHint = "keep the base plan in its subscription file with state: INACTIVE instead (apply deactivates it if it is ACTIVE: new subscribers can no longer buy it, existing ones keep their subscription, and the next run has nothing to do)"

func stateTransition(kind, productID, basePlanID, offerID, declared, live string) (*reconcile.StateChange, error) {
	if declared == "" || declared == live {
		return nil, nil
	}
	target := productID + "/" + basePlanID
	if offerID != "" {
		target += "/" + offerID
	}
	switch declared {
	case subscriptions.StateActive:
	case subscriptions.StateInactive:
		if live != subscriptions.StateActive {
			return nil, exit.Usagef("%s %s declares state INACTIVE while live is %s: only an ACTIVE %s can be deactivated; fix the state: field or activate it first", kind, target, live, kind)
		}
	default:
		return nil, exit.Usagef("%s %s declares state %q while live is %s: the API can only reach ACTIVE (:activate) or INACTIVE (:deactivate); fix the state: field", kind, target, declared, live)
	}
	return &reconcile.StateChange{Kind: kind, ProductID: productID, BasePlanID: basePlanID, OfferID: offerID, From: live, To: declared}, nil
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	lt, err := latency.Parse(in.LatencyTolerance)
	if err != nil {
		return nil, err
	}
	dir := in.Dir
	if dir == "" {
		dir = subscriptionscmd.DefaultDir
	}
	local, err := catalog.Read(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, exit.Usagef("catalog directory %q does not exist: run gplay subscriptions pull first, or pass --dir", dir)
		}
		return nil, err
	}
	localOffers, err := subscriptionscmd.ExtractOffers(local)
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
	items, err := subscriptions.List(rc.Ctx, httpClient, pkg)
	if err != nil {
		return nil, subscriptionscmd.Classify(pkg, err)
	}
	live := make(map[string]json.RawMessage, len(items))
	for _, it := range items {
		live[it.ProductID] = it.Raw
	}

	if len(local) == 0 && len(live) > 0 {
		return nil, exit.Usagef("catalog directory %q holds no .json catalog files while %q has %d live subscription(s): applying it would delete them all; run gplay subscriptions pull first, or pass the right --dir", dir, pkg, len(live))
	}

	liveOffers, err := subscriptions.ListAllOffers(rc.Ctx, httpClient, pkg)
	if err != nil {
		return nil, subscriptionscmd.Classify(pkg, err)
	}

	plan, err := reconcile.Compute(local, live, managedFields)
	if err != nil {
		return nil, err
	}

	deletedProducts := map[string]bool{}
	for _, c := range plan.Deletes {
		deletedProducts[c.ProductID] = true
	}
	localKeyed := map[reconcile.Key]json.RawMessage{}
	for key, raw := range localOffers {
		localKeyed[reconcile.Key{ProductID: key.ProductID, ParentID: key.BasePlanID, OfferID: key.OfferID}] = raw
	}
	liveKeyed := map[reconcile.Key]json.RawMessage{}
	for _, o := range liveOffers {
		if deletedProducts[o.ProductID] {
			continue
		}
		liveKeyed[reconcile.Key{ProductID: o.ProductID, ParentID: o.BasePlanID, OfferID: o.OfferID}] = o.Raw
	}
	plan.OfferCreates, plan.OfferPatches, plan.OfferDeletes, err = reconcile.ComputeChildren(localKeyed, liveKeyed, offerManagedFields)
	if err != nil {
		return nil, err
	}

	if plan.BasePlanDeletes, err = reconcile.BasePlanDeletes(local, live); err != nil {
		return nil, err
	}
	liveBPState, err := liveBasePlanStates(items)
	if err != nil {
		return nil, err
	}
	if err := refuseActiveBasePlanDeletes(plan.BasePlanDeletes, liveBPState); err != nil {
		return nil, err
	}

	liveOfferState, err := liveOfferStates(liveOffers)
	if err != nil {
		return nil, err
	}
	if err := refuseActiveOfferDeletes(plan.OfferDeletes, liveOfferState); err != nil {
		return nil, err
	}

	if err := planStates(local, liveBPState, liveOfferState, localOffers, &plan); err != nil {
		return nil, err
	}

	var requires []string
	if plan.HasDeletes() {
		requires = []string{"confirm"}
	}
	if in.DryRun || !plan.HasChanges() {
		return Payload{Package: pkg, Dir: dir, Plan: plan, DryRun: in.DryRun, Requires: requires}, nil
	}
	if plan.HasDeletes() && !in.Confirm {
		return nil, exit.SafetyFlag("confirm", "this plan deletes %d subscription(s), %d base plan(s) and %d offer(s) from the live catalog of %q and deletion cannot be undone; pass --confirm to proceed (rehearse first with --dry-run)", len(plan.Deletes), len(plan.BasePlanDeletes), len(plan.OfferDeletes), pkg)
	}

	regionsVersion := strings.TrimSpace(in.RegionsVersion)
	if regionsVersion == "" {
		regionsVersion = subscriptionscmd.DefaultRegionsVersion
	}
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
		if _, err := subscriptions.Patch(rc.Ctx, httpClient, pkg, c.ProductID, regionsVersion, c.Fields, body, lt); err != nil {
			return nil, subscriptionscmd.Classify(pkg, err)
		}
	}
	for _, c := range plan.OfferCreates {
		if _, err := subscriptions.CreateOffer(rc.Ctx, httpClient, pkg, c.ProductID, c.ParentID, c.OfferID, regionsVersion, localOffers[subscriptionscmd.OfferKey{ProductID: c.ProductID, BasePlanID: c.ParentID, OfferID: c.OfferID}]); err != nil {
			return nil, subscriptionscmd.Classify(pkg, err)
		}
	}
	for _, c := range plan.OfferPatches {
		if _, err := subscriptions.PatchOffer(rc.Ctx, httpClient, pkg, c.ProductID, c.ParentID, c.OfferID, regionsVersion, c.Fields, localOffers[subscriptionscmd.OfferKey{ProductID: c.ProductID, BasePlanID: c.ParentID, OfferID: c.OfferID}], lt); err != nil {
			return nil, subscriptionscmd.Classify(pkg, err)
		}
	}
	for _, phase := range []string{subscriptions.StateActive, subscriptions.StateInactive} {
		for _, s := range plan.StateChanges {
			if s.To != phase {
				continue
			}
			var err error
			if s.Kind == "basePlan" {
				_, err = subscriptions.SetBasePlanState(rc.Ctx, httpClient, pkg, s.ProductID, s.BasePlanID, s.To == subscriptions.StateActive, lt)
			} else {
				_, err = subscriptions.SetOfferState(rc.Ctx, httpClient, pkg, s.ProductID, s.BasePlanID, s.OfferID, s.To == subscriptions.StateActive, lt)
			}
			if err != nil {
				return nil, subscriptionscmd.Classify(pkg, err)
			}
		}
	}
	var refused []*deleteRefusedError
	record := func(err error, refusal deleteRefusedError) error {
		var apiErr *api.Error
		if !errors.As(err, &apiErr) || (apiErr.StatusCode != http.StatusBadRequest && apiErr.StatusCode != http.StatusForbidden) {
			return subscriptionscmd.Classify(pkg, err)
		}
		refusal.pkg, refusal.cause = pkg, err
		rc.Warnf("%v", &refusal)
		refused = append(refused, &refusal)
		return nil
	}
	for _, c := range plan.OfferDeletes {
		if err := subscriptions.DeleteOffer(rc.Ctx, httpClient, pkg, c.ProductID, c.ParentID, c.OfferID); err != nil {
			if err := record(err, deleteRefusedError{kind: "offer", target: c.Key(), documented: "offers.delete documents DRAFT as the only deletable state", hint: keepOfferInactiveHint, code: exit.CodeOfferNotDraft}); err != nil {
				return nil, err
			}
		}
	}
	for _, c := range plan.BasePlanDeletes {
		if err := subscriptions.DeleteBasePlan(rc.Ctx, httpClient, pkg, c.ProductID, c.ParentID); err != nil {
			if err := record(err, deleteRefusedError{kind: "base plan", target: c.Key(), documented: "basePlans.delete documents DRAFT as the only deletable state, though BasePlan.state also lists INACTIVE", hint: keepInactiveHint, code: exit.CodeBasePlanNotDraft}); err != nil {
				return nil, err
			}
		}
	}
	for _, c := range plan.Deletes {
		if err := subscriptions.Delete(rc.Ctx, httpClient, pkg, c.ProductID); err != nil {
			return nil, subscriptionscmd.Classify(pkg, err)
		}
	}
	if len(refused) > 0 {
		lead := refused[0]
		for _, r := range refused {
			if r.DiagnosticCode() == "" {
				lead = r
				break
			}
		}
		lead.others = len(refused) - 1
		return nil, lead
	}
	rc.Confirmf("subscriptions applied to %q (%d created, %d patched, %d deleted; %d base plan(s) deleted; offers: %d created, %d patched, %d deleted; %d state change(s))", pkg,
		len(plan.Creates), len(plan.Patches), len(plan.Deletes), len(plan.BasePlanDeletes),
		len(plan.OfferCreates), len(plan.OfferPatches), len(plan.OfferDeletes),
		len(plan.StateChanges))
	return Payload{Package: pkg, Dir: dir, Plan: plan}, nil
}

type deleteRefusedError struct {
	pkg, kind, target string
	documented, hint  string
	code              exit.Code
	others            int
	cause             error
}

func (e *deleteRefusedError) Error() string {
	msg := fmt.Sprintf("cannot delete %s %s of %q: the API refused it (%s), so %s; the rest of the plan was applied: %v", e.kind, e.target, e.pkg, e.documented, e.hint, e.cause)
	if e.others > 0 {
		msg += fmt.Sprintf(" (%d more delete(s) were refused, see stderr)", e.others)
	}
	return msg
}
func (e *deleteRefusedError) Unwrap() error { return e.cause }

func (e *deleteRefusedError) DiagnosticCode() exit.Code {
	var apiErr *api.Error
	if errors.As(e.cause, &apiErr) && apiErr.StatusCode == http.StatusBadRequest {
		return e.code
	}
	return ""
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Reconcile the live subscription catalog to the catalog files",
		Long: `Compute the plan between the on-disk catalog (--dir, default
` + subscriptionscmd.DefaultDir + `) and the app's live subscription catalog:
subscriptions, base plans (config + per-territory prices), offers, and the
declared lifecycle state, then execute it. The directory is the complete
declared catalog: a live subscription, base plan or offer with no declaration
is a delete in the plan (mirror semantics: deliberately not the additive
stance of metadata apply). The API never deletes an ACTIVE base plan:
dropping one is refused while planning (exit 2, before any write); keep it in
the file with state: INACTIVE instead, which stops new purchases and leaves
the next run nothing to do. Dropping an INACTIVE base plan is sent to the API,
which decides (its reference documents DRAFT as the only deletable state in
one place, DRAFT and INACTIVE in another): a refusal is reported after the
rest of the plan has run, with the same keep-as-INACTIVE hint. Offers follow
the same rule: dropping an ACTIVE offer is refused while planning, and an
INACTIVE offer Play refuses to delete is reported last (OFFER_NOT_DRAFT).

--dry-run reads live Play and prints the plan without changing anything.
Creates and patches run directly; a plan containing any delete refuses without
--confirm (exit 3, naming the flag): CI=true never auto-confirms. A base
plan's or offer's "state" field is reconciled through the dedicated
activate/deactivate endpoints (declare ACTIVE or INACTIVE; omit the field to
leave state unmanaged): state changes are listed prominently in the plan
because they move buyer availability, but are not gated: they are reversible.
Use "subscriptions prices convert" to derive regional prices in bulk. Editing
prices in files affects new purchases only: migrating existing subscribers is
a separate, gated command.

--regions-version pins the regions version sent with creates and patches
(default ` + subscriptionscmd.DefaultRegionsVersion + `, the latest Google has
published).

--latency-tolerance tolerant trades propagation speed for throughput on a
large catalog: Play takes up to 24 hours to show the writes to buyers, against
minutes by default. It rides the patches and the activate/deactivate calls;
Play takes no such parameter on creates and deletes, which keep the default.
GPLAY_READONLY refuses the command (exit 4).`,
		Example: `  # Show the plan against live Play (online, nothing changes)
  gplay subscriptions apply --dry-run

  # Run it, deletes included
  gplay subscriptions apply --confirm

  gplay subscriptions apply --dir store/subscriptions --dry-run --output json`,
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
	cmd.Flags().StringVar(&in.LatencyTolerance, latency.Flag, "", latency.Help)
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "read live Play and print the plan without committing (online)")
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "authorize a destructive plan (required when the plan deletes subscriptions, base plans or offers)")
	return cmd
}
