// Package migrate implements `gplay subscriptions prices migrate`: move
// EXISTING subscribers of one base plan to its current price
// (basePlans.migratePrices) — the sole imperative escape hatch of the
// Monetization catalog (ADR-0041 §4). `apply` never reprices a live purchaser;
// this command is how an operator deliberately does, so it is DESTRUCTIVE tier
// (ADR-0017): refuses without --confirm (exit 3, naming the flag), CI=true
// never auto-confirms, MarkMutating so GPLAY_READONLY refuses it (exit 4),
// --dry-run previews the target with no HTTP and lists the gate in `requires`.
// One base plan per invocation — no bulk money-moving (the ADR-0031 stance;
// batchMigratePrices is deliberately not wrapped). `migrate` is a domain verb
// admitted under ADR-0019 §2 (Google's own method name). Ships [experimental]
// (ADR-0010).
package migrate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/subscriptions/subscriptionscmd"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/subscriptions"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package           string
	Product           string
	BasePlan          string
	Regions           []string
	Oldest            string
	PriceIncreaseType string
	RegionsVersion    string
	Confirm           bool
	DryRun            bool
}

// Payload renders what migrate did (or, on --dry-run, would do). Requires is
// the safety gate surfaced under --dry-run (ADR-0017 §4); Raw carries any
// verbatim response body (usually empty, so a gplay-shaped success object is
// emitted instead).
type Payload struct {
	Product  string
	BasePlan string
	Regions  []string
	Oldest   string
	Requires []string
	DryRun   bool
	Raw      json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderHuman(w, false) },
		JSON:     func(w io.Writer) error { return p.renderJSON(w) },
		Markdown: func(w io.Writer) error { return p.renderHuman(w, true) },
	}
}

func (p Payload) verb() string {
	if p.DryRun {
		return "would migrate"
	}
	return "migrated"
}

func (p Payload) renderHuman(w io.Writer, markdown bool) error {
	if markdown {
		if _, err := fmt.Fprintf(w, "## subscriptions prices migrate\n\n"); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "%s existing subscribers of %s/%s to the current price [regions: %s; cohorts older than %s]\n",
		p.verb(), p.Product, p.BasePlan, strings.Join(p.Regions, ", "), p.Oldest); err != nil {
		return err
	}
	if len(p.Requires) > 0 {
		if _, err := fmt.Fprintf(w, "requires: %s\n", strings.Join(p.Requires, ", ")); err != nil {
			return err
		}
	}
	return nil
}

type dryRunView struct {
	DryRun   bool     `json:"dryRun"`
	Product  string   `json:"productId"`
	BasePlan string   `json:"basePlanId"`
	Regions  []string `json:"regions"`
	Oldest   string   `json:"oldestAllowedPriceVersionTime"`
	Requires []string `json:"requires"`
}

func (p Payload) renderJSON(w io.Writer) error {
	if p.DryRun {
		return output.WriteJSON(w, dryRunView{DryRun: true, Product: p.Product, BasePlan: p.BasePlan, Regions: p.Regions, Oldest: p.Oldest, Requires: p.Requires})
	}
	// migratePrices returns an empty body on success; emit a gplay-shaped
	// success object so --output json (the CI default) is always parseable
	// (the documented ADR-0003 exception family, as in `orders refund`).
	if len(bytes.TrimSpace(p.Raw)) > 2 { // more than bare {}
		_, err := w.Write(p.Raw)
		return err
	}
	return output.WriteJSON(w, struct {
		OK       bool     `json:"ok"`
		Product  string   `json:"productId"`
		BasePlan string   `json:"basePlanId"`
		Regions  []string `json:"regions"`
	}{OK: true, Product: p.Product, BasePlan: p.BasePlan, Regions: p.Regions})
}

// priceIncreaseTypes maps the operator-friendly flag values to the API enum.
var priceIncreaseTypes = map[string]string{
	"":                            "",
	"opt-in":                      "PRICE_INCREASE_TYPE_OPT_IN",
	"opt-out":                     "PRICE_INCREASE_TYPE_OPT_OUT",
	"PRICE_INCREASE_TYPE_OPT_IN":  "PRICE_INCREASE_TYPE_OPT_IN",
	"PRICE_INCREASE_TYPE_OPT_OUT": "PRICE_INCREASE_TYPE_OPT_OUT",
}

