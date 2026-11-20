package pull

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/apihint"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/metadata/tree"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/listings"
)

const DefaultDir = "./metadata"

type Input struct {
	Package string
	Dir     string
}

func fieldValue(l listings.Listing, f listing.Field) string {
	switch f {
	case listing.Title:
		return l.Title
	case listing.ShortDescription:
		return l.ShortDescription
	case listing.FullDescription:
		return l.FullDescription
	case listing.Video:
		return l.Video
	default:
		return ""
	}
}

func BuildTree(apiListings []listings.Listing) listing.Tree {
	tr := make(listing.Tree)
	for _, al := range apiListings {
		l := listing.NewListing(al.Language)
		for _, f := range listing.Fields() {
			v := fieldValue(al, f)
			if v == "" {
				continue
			}
			l.Set(f, v)
		}
		if l.Empty() {
			continue
		}
		tr[al.Language] = l
	}
	return tr
}

type localeReport struct {
	Locale string   `json:"locale"`
	Fields []string `json:"fields"`
}

type summary struct {
	Locales int `json:"locales"`
	Files   int `json:"files"`
}

type Payload struct {
	Package string         `json:"package"`
	Dir     string         `json:"dir"`
	Pulled  []localeReport `json:"pulled"`
	Summary summary        `json:"summary"`
}

func newPayload(pkg, dir string, tr listing.Tree) Payload {
	pulled := make([]localeReport, 0, len(tr))
	files := 0
	for _, locale := range tr.Locales() {
		l := tr[locale]
		keys := make([]string, 0, len(l.Fields))
		for _, f := range listing.Fields() {
			if _, managed := l.Get(f); !managed {
				continue
			}
			spec, ok := listing.SpecOf(f)
			if !ok {
				continue
			}
			keys = append(keys, spec.Key)
		}
		pulled = append(pulled, localeReport{Locale: locale, Fields: keys})
		files += len(keys)
	}
	return Payload{
		Package: pkg,
		Dir:     dir,
		Pulled:  pulled,
		Summary: summary{Locales: len(pulled), Files: files},
	}
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

func headers() []string { return []string{"LOCALE", "FIELDS"} }

func row(r localeReport) []string {
	return []string{r.Locale, strings.Join(r.Fields, ", ")}
}

func renderTable(w io.Writer, p Payload) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, strings.Join(headers(), "\t")); err != nil {
		return err
	}
	for _, r := range p.Pulled {
		if _, err := fmt.Fprintln(tw, strings.Join(row(r), "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func renderMarkdown(w io.Writer, p Payload) error {
	rows := make([][]string, 0, len(p.Pulled))
	for _, r := range p.Pulled {
		rows = append(rows, row(r))
	}
	return output.MarkdownTable(w, headers(), rows)
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}

	dir := in.Dir
	if dir == "" {
		dir = DefaultDir
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	var parsed []listings.Listing
	if err := edits.WithReadOnlyEdit(rc.Ctx, httpClient, pkg, func(editID string) error {
		ls, _, e := listings.List(rc.Ctx, httpClient, pkg, editID)
		if e != nil {
			return e
		}
		parsed = ls
		return nil
	}); err != nil {
		return nil, apihint.ForPackage(pkg, err)
	}

	tr := BuildTree(parsed)
	if err := tree.Write(dir, tr); err != nil {
		return nil, err
	}

	return newPayload(pkg, dir, tr), nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Download the Store front Listings live on Play into the local Metadata tree",
		Long: `Download the Store front Listings currently live on Google Play for
--package into the local Metadata tree under --dir (default ./metadata):
one ` + "`<locale>/<field>.txt`" + ` file per managed field a locale holds
non-empty online (title, short_description, full_description, video).

Reads the Listings inside a read-only Edit (open → listings.list →
discard); nothing is committed. The write is additive: a field Play holds
empty writes no file, and a local locale absent online is left intact.
Removing locales/fields no longer online is the opt-in job of ` +
			"`gplay metadata apply --prune`" + `.

Because pull writes only non-empty online values and the tree codec is a
value-level inverse, a ` + "`metadata apply`" + ` immediately after a pull
is a guaranteed no-op.`,
		Example: `  # Seed the Metadata tree from what is live on Play, then commit it
  gplay metadata pull

  # Write it somewhere else, for another app
  gplay metadata pull --dir store/metadata --package com.example.lite`,
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
	cmd.Flags().StringVar(&in.Dir, "dir", DefaultDir, "directory to write the Metadata tree into")
	return cmd
}
