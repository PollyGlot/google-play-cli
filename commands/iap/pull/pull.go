// Package pull implements `gplay iap pull`: read the complete live
// one-time-product catalog from BOTH surfaces: the v2
// monetization.onetimeproducts model (with each purchase option's offers
// nested from one wildcard walk) AND the read-only legacy inappproducts, and
// write their union as the on-disk catalog, one <productId>.json per product,
// deduped by product ID with v2 winning (an unmigrated legacy product is
// invisible to the v2 list, so a v2-only pull would strand it: ADR-0041 §8).
// A legacy file is recognizable by its `sku` field; apply never writes legacy
// in place. Edit-free, package axis. Ships [experimental] (ADR-0010).
package pull

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/iap/iapcmd"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/monetization/catalog"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/iap"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package string
	Dir     string
}

// Payload renders what pull wrote. Raw is a composite of the two verbatim
// list envelopes (the apps view precedent: no single API response covers a
// union); Shadowed names legacy products hidden by a v2 product of the same
// ID.
type Payload struct {
	Package  string
	Dir      string
	Written  []string
	Removed  []string
	Shadowed []string
	Raw      json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { _, err := w.Write(p.Raw); return err },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

func (p Payload) renderTable(w io.Writer) error {
	if _, err := fmt.Fprintf(w, "pulled %d one-time product(s) into %s\n", len(p.Written), p.Dir); err != nil {
		return err
	}
	for _, f := range p.Written {
		if _, err := fmt.Fprintf(w, "  wrote %s\n", f); err != nil {
			return err
		}
	}
	for _, f := range p.Removed {
		if _, err := fmt.Fprintf(w, "  removed %s (no longer live)\n", f); err != nil {
			return err
		}
	}
	for _, id := range p.Shadowed {
		if _, err := fmt.Fprintf(w, "  note: legacy product %s is shadowed by the v2 product of the same ID (v2 written)\n", id); err != nil {
			return err
		}
	}
	return nil
}

func (p Payload) renderMarkdown(w io.Writer) error {
	if _, err := fmt.Fprintf(w, "## iap pull: %s\n\n", p.Package); err != nil {
		return err
	}
	rows := make([][]string, 0, len(p.Written)+len(p.Removed))
	for _, f := range p.Written {
		rows = append(rows, []string{f, "written"})
	}
	for _, f := range p.Removed {
		rows = append(rows, []string{f, "removed (no longer live)"})
	}
	for _, id := range p.Shadowed {
		rows = append(rows, []string{id + ".json", "legacy shadowed by v2 (v2 written)"})
	}
	return output.MarkdownTable(w, []string{"FILE", "RESULT"}, rows)
}

// Run is the business function the kernel invokes: list both live surfaces to
// completion, union them (v2 wins), mirror into --dir.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg, err := iapcmd.ResolvePackage(rc, in.Package)
	if err != nil {
		return nil, err
	}
	dir := in.Dir
	if dir == "" {
		dir = iapcmd.DefaultDir
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	items, err := iap.ListOneTimeProducts(rc.Ctx, httpClient, pkg)
	if err != nil {
		return nil, iapcmd.Classify(pkg, err)
	}
	legacy, err := iap.ListInAppProducts(rc.Ctx, httpClient, pkg)
	if err != nil {
		return nil, iapcmd.Classify(pkg, err)
	}
	// Mirror semantics make pull destructive locally: refuse to erase a
	// populated directory on an unexpectedly-empty live catalog (the
	// subscriptions pull guard).
	if len(items) == 0 && len(legacy) == 0 {
		if existing, readErr := catalog.Read(dir); readErr == nil && len(existing) > 0 {
			return nil, exit.Usagef("live one-time-product catalog of %q is empty while %q holds %d catalog file(s): refusing to erase the local catalog; verify --package, or delete the files yourself if the empty live catalog is intended", pkg, dir, len(existing))
		}
	}
	offers, err := iap.ListAllOffers(rc.Ctx, httpClient, pkg)
	if err != nil {
		return nil, iapcmd.Classify(pkg, err)
	}
	embedded, err := iapcmd.EmbedOffers(items, offers)
	if err != nil {
		return nil, err
	}

	v2IDs := map[string]bool{}
	entries := make([]catalog.Entry, 0, len(items)+len(legacy))
	written := make([]string, 0, len(items)+len(legacy))
	rawV2 := make([]json.RawMessage, 0, len(items))
	for _, it := range items {
		v2IDs[it.ProductID] = true
		entries = append(entries, catalog.Entry{ProductID: it.ProductID, Raw: embedded[it.ProductID]})
		written = append(written, it.ProductID+".json")
		rawV2 = append(rawV2, it.Raw)
	}
	var shadowed []string
	rawLegacy := make([]json.RawMessage, 0, len(legacy))
	for _, lg := range legacy {
		rawLegacy = append(rawLegacy, lg.Raw)
		if v2IDs[lg.SKU] {
			// The same ID exists in both models: the v2 resource is the
			// forward one, the legacy row is its pre-migration shadow.
			shadowed = append(shadowed, lg.SKU)
			continue
		}
		entries = append(entries, catalog.Entry{ProductID: lg.SKU, Raw: lg.Raw})
		written = append(written, lg.SKU+".json")
	}
	removed, err := catalog.Write(dir, entries)
	if err != nil {
		return nil, err
	}
	// No single API response covers a two-surface union, so the json view is a
	// composite of the two verbatim list envelopes (the `apps view` precedent:
	// each sub-array is upstream bytes, key names mirror the API envelopes).
	merged, err := json.Marshal(struct {
		OneTimeProducts []json.RawMessage `json:"oneTimeProducts"`
		InAppProduct    []json.RawMessage `json:"inappproduct"`
	}{OneTimeProducts: rawV2, InAppProduct: rawLegacy})
	if err != nil {
		return nil, fmt.Errorf("marshal merged response: %w", err)
	}
	return Payload{Package: pkg, Dir: dir, Written: written, Removed: removed, Shadowed: shadowed, Raw: merged}, nil
}

// NewCommand returns the cobra command for `gplay iap pull`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Pull the live one-time-product catalog into files (v2 ∪ legacy)",
		Long: `Read the app's complete live one-time-product catalog from BOTH surfaces:
the v2 model (monetization.onetimeproducts, offers nested under each purchase
option) and the legacy inappproducts, and write their union as the on-disk
catalog: one <productId>.json per product under --dir (default
` + iapcmd.DefaultDir + `). Products live in both models keep the v2 file (the
legacy row is its pre-migration shadow). A legacy file is recognizable by its
"sku" field; "gplay iap apply" writes the v2 model only and never edits a
legacy product in place. Stale .json files are removed so the directory
mirrors Play; non-.json files are never touched.

--output json is a composite of the two verbatim list envelopes
({"oneTimeProducts":[...],"inappproduct":[...]}).`,
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
	cmd.Flags().StringVar(&in.Dir, "dir", iapcmd.DefaultDir, "catalog directory to write")
	return cmd
}
