// Package pull implements `gplay subscriptions pull`: read the complete live
// subscription catalog (monetization.subscriptions.list, followed to
// completion) and write it as the on-disk Monetization catalog — one
// <productId>.json per subscription under --dir, stale catalog files removed,
// so pull-then-apply is a no-op (ADR-0041). Edit-free, package axis. The
// files are the API resources verbatim; --output json passes the merged
// ListSubscriptionsResponse envelope through (ADR-0003). Ships [experimental]
// (ADR-0010).
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

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package string
	Dir     string
}

// Payload renders what pull wrote. Raw is the merged ListSubscriptionsResponse
// envelope for the ADR-0003 pass-through; Written/Removed are the catalog file
// names for the human views.
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
	if _, err := fmt.Fprintf(w, "## subscriptions pull — %s\n\n", p.Package); err != nil {
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

// Run is the business function the kernel invokes: list the live catalog to
// completion, mirror it into --dir, report what changed on disk.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg, err := subscriptionscmd.ResolvePackage(rc, in.Package)
	if err != nil {
		return nil, err
	}
	dir := in.Dir
	if dir == "" {
		dir = subscriptionscmd.DefaultDir
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	items, err := subscriptions.List(rc.Ctx, httpClient, pkg)
	if err != nil {
		return nil, subscriptionscmd.Classify(pkg, err)
	}
	// Mirror semantics make pull destructive locally: an unexpectedly-empty
	// live listing (mis-set --package, scope loss) would erase a populated
	// catalog directory. Refuse instead — the apply-side empty-vs-live guard,
	// mirrored (ADR-0041).
	if len(items) == 0 {
		if existing, readErr := catalog.Read(dir); readErr == nil && len(existing) > 0 {
			return nil, exit.Usagef("live subscription catalog of %q is empty while %q holds %d catalog file(s) — refusing to erase the local catalog; verify --package, or delete the files yourself if the empty live catalog is intended", pkg, dir, len(existing))
		}
	}
	// Offers are a separate sub-resource (no embedding in the Subscription
	// resource), read in one wildcard walk and nested under each base plan's
	// "offers" array in the files (slice #369, ADR-0041 §5).
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
	// Merge the pages back into one ListSubscriptionsResponse envelope so the
	// json view stays an API-shaped pass-through (the fully-consumed
	// nextPageToken is the only field dropped) — the team users list precedent.
	merged, err := json.Marshal(struct {
		Subscriptions []json.RawMessage `json:"subscriptions"`
	}{Subscriptions: rawSubs})
	if err != nil {
		return nil, fmt.Errorf("marshal merged response: %w", err)
	}
	return Payload{Package: pkg, Dir: dir, Written: written, Removed: removed, Raw: merged}, nil
}

// NewCommand returns the cobra command for `gplay subscriptions pull`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "[experimental] Pull the live subscription catalog into files",
		Long: `Read the app's complete live subscription catalog
(monetization.subscriptions.list plus one wildcard offers walk, every page)
and write it as the on-disk catalog: one <productId>.json file per
subscription under --dir (default ` + subscriptionscmd.DefaultDir + `), the
API resource verbatim with each base plan's offers nested under
basePlans[].offers. Stale .json files for subscriptions no longer live are
removed so the directory mirrors Play; non-.json files are never touched.
Commit the directory, edit the files, then rehearse with
"gplay subscriptions apply --dry-run". --output json stays the merged
ListSubscriptionsResponse (offers travel in the files).

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
	cmd.Flags().StringVar(&in.Dir, "dir", subscriptionscmd.DefaultDir, "catalog directory to write")
	return cmd
}
