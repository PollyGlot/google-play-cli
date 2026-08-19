// Package imagespull implements `gplay metadata images pull`: it rapatriates
// the Store images live on Google Play for a package into the local Metadata
// tree on disk, under each locale's images/ sub-directory, alongside the text
// Listing `.txt` files. It reads ONLY from Play, then writes additively:
// resolve --package/--dir, open a read-only Edit, enumerate the app's locales,
// list each of the 9 image slots, download each image's bytes from its API
// url, and hand the resulting imagetree.Tree to imagetree.Write. The Edit is
// opened and discarded via edits.WithReadOnlyEdit, never committed.
//
// Filename synthesis (ADR-0013): images.list returns no original filename, so
// pull names singular slots `<type>.<ext>` and gallery slots `<type>/1.<ext>`…
// `N.<ext>` in display order, with the extension sniffed from the image bytes
// (PNG/JPEG magic), not the response Content-Type. A slot with no images
// online writes no directory/file, so pull never emits the "empty" form and a
// later `apply` sees no delta: the pull → apply no-op invariant.
package imagespull

import (
	"context"
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
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
	"github.com/PollyGlot/google-play-cli/internal/play/listings"
)

// DefaultDir matches the rest of the metadata family and `fastlane supply`.
const DefaultDir = "./metadata"

// maxImageBytes caps how many bytes pull holds in memory per downloaded image.
// Play's largest asset (a 3840px screenshot) stays well under this; the cap is
// defence-in-depth against a runaway or hostile url.
const maxImageBytes = 16 << 20 // 16 MiB

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package string
	Dir     string
}

type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

type packageNotFoundError struct {
	pkg   string
	cause error
}

func (e *packageNotFoundError) Error() string {
	return fmt.Sprintf("package %q not found: run `gplay apps list` to see the packages registered with gplay: %v", e.pkg, e.cause)
}
func (e *packageNotFoundError) Unwrap() error { return e.cause }

