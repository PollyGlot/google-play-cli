package imageslist

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/apihint"
	"github.com/PollyGlot/google-play-cli/internal/fanout"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
	"github.com/PollyGlot/google-play-cli/internal/play/listings"
)

type Input struct {
	Package string
	Type    string
}

type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }
func (e *validationError) ExitCode() int { return 20 }

type Slot struct {
	Locale    string
	Type      images.Type
	Images    []images.Image
	RawImages json.RawMessage
}

type Payload struct {
	Package string
	Slots   []Slot
}

type jsonSlot struct {
	Locale    string          `json:"locale"`
	ImageType string          `json:"imageType"`
	Count     int             `json:"count"`
	Images    json.RawMessage `json:"images"`
}

type jsonDoc struct {
	Package string     `json:"package"`
	Slots   []jsonSlot `json:"slots"`
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

func headers() []string { return []string{"LOCALE", "IMAGE_TYPE", "COUNT", "SHA256"} }

func row(s Slot) []string {
	sums := make([]string, 0, len(s.Images))
	for _, img := range s.Images {
		sums = append(sums, img.Sha256)
	}
	return []string{s.Locale, string(s.Type), strconv.Itoa(len(s.Images)), strings.Join(sums, ", ")}
}

func renderTable(w io.Writer, p Payload) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, strings.Join(headers(), "\t")); err != nil {
		return err
	}
	for _, s := range p.Slots {
		if _, err := fmt.Fprintln(tw, strings.Join(row(s), "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func renderMarkdown(w io.Writer, p Payload) error {
	rows := make([][]string, 0, len(p.Slots))
	for _, s := range p.Slots {
		rows = append(rows, row(s))
	}
	return output.MarkdownTable(w, headers(), rows)
}

func renderJSON(w io.Writer, p Payload) error {
	doc := jsonDoc{Package: p.Package, Slots: make([]jsonSlot, 0, len(p.Slots))}
	for _, s := range p.Slots {
		raw := s.RawImages
		if len(raw) == 0 {
			b, err := output.Marshal(s.Images)
			if err != nil {
				return err
			}
			raw = b
		}
		doc.Slots = append(doc.Slots, jsonSlot{
			Locale:    s.Locale,
			ImageType: string(s.Type),
			Count:     len(s.Images),
			Images:    raw,
		})
	}
	return output.WriteJSON(w, doc)
}

func rawImagesArray(envelope json.RawMessage) json.RawMessage {
	var env struct {
		Images json.RawMessage `json:"images"`
	}
	if err := json.Unmarshal(envelope, &env); err != nil {
		return nil
	}
	return env.Images
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}

	types := images.Types()
	if in.Type != "" {
		ty, ok := images.ParseType(in.Type)
		if !ok {
			return nil, &validationError{msg: fmt.Sprintf(
				"metadata images list: %q is not a valid image type: one of: %s",
				in.Type, images.Names())}
		}
		types = []images.Type{ty}
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	var slots []Slot
	if err := edits.WithReadOnlyEdit(rc.Ctx, httpClient, pkg, func(editID string) error {
		locales, err := appLocales(rc, httpClient, pkg, editID)
		if err != nil {
			return err
		}
		read := make([]Slot, len(locales)*len(types))
		if err := fanout.Each(len(read), func(i int) error {
			loc, ty := locales[i/len(types)], types[i%len(types)]
			imgs, raw, e := images.List(rc.Ctx, httpClient, pkg, editID, loc, ty)
			if e != nil {
				return e
			}
			read[i] = Slot{Locale: loc, Type: ty, Images: imgs, RawImages: rawImagesArray(raw)}
			return nil
		}); err != nil {
			return err
		}
		for _, s := range read {
			if len(s.Images) > 0 {
				slots = append(slots, s)
			}
		}
		return nil
	}); err != nil {
		return nil, apihint.ForPackage(pkg, err)
	}

	return Payload{Package: pkg, Slots: slots}, nil
}

func appLocales(rc *kernel.RunContext, hc *http.Client, pkg, editID string) ([]string, error) {
	parsed, _, err := listings.List(rc.Ctx, hc, pkg, editID)
	if err != nil {
		return nil, err
	}
	locs := make([]string, 0, len(parsed))
	for _, l := range parsed {
		locs = append(locs, l.Language)
	}
	sort.Strings(locs)
	return locs, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Summarize the Store images live on Play, per locale and slot",
		Long: `Summarize the Store images currently live on Google Play for --package:
one row per non-empty slot (a locale × image-type pair), showing the image
count and each image's content sha256 in display order.

Reads inside a read-only Edit (open → listings.list to enumerate the app's
locales → images.list for each of the 8 image types per locale → discard);
nothing is committed and nothing on disk is read. The API exposes no
list-by-locale endpoint, so list walks the 8 types across every locale that
has a Listing.

[experimental] --type <AppImageType> narrows the read to a single image
type (one of: icon, featureGraphic, tvBanner, phoneScreenshots,
sevenInchScreenshots, tenInchScreenshots, tvScreenshots, wearScreenshots)
across every locale, instead of walking all eight. An
unknown value is refused client-side (exit 20) before any API call.
--output json keeps its exact shape: --type only narrows which slots
appear.

(--output json carries each slot's images verbatim: the edits.images.list
objects, id/url/sha1/sha256; --output markdown renders a Markdown table.)`,
		Example: `  gplay metadata images list
  gplay metadata images list --type phoneScreenshots
  gplay metadata images list --output json`,
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
	cmd.Flags().StringVar(&in.Type, "type", "", "[experimental] narrow to one AppImageType (icon, featureGraphic, phoneScreenshots, …); default: all eight")
	return cmd
}
