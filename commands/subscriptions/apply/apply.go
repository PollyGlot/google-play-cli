// Package apply implements `gplay subscriptions apply`: compute the
// Reconciliation plan between the on-disk Monetization catalog (--dir) and the
// live catalog: subscriptions, their base plans, their offers, and the
// declared lifecycle state of base plans and offers: print it, and execute
// it. Creates and patches run directly; any plan containing a delete
// (subscription, base plan or offer) refuses without --confirm (exit 3,
// ADR-0017 family; CI=true never auto-confirms). --dry-run prints the plan online and stops.
// State transitions ride the dedicated activate/deactivate endpoints, never a
// patch; they are surfaced prominently in every plan view (they move buyer
// availability) but are not gated: they are reversible. The updateMask of
// every patch is exactly the changed managed fields (ADR-0041). Edit-free,
// package axis. MarkMutating so GPLAY_READONLY refuses it (exit 4). --output
// json emits the plan: a gplay-owned shape, a recorded ADR-0003 exception
// like metadata apply. Ships [experimental] (ADR-0010).
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

// managedFields is the subscription-level projection apply reconciles, and
// therefore the widest updateMask a parent patch can ever send (ADR-0041 §5).
// basePlans (slice #368) is declarable config riding the parent patch; its
// output-only state subfield is reconciled via activate/deactivate instead
// (slice #369) and its embedded offers array is a catalog-file construct the
// API resource does not carry: both are normalized out of the comparison. A
// base plan dropped from the file still diffs here (the patch body omits it),
// but the patch never removes it server-side: the removal is a separate
// basePlans.delete in the plan (slice #542).
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
	Package          string
	Dir              string
	RegionsVersion   string
	LatencyTolerance string
	DryRun           bool
	Confirm          bool
}

// Payload renders the Reconciliation plan: planned under --dry-run, executed
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
	v := planview.View{Axis: planview.Subscriptions, Package: p.Package, Plan: p.Plan, DryRun: p.DryRun, Requires: p.Requires}
	return output.Renderers{
		Table:    func(w io.Writer) error { return v.Human(w, false) },
		JSON:     v.JSON,
		Markdown: func(w io.Writer) error { return v.Human(w, true) },
	}
}

// planStates computes the lifecycle transitions (slice #369): for every base
// plan and offer whose file declares a state, compare with the live state
// (DRAFT for anything being created) and plan the activate/deactivate. A
// declared state the endpoints cannot reach (DRAFT from anything, INACTIVE
// from DRAFT) is a usage error naming the impossible transition; a file that
// omits state declares nothing (missing = unmanaged, the metadata stance).
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
				live = subscriptions.StateDraft // being created by this very plan
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
			live = subscriptions.StateDraft // being created by this very plan
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

// liveBasePlanStates indexes the live state of every base plan by its
// productId/basePlanId display key, as the list returned it.
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

// liveOfferStates indexes the live state of every offer, as the offers list
// returned it.
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

// refuseActiveOfferDeletes refuses, while planning, dropping an offer the list
// shows ACTIVE: offers.delete documents DRAFT as the only deletable state, so
// that file would fail on every run (#790). The same sentence refuses an
// INACTIVE offer, but that one is left to the API like an INACTIVE base plan:
// Play words basePlans.delete identically and contradicts it in
// BasePlan.state, so gplay does not block a delete Play might accept.
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

// keepOfferInactiveHint is keepInactiveHint for an offer: the file keeps it
// under its base plan, so no delete is ever attempted again.
const keepOfferInactiveHint = "keep the offer under its base plan in the subscription file with state: INACTIVE instead (apply deactivates it if it is ACTIVE: new subscribers can no longer get it, existing ones keep it, and the next run has nothing to do)"

// refuseActiveBasePlanDeletes refuses, while planning, dropping a base plan
// the list shows ACTIVE: Discovery contradicts itself on which states
// basePlans.delete takes (the method says DRAFT only, BasePlan.state says
// DRAFT or INACTIVE), but neither reading deletes an ACTIVE plan, so that
// file would fail on every run (#778). An INACTIVE plan, or one whose live
// state is absent, is left to the API, which settles the contradiction.
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

// keepInactiveHint is the action that converges for a base plan the API will
// not delete, whichever Discovery reading holds: the file keeps the plan, so
// no delete is ever attempted again.
const keepInactiveHint = "keep the base plan in its subscription file with state: INACTIVE instead (apply deactivates it if it is ACTIVE: new subscribers can no longer buy it, existing ones keep their subscription, and the next run has nothing to do)"

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
	case subscriptions.StateActive:
		// reachable from DRAFT and INACTIVE via :activate
	case subscriptions.StateInactive:
		if live != subscriptions.StateActive {
			return nil, exit.Usagef("%s %s declares state INACTIVE while live is %s: only an ACTIVE %s can be deactivated; fix the state: field or activate it first", kind, target, live, kind)
		}
	default:
		return nil, exit.Usagef("%s %s declares state %q while live is %s: the API can only reach ACTIVE (:activate) or INACTIVE (:deactivate); fix the state: field", kind, target, declared, live)
	}
	return &reconcile.StateChange{Kind: kind, ProductID: productID, BasePlanID: basePlanID, OfferID: offerID, From: live, To: declared}, nil
}