// Run is the business function the kernel invokes. It validates offline,
// enforces the destructive --confirm gate (unless --dry-run previews) and
// migrates the subscribers.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	product := strings.TrimSpace(in.Product)
	basePlan := strings.TrimSpace(in.BasePlan)
	if product == "" || basePlan == "" {
		return nil, exit.Usagef("no target — pass --product <productId> and --base-plan <basePlanId>")
	}
	regions := make([]string, 0, len(in.Regions))
	for _, r := range in.Regions {
		r = strings.ToUpper(strings.TrimSpace(r))
		if r == "" {
			continue
		}
		regions = append(regions, r)
	}
	if len(regions) == 0 {
		return nil, exit.Usagef("no regions — pass at least one --region <ISO-3166-2 code> whose price cohorts should migrate")
	}
	oldest := strings.TrimSpace(in.Oldest)
	if _, err := time.Parse(time.RFC3339, oldest); err != nil {
		return nil, exit.Usagef("invalid --oldest %q — pass an RFC-3339 timestamp (e.g. 2026-01-01T00:00:00Z); subscribers in price cohorts older than it migrate", oldest)
	}
	increaseType, ok := priceIncreaseTypes[strings.TrimSpace(in.PriceIncreaseType)]
	if !ok {
		return nil, exit.Usagef("invalid --price-increase-type %q — pass opt-in or opt-out", in.PriceIncreaseType)
	}

	// migrating live subscribers is unconditionally money-moving, so the gate
	// is always a single required flag.
	requires := []string{"confirm"}

	if in.DryRun {
		return Payload{Product: product, BasePlan: basePlan, Regions: regions, Oldest: oldest, Requires: requires, DryRun: true}, nil
	}
	if !in.Confirm {
		return nil, exit.SafetyFlag("confirm", "migrating existing subscribers of %s/%s changes what live purchasers pay and cannot be undone; pass --confirm to proceed (rehearse first with --dry-run)", product, basePlan)
	}

	pkg, err := subscriptionscmd.ResolvePackage(rc, in.Package)
	if err != nil {
		return nil, err
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	regionsVersion := strings.TrimSpace(in.RegionsVersion)
	if regionsVersion == "" {
		regionsVersion = subscriptionscmd.DefaultRegionsVersion
	}
	migrations := make([]subscriptions.RegionalPriceMigration, 0, len(regions))
	for _, r := range regions {
		migrations = append(migrations, subscriptions.RegionalPriceMigration{
			RegionCode:                    r,
			OldestAllowedPriceVersionTime: oldest,
			PriceIncreaseType:             increaseType,
		})
	}
	raw, err := subscriptions.MigrateBasePlanPrices(rc.Ctx, httpClient, pkg, product, basePlan, subscriptions.MigrateBasePlanPricesRequest{
		PackageName:             pkg,
		ProductID:               product,
		BasePlanID:              basePlan,
		RegionalPriceMigrations: migrations,
		RegionsVersion:          subscriptions.RegionsVersion{Version: regionsVersion},
	})
	if err != nil {
		return nil, subscriptionscmd.Classify(pkg, err)
	}
	rc.Confirmf("migrated existing subscribers of %s/%s for %q (%d region(s))", product, basePlan, pkg, len(regions))
	return Payload{Product: product, BasePlan: basePlan, Regions: regions, Oldest: oldest, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay subscriptions prices migrate`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "[experimental] Migrate existing subscribers to the current price (money-moving)",
		Long: `Migrate EXISTING subscribers of one base plan to its current price
(basePlans.migratePrices) — the one deliberate exception to the rule that
editing catalog files never touches a live purchaser: "subscriptions apply"
changes what NEW buyers pay, this command changes what EXISTING subscribers
pay. Subscribers in price cohorts older than --oldest migrate, per --region.

This is money-moving, so it refuses without --confirm (exit 3, naming the
flag); CI=true never auto-confirms. Use --dry-run to preview the target with
no HTTP call — under --output json the preview lists the gate in a "requires"
array. GPLAY_READONLY refuses it (exit 4). One base plan per invocation —
there is deliberately no bulk migration.

--price-increase-type opt-in requires subscribers to accept the new price or
churn; opt-out (where Google allows it) applies it automatically with notice.

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
	cmd.Flags().StringVar(&in.Product, "product", "", "subscription product ID (required)")
	cmd.Flags().StringVar(&in.BasePlan, "base-plan", "", "base plan ID whose subscribers migrate (required)")
	cmd.Flags().StringArrayVar(&in.Regions, "region", nil, "region code to migrate (repeatable, at least one)")
	cmd.Flags().StringVar(&in.Oldest, "oldest", "", "RFC-3339 cutoff: price cohorts older than this migrate (required)")
	cmd.Flags().StringVar(&in.PriceIncreaseType, "price-increase-type", "", "opt-in or opt-out (default: API decides)")
	cmd.Flags().StringVar(&in.RegionsVersion, "regions-version", subscriptionscmd.DefaultRegionsVersion, "regions version pin")
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "authorize the migration (required — this changes what live subscribers pay)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "preview the target without any HTTP call (reports the --confirm requirement)")
	return cmd
}
