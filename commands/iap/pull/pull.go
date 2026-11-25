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

type Input struct {
	Package string
	Dir     string
}

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

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}
	dir := in.Dir
	if dir == "" {
		if dir, err = catalog.ResolveDefault(iapcmd.DefaultDir); err != nil {
			return nil, err
		}
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	items, err := iap.ListOneTimeProducts(rc.Ctx, httpClient, pkg)
	if err != nil {
		return nil, iapcmd.Classify(pkg, err)
	}
	legacy, retired, err := iapcmd.ListLegacy(rc.Ctx, httpClient, pkg)
	if err != nil {
		return nil, err
	}
	if retired {
		rc.Notef(iapcmd.LegacyRetiredNote, pkg)
	}
	if len(items) == 0 && len(legacy) == 0 {
		if n := catalog.Count(dir); n > 0 {
			return nil, exit.Usagef("live one-time-product catalog of %q is empty while %q holds %d catalog file(s): refusing to erase the local catalog; verify --package, or delete the files yourself if the empty live catalog is intended", pkg, dir, n)
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
	merged, err := output.Marshal(struct {
		OneTimeProducts []json.RawMessage `json:"oneTimeProducts"`
		InAppProduct    []json.RawMessage `json:"inappproduct"`
	}{OneTimeProducts: rawV2, InAppProduct: rawLegacy})
	if err != nil {
		return nil, fmt.Errorf("marshal merged response: %w", err)
	}
	return Payload{Package: pkg, Dir: dir, Written: written, Removed: removed, Shadowed: shadowed, Raw: merged}, nil
}

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
mirrors Play; non-.json files are never touched. When Google has retired the
legacy API for the app ("Please migrate to the new publishing API"), its whole
catalog lives in the v2 model: pull reads v2 only and says so on stderr.

--output json is a composite of the two verbatim list envelopes
({"oneTimeProducts":[...],"inappproduct":[...]}).`,
		Example: `  # Write the live catalog to ./monetization/iap, then commit it
  gplay iap pull

  gplay iap pull --dir store/iap --package com.example.app`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("dir") {
				in.Dir = ""
			}
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
