package pull

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/subscriptions/subscriptionscmd"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/monetization/catalog"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/subscriptions"
)

type Input struct {
	Package string
	Dir     string
}

type Payload struct {
	Package string
	Dir     string
	Written []string
	Removed []string
	Raw     json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { _, err := w.Write(p.Raw); return err },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

func (p Payload) renderTable(w io.Writer) error {
	if _, err := fmt.Fprintf(w, "pulled %d subscription(s) into %s\n", len(p.Written), p.Dir); err != nil {
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
	return nil
}

func (p Payload) renderMarkdown(w io.Writer) error {
	if _, err := fmt.Fprintf(w, "## subscriptions pull: %s\n\n", p.Package); err != nil {
		return err
	}
	rows := make([][]string, 0, len(p.Written)+len(p.Removed))
	for _, f := range p.Written {
		rows = append(rows, []string{f, "written"})
	}
	for _, f := range p.Removed {
		rows = append(rows, []string{f, "removed (no longer live)"})
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
		if dir, err = catalog.ResolveDefault(subscriptionscmd.DefaultDir); err != nil {
			return nil, err
		}
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	items, err := subscriptions.List(rc.Ctx, httpClient, pkg)
	if err != nil {
		return nil, subscriptionscmd.Classify(pkg, err)
	}
	if len(items) == 0 {
		if n := catalog.Count(dir); n > 0 {
			return nil, exit.Usagef("live subscription catalog of %q is empty while %q holds %d catalog file(s): refusing to erase the local catalog; verify --package, or delete the files yourself if the empty live catalog is intended", pkg, dir, n)
		}
	}
	offers, err := subscriptions.ListAllOffers(rc.Ctx, httpClient, pkg)
	if err != nil {
		return nil, subscriptionscmd.Classify(pkg, err)
	}
	embedded, err := subscriptionscmd.EmbedOffers(items, offers)
	if err != nil {
		return nil, err
	}
	entries := make([]catalog.Entry, 0, len(items))
	rawSubs := make([]json.RawMessage, 0, len(items))
	written := make([]string, 0, len(items))
	for _, it := range items {
		entries = append(entries, catalog.Entry{ProductID: it.ProductID, Raw: embedded[it.ProductID]})
		rawSubs = append(rawSubs, it.Raw)
		written = append(written, it.ProductID+".json")
	}
	removed, err := catalog.Write(dir, entries)
	if err != nil {
		return nil, err
	}
	merged, err := output.Marshal(struct {
		Subscriptions []json.RawMessage `json:"subscriptions"`
	}{Subscriptions: rawSubs})
	if err != nil {
		return nil, fmt.Errorf("marshal merged response: %w", err)
	}
	return Payload{Package: pkg, Dir: dir, Written: written, Removed: removed, Raw: merged}, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Pull the live subscription catalog into files",
		Long: `Read the app's complete live subscription catalog
(monetization.subscriptions.list plus one wildcard offers walk, every page)
and write it as the on-disk catalog: one <productId>.json file per
subscription under --dir (default ` + subscriptionscmd.DefaultDir + `), the
API resource verbatim with each base plan's offers nested under
basePlans[].offers. Stale .json files for subscriptions no longer live are
removed so the directory mirrors Play; non-.json files are never touched.
Commit the directory, edit the files, then rehearse with
"gplay subscriptions apply --dry-run". --output json stays the merged
ListSubscriptionsResponse (offers travel in the files).`,
		Example: `  # Write the live catalog to ./monetization/subscriptions, then commit it
  gplay subscriptions pull

  gplay subscriptions pull --dir store/subscriptions --package com.example.app`,
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
	cmd.Flags().StringVar(&in.Dir, "dir", subscriptionscmd.DefaultDir, "catalog directory to write")
	return cmd
}
