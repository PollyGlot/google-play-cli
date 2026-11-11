// Package imagespull implements `gplay metadata images pull`: it rapatriates
// the Store images live on Google Play for a package into the local Metadata
// tree on disk, under each locale's images/ sub-directory, alongside the text
// Listing `.txt` files. It reads ONLY from Play, then writes additively:
// resolve --package/--dir, open a read-only Edit, enumerate the app's locales,
// list each of the 8 image slots, stream each image's original bytes to a
// staging file while checking them against the API's sha256, and hand the
// resulting imagetree.Staged tree to imagetree.WriteStaged. The Edit is opened
// and discarded via edits.WithReadOnlyEdit, never committed.
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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/apihint"
	"github.com/PollyGlot/google-play-cli/internal/fanout"
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

// maxImageBytes caps how many bytes pull accepts per downloaded image.
// Play's largest asset (a 3840px screenshot) stays well under this; the cap is
// defence-in-depth against a runaway or hostile url.
const maxImageBytes = 16 << 20 // 16 MiB

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package string
	Dir     string
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

	// Every download streams into a file under staging first, and nothing
	// touches dir until the last download succeeded. Pull stays all-or-nothing
	// under concurrency: a failed or interrupted pull leaves no half-written
	// tree that a later `images apply --prune` would read as deletions. Staging
	// on disk rather than in memory keeps a pull of hundreds of full-size
	// images to one copy buffer per in-flight download.
	staging, err := os.MkdirTemp("", "gplay-images-pull-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(staging) }()

	tr := make(imagetree.Staged)
	if err := edits.WithReadOnlyEdit(rc.Ctx, httpClient, pkg, func(editID string) error {
		locales, err := appLocales(rc, httpClient, pkg, editID)
		if err != nil {
			return err
		}
		return fetchSlots(rc, httpClient, pkg, editID, locales, staging, tr)
	}); err != nil {
		return nil, apihint.ForPackage(pkg, err)
	}

	if err := imagetree.WriteStaged(dir, tr); err != nil {
		return nil, err
	}

	return newPayload(pkg, dir, tr), nil
}

// fetchSlots fills tr with every non-empty (locale, type) slot, each image
// staged as its own file under staging, in two
// fanout.Limit-wide passes: list every slot, then download every image of
// every slot. Two passes rather than one task per slot keep the pool busy when
// one gallery holds eight screenshots and the other slots hold none. Every
// result is written at its own index, so tr is identical to a serial walk's.
// Any failure aborts before tr is touched; within a pass the error reported is
// the lowest-index one, so the same responses always yield the same error.
func fetchSlots(rc *kernel.RunContext, hc *http.Client, pkg, editID string, locales []string, staging string, tr imagetree.Staged) error {
	types := images.Types()
	listed := make([][]images.Image, len(locales)*len(types))
	if err := fanout.Each(len(listed), func(i int) error {
		imgs, _, err := images.List(rc.Ctx, hc, pkg, editID, locales[i/len(types)], types[i%len(types)])
		listed[i] = imgs
		return err
	}); err != nil {
		return err
	}

	type job struct{ slot, pos int }
	var jobs []job
	files := make([][]string, len(listed))
	for s, imgs := range listed {
		files[s] = make([]string, len(imgs))
		for p := range imgs {
			jobs = append(jobs, job{slot: s, pos: p})
		}
	}
	if err := fanout.Each(len(jobs), func(i int) error {
		j := jobs[i]
		img := listed[j.slot][j.pos]
		u := images.OriginalURL(img.URL)
		path := filepath.Join(staging, strconv.Itoa(i))
		if err := stage(rc.Ctx, hc, img, u, path); err != nil {
			return err
		}
		files[j.slot][j.pos] = path
		return nil
	}); err != nil {
		return err
	}

	for s, seq := range files {
		if len(seq) == 0 {
			continue // empty slot writes nothing (missing == empty)
		}
		loc, ty := locales[s/len(types)], types[s%len(types)]
		if tr[loc] == nil {
			tr[loc] = make(map[images.Type][]string)
		}
		tr[loc][ty] = seq
	}
	return nil
}

// stage downloads img from url into a new file at path, hashing the bytes as
// they stream, and refuses them (checkSha256) unless they are the stored file.
func stage(ctx context.Context, hc *http.Client, img images.Image, url, path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	err = download(ctx, hc, url, io.MultiWriter(f, h))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return checkSha256(img, url, hex.EncodeToString(h.Sum(nil)))
}

// opImagesDownload tags the errors of an image-bytes download.
const opImagesDownload = "images.download"

