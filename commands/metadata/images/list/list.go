// Package imageslist implements `gplay metadata images list`: a read-only
// summary of the Store images live on Google Play for a package — one row per
// non-empty slot (a (locale, imageType) pair), showing the image count and
// each image's content sha256. It reads ONLY from Play (never the local
// tree): resolve --package, open a read-only Edit, enumerate the app's
// locales via listings.list, then read each of the 9 AppImageType slots per
// locale via images.list. The Edit is opened and discarded via
// edits.WithReadOnlyEdit, never committed.
//
// The API exposes no "list every image" endpoint, so list iterates the 9
// types × the app's locales (the locales are those carrying a Listing). The
// API also carries no order field — display order is list order (ADR-0013),
// so the per-image sha256 are reported in the order images.list returns them.
package imageslist

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
	"github.com/PollyGlot/google-play-cli/internal/play/listings"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package string
	Type    string
}

// usageError is a CLI-misuse error (no package); ExitCode()=2 per
// docs/DESIGN.md §9.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// validationError is a client-side flag-value failure (an unknown
// --type); ExitCode()=20 per docs/DESIGN.md §9. Refused offline, before
// any HTTP round-trip, so a typo like "iconn" surfaces a clear message
// naming the nine valid AppImageType values rather than a downstream 404.
type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }
func (e *validationError) ExitCode() int { return 20 }

// packageNotFoundError / forbiddenError mirror `metadata list`: actionable
// hints on a 404 / 403, with the wrapped *api.Error left to drive the exit
// code.
type packageNotFoundError struct {
	pkg   string
	cause error
}

func (e *packageNotFoundError) Error() string {
	return fmt.Sprintf("package %q not found — run `gplay apps list` to see the packages registered with gplay: %v", e.pkg, e.cause)
}
func (e *packageNotFoundError) Unwrap() error { return e.cause }

type forbiddenError struct {
	pkg   string
	cause error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account is not granted access to %q — in the Play Console, open Setup → API access and grant this service account permission on the app: %v", e.pkg, e.cause)
}
func (e *forbiddenError) Unwrap() error { return e.cause }

func classifyEditError(pkg string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusNotFound:
			return &packageNotFoundError{pkg: pkg, cause: err}
		case http.StatusForbidden:
			return &forbiddenError{pkg: pkg, cause: err}
		}
	}
	return err
}

// Slot is one non-empty (locale, imageType) slot: the parsed images (for the
// table/markdown count + sha256 view) and the verbatim API images array (for
// the ADR-0003 JSON pass-through).
type Slot struct {
	Locale    string
	Type      images.Type
	Images    []images.Image
	RawImages json.RawMessage
}

// Payload satisfies output.Renderable. Slots are in (locale, canonical type)
// order for deterministic output; only non-empty slots are carried (an empty
// slot has nothing to report — same stance as `pull`, which writes nothing
// for it).
type Payload struct {
	Package string
	Slots   []Slot
}

// jsonSlot/jsonDoc are the --output json shape: a flat document whose per-slot
// `images` field is the VERBATIM edits.images.list array (ADR-0003), so the
// Image objects keep the API's own field names (id/url/sha1/sha256).
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

// row renders one slot's cells: locale, type, count, and the per-image sha256
// joined in display order. Galleries are small (≤8), so the joined cell stays
// bounded.
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

// renderJSON emits the slots document with each slot's images verbatim
// (ADR-0003). A slot whose RawImages was not captured falls back to a
// re-marshaled images array, which carries the same API field names.
func renderJSON(w io.Writer, p Payload) error {
	doc := jsonDoc{Package: p.Package, Slots: make([]jsonSlot, 0, len(p.Slots))}
	for _, s := range p.Slots {
		raw := s.RawImages
		if len(raw) == 0 {
			b, err := json.Marshal(s.Images)
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

// rawImagesArray extracts the verbatim `.images` array bytes from an
// edits.images.list envelope, so the JSON pass-through keeps Play's own bytes
// rather than a re-marshal. An envelope with no `images` key yields nil (the
// renderer falls back to a re-marshal of the parsed slice).
func rawImagesArray(envelope json.RawMessage) json.RawMessage {
	var env struct {
		Images json.RawMessage `json:"images"`
	}
	if err := json.Unmarshal(envelope, &env); err != nil {
		return nil
	}
	return env.Images
}

// Run is the business function the kernel invokes. It resolves the package,
// builds an authenticated client, opens a read-only Edit, enumerates the
// app's locales, then reads every (locale, imageType) slot.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg := in.Package
	if pkg == "" && rc.Resolved != nil {
		pkg = rc.Resolved.Pin
	}
	if pkg == "" {
		return nil, &usageError{msg: "no package — pass --package <pkg> or run gplay init in your repo"}
	}

	// [experimental] --type narrows the read to a single AppImageType.
	// Validate client-side BEFORE any HTTP call so an unknown value is
	// refused offline (exit 20) rather than building an invalid slot URL.
	types := images.Types()
	if in.Type != "" {
		ty, ok := images.ParseType(in.Type)
		if !ok {
			return nil, &validationError{msg: fmt.Sprintf(
				"metadata images list: %q is not a valid image type — one of: %s",
				in.Type, joinTypes(images.Types()))}
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
		for _, loc := range locales {
			for _, ty := range types {
				imgs, raw, e := images.List(rc.Ctx, httpClient, pkg, editID, loc, ty)
				if e != nil {
					return e
				}
				if len(imgs) == 0 {
					continue
				}
				slots = append(slots, Slot{Locale: loc, Type: ty, Images: imgs, RawImages: rawImagesArray(raw)})
			}
		}
		return nil
	}); err != nil {
		return nil, classifyEditError(pkg, err)
	}

	return Payload{Package: pkg, Slots: slots}, nil
}

// joinTypes renders the nine AppImageType values as a comma-separated
// list for the --type validation-error message, in canonical order.
func joinTypes(ts []images.Type) string {
	ss := make([]string, 0, len(ts))
	for _, t := range ts {
		ss = append(ss, string(t))
	}
	return strings.Join(ss, ", ")
}

// appLocales returns the app's locale codes (those carrying a Listing), in
// sorted order, inside the already-open Edit. There is no list-by-locale
// images endpoint, so the Listing locales are the enumeration domain for the
// 9 image types.
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

// NewCommand returns the cobra command for `gplay metadata images list`.
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
locales → images.list for each of the 9 image types per locale → discard);
nothing is committed and nothing on disk is read. The API exposes no
list-by-locale endpoint, so list walks the 9 types across every locale that
has a Listing.

[experimental] --type <AppImageType> narrows the read to a single image
type (one of: icon, featureGraphic, tvBanner, promoGraphic,
phoneScreenshots, sevenInchScreenshots, tenInchScreenshots, tvScreenshots,
wearScreenshots) across every locale, instead of walking all nine. An
unknown value is refused client-side (exit 20) before any API call.
--output json keeps its exact shape — --type only narrows which slots
appear.

(--output json carries each slot's images verbatim — the edits.images.list
objects, id/url/sha1/sha256; --output markdown renders a Markdown table.)`,
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
	cmd.Flags().StringVar(&in.Type, "type", "", "[experimental] narrow to one AppImageType (icon, featureGraphic, phoneScreenshots, …); default: all nine")
	return cmd
}