// Run is the business function the kernel invokes: read the declared catalog,
// list the live one (subscriptions + offers), compute the three-level plan,
// then print (--dry-run), refuse (destructive without --confirm) or execute it.
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

	// A declared catalog that is empty while Play holds subscriptions is far
	// more likely a mis-pointed --dir than an intent to delete the whole
	// catalog: refuse even under --dry-run (the metadata --prune empty-tree
	// guard, ported).
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

	// Offer diff: same engine, composite keys. Offers of a subscription the
	// plan deletes are not individually deleted: the parent delete takes them.
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

	// Base-plan shrink (slice #542): a live plan the file dropped. The engine
	// skips deleted and created products on its own.
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
		// DryRun reflects the invocation, not the branch: a real apply that
		// found nothing to do reports dryRun:false with zero changes.
		return Payload{Package: pkg, Dir: dir, Plan: plan, DryRun: in.DryRun, Requires: requires}, nil
	}
	if plan.HasDeletes() && !in.Confirm {
		return nil, exit.SafetyFlag("confirm", "this plan deletes %d subscription(s), %d base plan(s) and %d offer(s) from the live catalog of %q and deletion cannot be undone; pass --confirm to proceed (rehearse first with --dry-run)", len(plan.Deletes), len(plan.BasePlanDeletes), len(plan.OfferDeletes), pkg)
	}

	regionsVersion := strings.TrimSpace(in.RegionsVersion)
	if regionsVersion == "" {
		regionsVersion = subscriptionscmd.DefaultRegionsVersion
	}
	// Execution order: grow, then move state, then shrink: parent
	// creates/patches, offer creates/patches, activations, deactivations,
	// offer deletes, base-plan deletes, parent deletes. Not transactional (no
	// batch spans these verbs); a failure surfaces immediately and a re-run
	// converges. The one exception is an offer or base-plan delete the API
	// refuses (an INACTIVE one, or one published after the list): it is
	// independent of every other change, so the run records it, finishes the
	// plan, and reports it last.
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
	// Activations before deactivations: an offer can only go ACTIVE under an
	// active base plan, and nothing below depends on a deactivation.
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
	// record keeps the non-DRAFT refusal (400, or 403 on some accounts) for
	// the end of the run. Transport, 5xx, 404 are not that refusal: they abort
	// like any other write so a re-run converges.
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
		// Every independent change ran; the plan still did not converge. One
		// refusal drives the envelope (its *api.Error keeps the exit code and
		// the diagnostic code, ADR-0044); the count says how many more. All are
		// non-retryable, so ADR-0040's rule alone keeps the first; it is
		// refined here: a 403 (exit 11) outranks a state refusal (exit 30),
		// whatever the order. The file resolves the state refusal (its hint is
		// on stderr), not a missing grant, which a re-run would hit again.
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

// deleteRefusedError wraps the API's refusal to delete an offer or a base
// plan that is not in DRAFT (400, or 403 on some accounts) with the
// resolution the user actually needs: the file keeps it with state: INACTIVE
// (the kind's hint), the one file that converges. The planner already refuses
// dropping one it saw ACTIVE; this covers an INACTIVE one the API would not
// delete, and one published after the list.
// It carries no ExitCode of its own, so the wrapped *api.Error stays
// authoritative for the exit code, the operation (offers.delete or
// basePlans.delete) and the verbatim upstream reasons. It does declare its own
// diagnostic code (exit.Diagnoser, ADR-0044): on the 400 the envelope says
// OFFER_NOT_DRAFT or BASE_PLAN_NOT_DRAFT (exit 30) rather than the generic
// INVALID_ARGUMENT, so an agent can branch on "keep it as INACTIVE" without
// regexing the message. A 403 is left to the classifier (exit 11 /
// PERMISSION_DENIED): it may be the same refusal on some accounts, but it
// cannot rule out a genuine permission problem, and the hint text still
// travels in the message.
type deleteRefusedError struct {
	pkg, kind, target string
	// documented is what Discovery says about the deletable states.
	documented, hint string
	code             exit.Code
	others           int
	cause            error
}

func (e *deleteRefusedError) Error() string {
	msg := fmt.Sprintf("cannot delete %s %s of %q: the API refused it (%s), so %s; the rest of the plan was applied: %v", e.kind, e.target, e.pkg, e.documented, e.hint, e.cause)
	if e.others > 0 {
		msg += fmt.Sprintf(" (%d more delete(s) were refused, see stderr)", e.others)
	}
	return msg
}
func (e *deleteRefusedError) Unwrap() error { return e.cause }

// DiagnosticCode implements exit.Diagnoser: the kind's code on the 400
// refusal, empty otherwise so the wrapped *api.Error keeps its own code.
func (e *deleteRefusedError) DiagnosticCode() exit.Code {
	var apiErr *api.Error
	if errors.As(e.cause, &apiErr) && apiErr.StatusCode == http.StatusBadRequest {
		return e.code
	}
	return ""
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