// download GETs the image bytes at url (the API gives no original filename, so
// the bytes are downloaded from the url images.list returns, sized to the
// original by images.OriginalURL rather than the host's thumbnail) through the
// executor's streaming download. It uses the authenticated client: Play's image
// urls are Google-owned hosts, so carrying the androidpublisher token is
// harmless, and caps the body at maxImageBytes. The bytes stream into w, which
// holds whatever arrived before a failure.
func download(ctx context.Context, hc *http.Client, url string, w io.Writer) error {
	dst := &cappedWriter{w: w, max: maxImageBytes}
	_, err := api.Download(ctx, hc, api.Call{URL: url, Op: opImagesDownload}, dst)
	if err == nil {
		return nil
	}
	var ae *api.Error
	if !errors.As(err, &ae) || ae.StatusCode == 0 {
		return err // no answer came back: the transport error as tagged
	}
	if ae.StatusCode < 200 || ae.StatusCode >= 300 {
		// An image host does not answer with Google's error envelope: the
		// status is the whole signal.
		return &api.Error{Operation: opImagesDownload, StatusCode: ae.StatusCode, Message: fmt.Sprintf("downloading %s: HTTP %d", url, ae.StatusCode)}
	}
	// A 2xx that never arrived whole: cut mid-body, or past the cap. Neither is
	// the API's answer, so neither carries its status (exit 50).
	var tooBig *imageTooLargeError
	if errors.As(ae.Cause, &tooBig) {
		return &api.Error{Operation: opImagesDownload, Message: fmt.Sprintf("downloading %s: %v", url, tooBig), Cause: ae.Cause}
	}
	return &api.Error{Operation: opImagesDownload, Message: fmt.Sprintf("downloading %s: read image: %v", url, ae.Cause), Cause: ae.Cause}
}

// checkSha256 refuses bytes whose hex sha256 got is not the Image's sha256, the
// content identity apply reconciles by (ADR-0013). A mismatch means the host
// served something other than the stored file (a resized preview, should the
// "=s0" size option ever stop meaning "original"), and writing it would make a
// later apply replace the store image with it. An Image without a sha256 is
// not checked: there is nothing to compare against.
func checkSha256(img images.Image, url, got string) error {
	if img.Sha256 == "" {
		return nil
	}
	if !strings.EqualFold(got, img.Sha256) {
		return &sha256MismatchError{id: img.ID, url: url, got: got, want: img.Sha256}
	}
	return nil
}

// sha256MismatchError is a download whose bytes are not the stored image. It
// carries no exit code of its own (exit 1): neither the network nor the API
// failed, and a retry would fetch the same bytes.
type sha256MismatchError struct{ id, url, got, want string }

func (e *sha256MismatchError) Error() string {
	return fmt.Sprintf("image %s downloaded from %s hashes to sha256 %s, but images.list reports %s: the host served a copy, not the stored file, so pull wrote nothing", e.id, e.url, e.got, e.want)
}

// cappedWriter passes a download through to w and refuses the first byte past
// max rather than silently truncating: a truncated image would be written to
// disk as a corrupt file.
type cappedWriter struct {
	w   io.Writer
	n   int64
	max int64
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	if c.n+int64(len(p)) > c.max {
		return 0, &imageTooLargeError{max: c.max}
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// imageTooLargeError is the cap refusing an image.
type imageTooLargeError struct{ max int64 }

func (e *imageTooLargeError) Error() string {
	return fmt.Sprintf("image exceeds the %d-byte cap", e.max)
}

// appLocales returns the app's locale codes (those carrying a Listing), sorted,
// inside the already-open Edit: the enumeration domain for the 8 image types.
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
func newPayload(pkg, dir string, tr imagetree.Staged) Payload {
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
		Short: "Download the Store images live on Play into the local Metadata tree",
		Long: `Download the Store images currently live on Google Play for --package
into the local Metadata tree under --dir (default ./metadata): singular
slots as ` + "`<locale>/images/<type>.<ext>`" + ` and gallery slots as
` + "`<locale>/images/<type>/1.<ext>…N.<ext>`" + ` in display order.

Reads inside a read-only Edit (open → list slots → download bytes →
discard); nothing is committed. The write is additive: a slot with no
images online writes nothing, so pull never emits an empty slot and a
` + "`metadata images apply`" + ` immediately after a pull is a no-op.
Filenames are synthesized (the API carries none) and the
extension is sniffed from the image bytes (PNG/JPEG), not the response
header.`,
		Example: `  gplay metadata images pull
  gplay metadata images pull --dir store/metadata --package com.example.lite`,
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