type forbiddenError struct {
	pkg   string
	cause error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account is not granted access to %q: in the Play Console, open Setup → API access and grant this service account permission on the app: %v", e.pkg, e.cause)
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

// slotReport is one written slot for the gplay summary: locale, image type,
// and how many images were pulled into it.
type slotReport struct {
	Locale    string `json:"locale"`
	ImageType string `json:"imageType"`
	Count     int    `json:"count"`
}

// summary is the run-level tally. A CI gate can read `.summary.files` in one
// jq line.
type summary struct {
	Locales int `json:"locales"`
	Slots   int `json:"slots"`
	Files   int `json:"files"`
}

// Payload is a gplay-defined summary of what pull WROTE (not an API
// pass-through): the on-disk result, not the wire payload, is what the
// operator cares about. Pulled is in (locale, canonical type) order.
type Payload struct {
	Package string       `json:"package"`
	Dir     string       `json:"dir"`
	Pulled  []slotReport `json:"pulled"`
	Summary summary      `json:"summary"`
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

func headers() []string { return []string{"LOCALE", "IMAGE_TYPE", "COUNT"} }

func row(r slotReport) []string {
	return []string{r.Locale, r.ImageType, strconv.Itoa(r.Count)}
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

// Run resolves inputs, opens a read-only Edit, lists every (locale, imageType)
// slot, downloads the bytes, and writes the resulting tree additively.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg := in.Package
	if pkg == "" && rc.Resolved != nil {
		pkg = rc.Resolved.Pin
	}
	if pkg == "" {
		return nil, &usageError{msg: "no package: pass --package <pkg> or run gplay init in your repo"}
	}
	dir := in.Dir
	if dir == "" {
		dir = DefaultDir
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	tr := make(imagetree.Tree)
	if err := edits.WithReadOnlyEdit(rc.Ctx, httpClient, pkg, func(editID string) error {
		locales, err := appLocales(rc, httpClient, pkg, editID)
		if err != nil {
			return err
		}
		for _, loc := range locales {
			for _, ty := range images.Types() {
				imgs, _, e := images.List(rc.Ctx, httpClient, pkg, editID, loc, ty)
				if e != nil {
					return e
				}
				if len(imgs) == 0 {
					continue // empty slot writes nothing (missing == empty)
				}
				seq := make([][]byte, 0, len(imgs))
				for _, img := range imgs {
					b, e := download(rc.Ctx, httpClient, img.URL)
					if e != nil {
						return e
					}
					seq = append(seq, b)
				}
				if tr[loc] == nil {
					tr[loc] = make(map[images.Type][][]byte)
				}
				tr[loc][ty] = seq
			}
		}
		return nil
	}); err != nil {
		return nil, classifyEditError(pkg, err)
	}

	if err := imagetree.Write(dir, tr); err != nil {
		return nil, err
	}

	return newPayload(pkg, dir, tr), nil
}

// download GETs the image bytes at url (the API gives no original filename, so
// the bytes are downloaded from the url images.list returns). It uses the
// authenticated client: Play's image urls are Google-owned hosts, so carrying
// the androidpublisher token is harmless, and caps the read at maxImageBytes.
func download(ctx context.Context, hc *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, &api.Error{Operation: "images.download", Message: err.Error(), Cause: err}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &api.Error{Operation: "images.download", Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &api.Error{Operation: "images.download", StatusCode: resp.StatusCode, Message: fmt.Sprintf("downloading %s: HTTP %d", url, resp.StatusCode)}
	}
	b, err := readCapped(resp.Body, maxImageBytes)
	if err != nil {
		return nil, &api.Error{Operation: "images.download", Message: fmt.Sprintf("downloading %s: %v", url, err), Cause: err}
	}
	return b, nil
}

// readCapped reads up to max bytes from r and FAILS if the source has more,
// rather than silently truncating: a truncated image would be written to disk
// as a corrupt file. It reads one extra byte to detect the overflow.
func readCapped(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, fmt.Errorf("read image: %w", err)
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("image exceeds the %d-byte cap", max)
	}
	return b, nil
}

// appLocales returns the app's locale codes (those carrying a Listing), sorted,
// inside the already-open Edit: the enumeration domain for the 9 image types.
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

// newPayload builds the summary from the written tree, in (locale, canonical
// type) order.
func newPayload(pkg, dir string, tr imagetree.Tree) Payload {
	pulled := make([]slotReport, 0)
	files := 0
	locales := make([]string, 0, len(tr))
	for loc := range tr {
		locales = append(locales, loc)
	}
	sort.Strings(locales)
	for _, loc := range locales {
		for _, ty := range images.Types() {
			seq := tr[loc][ty]
			if len(seq) == 0 {
				continue
			}
			pulled = append(pulled, slotReport{Locale: loc, ImageType: string(ty), Count: len(seq)})
			files += len(seq)
		}
	}
	return Payload{
		Package: pkg,
		Dir:     dir,
		Pulled:  pulled,
		Summary: summary{Locales: len(locales), Slots: len(pulled), Files: files},
	}
}

// NewCommand returns the cobra command for `gplay metadata images pull`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Rapatriate the Store images live on Play into the local Metadata tree",
		Long: `Rapatriate the Store images currently live on Google Play for --package
into the local Metadata tree under --dir (default ./metadata): singular
slots as ` + "`<locale>/images/<type>.<ext>`" + ` and gallery slots as
` + "`<locale>/images/<type>/1.<ext>…N.<ext>`" + ` in display order.

Reads inside a read-only Edit (open → list slots → download bytes →
discard); nothing is committed. The write is additive: a slot with no
images online writes nothing, so pull never emits an empty slot and a
` + "`metadata images apply`" + ` immediately after a pull is a no-op
(ADR-0013). Filenames are synthesized (the API carries none) and the
extension is sniffed from the image bytes (PNG/JPEG), not the response
header.`,
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
