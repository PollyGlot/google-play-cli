// Package apply implements `gplay subscriptions apply`: compute the
// Reconciliation plan between the on-disk Monetization catalog (--dir) and the
// live subscription catalog, print it, and execute it — creates and patches
// directly, deletes only behind --confirm (exit 3 without it, ADR-0017 family;
// CI=true never auto-confirms). --dry-run prints the plan online and stops.
// The updateMask of every patch is exactly the changed managed fields, so the
// walking skeleton can never clobber basePlans it does not yet manage
// (ADR-0041). Edit-free, package axis. MarkMutating so GPLAY_READONLY refuses
// it (exit 4). --output json emits the plan — a gplay-owned shape, a recorded
// ADR-0003 exception like metadata apply. Ships [experimental] (ADR-0010).
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

// managedFields is the subscription-level projection this slice reconciles —
// and therefore the widest updateMask it can ever send. basePlans joins with
// slice #368, offers with #369 (ADR-0041 §5).
var managedFields = []string{"listings", "taxAndComplianceSettings", "restrictedPaymentCountries"}

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

// verbs returns the per-action labels for the human views.
func (p Payload) verbs() (create, patch, del string) {
	if p.DryRun {
		return "create", "patch", "delete"
	}
	return "created", "patched", "deleted"
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
	create, patch, del := p.verbs()
	header := "applied to"
	if p.DryRun {
		header = "plan for"
	}
	n := len(p.Plan.Creates) + len(p.Plan.Patches) + len(p.Plan.Deletes)
	if _, err := fmt.Fprintf(w, "%s %s (%d change(s)):\n", header, p.Package, n); err != nil {
		return err
	}
	for _, c := range p.Plan.Creates {
		if _, err := fmt.Fprintf(w, "  %s %s\n", create, c.ProductID); err != nil {
			return err
		}
	}
	for _, c := range p.Plan.Patches {
		if _, err := fmt.Fprintf(w, "  %s %s (%s)\n", patch, c.ProductID, strings.Join(c.Fields, ", ")); err != nil {
			return err
		}
	}
	for _, c := range p.Plan.Deletes {
		if _, err := fmt.Fprintf(w, "  %s %s\n", del, c.ProductID); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "summary: create=%d patch=%d delete=%d unchanged=%d\n",
		len(p.Plan.Creates), len(p.Plan.Patches), len(p.Plan.Deletes), len(p.Plan.Unchanged)); err != nil {
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
// shape — ADR-0003 exception — kept flat so a CI gate is one jq line).
type jsonChange struct {
	Op        string   `json:"op"`
	ProductID string   `json:"productId"`
	Fields    []string `json:"fields,omitempty"`
}

type jsonView struct {
	Package  string         `json:"package"`
	DryRun   bool           `json:"dryRun"`
	Changes  []jsonChange   `json:"changes"`
	Summary  map[string]int `json:"summary"`
	Requires []string       `json:"requires,omitempty"`
}

func (p Payload) renderJSON(w io.Writer) error {
	changes := make([]jsonChange, 0, len(p.Plan.Creates)+len(p.Plan.Patches)+len(p.Plan.Deletes))
	for _, c := range p.Plan.Creates {
		changes = append(changes, jsonChange{Op: "create", ProductID: c.ProductID})
	}
	for _, c := range p.Plan.Patches {
		changes = append(changes, jsonChange{Op: "patch", ProductID: c.ProductID, Fields: c.Fields})
	}
	for _, c := range p.Plan.Deletes {
		changes = append(changes, jsonChange{Op: "delete", ProductID: c.ProductID})
	}
	return output.WriteJSON(w, jsonView{
		Package: p.Package,
		DryRun:  p.DryRun,
		Changes: changes,
		Summary: map[string]int{
			"create":    len(p.Plan.Creates),
			"patch":     len(p.Plan.Patches),
			"delete":    len(p.Plan.Deletes),
			"unchanged": len(p.Plan.Unchanged),
		},
		Requires: p.Requires,
	})
}

// Run is the business function the kernel invokes: read the declared catalog,
// list the live one, compute the plan, then print (--dry-run), refuse
// (destructive without --confirm) or execute it.
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

	plan, err := reconcile.Compute(local, live, managedFields)
	if err != nil {
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
		return nil, exit.SafetyFlag("confirm", "this plan deletes %d subscription(s) from the live catalog of %q and deletion cannot be undone; pass --confirm to proceed (rehearse first with --dry-run)", len(plan.Deletes), pkg)
	}

	regionsVersion := strings.TrimSpace(in.RegionsVersion)
	if regionsVersion == "" {
		regionsVersion = subscriptionscmd.DefaultRegionsVersion
	}
	// Execution order: grow before shrinking — creates, then patches, then
	// deletes. A failure surfaces immediately; the plan is not transactional
	// (the API has no batch spanning the three verbs), so re-running apply
	// after a fix converges on the same end state.
	for _, c := range plan.Creates {
		if _, err := subscriptions.Create(rc.Ctx, httpClient, pkg, c.ProductID, regionsVersion, local[c.ProductID]); err != nil {
			return nil, subscriptionscmd.Classify(pkg, err)
		}
	}
	for _, c := range plan.Patches {
		if _, err := subscriptions.Patch(rc.Ctx, httpClient, pkg, c.ProductID, regionsVersion, c.Fields, local[c.ProductID]); err != nil {
			return nil, subscriptionscmd.Classify(pkg, err)
		}
	}
	for _, c := range plan.Deletes {
		if err := subscriptions.Delete(rc.Ctx, httpClient, pkg, c.ProductID); err != nil {
			return nil, subscriptionscmd.Classify(pkg, err)
		}
	}
	rc.Confirmf("subscriptions applied to %q (%d created, %d patched, %d deleted)", pkg,
		len(plan.Creates), len(plan.Patches), len(plan.Deletes))
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
		Short: "[experimental] Reconcile the live subscription catalog to the catalog files",
		Long: `Compute the create/patch/delete plan between the on-disk catalog (--dir,
default ` + subscriptionscmd.DefaultDir + `) and the app's live subscription
catalog, then execute it. The directory is the complete declared catalog: a
live subscription with no file is a delete in the plan (mirror semantics —
deliberately not the additive stance of metadata apply).

--dry-run reads live Play and prints the plan without changing anything.
Creates and patches run directly; a plan containing any delete refuses without
--confirm (exit 3, naming the flag) — CI=true never auto-confirms. Patches send
an updateMask limited to the changed subscription-level fields (listings, tax
and compliance, restricted payment countries), so base plans and offers are
never touched by this command. Editing prices in files affects new purchases
only — migrating existing subscribers is a separate, gated command.

--regions-version pins the regions version sent with creates and patches
(default ` + subscriptionscmd.DefaultRegionsVersion + `, the latest Google has
published). GPLAY_READONLY refuses the command (exit 4).

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
	cmd.Flags().StringVar(&in.Dir, "dir", subscriptionscmd.DefaultDir, "catalog directory to reconcile from")
	cmd.Flags().StringVar(&in.RegionsVersion, "regions-version", subscriptionscmd.DefaultRegionsVersion, "regions version pin sent with subscription writes")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "read live Play and print the plan without committing (online)")
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "authorize a destructive plan (required when the plan deletes subscriptions)")
	return cmd
}
