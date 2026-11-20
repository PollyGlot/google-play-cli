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

const DefaultDir = "./metadata"

const maxImageBytes = 16 << 20

type Input struct {
	Package string
	Dir     string
}

type slotReport struct {
	Locale    string `json:"locale"`
	ImageType string `json:"imageType"`
	Count     int    `json:"count"`
}

type summary struct {
	Locales int `json:"locales"`
	Slots   int `json:"slots"`
	Files   int `json:"files"`
}

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
		return fetchSlots(rc, httpClient, rc.PublicClient(), pkg, editID, locales, staging, tr)
	}); err != nil {
		return nil, apihint.ForPackage(pkg, err)
	}

	if err := imagetree.WriteStaged(dir, tr); err != nil {
		return nil, err
	}

	return newPayload(pkg, dir, tr), nil
}

func fetchSlots(rc *kernel.RunContext, hc, media *http.Client, pkg, editID string, locales []string, staging string, tr imagetree.Staged) error {
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
		if err := stage(rc.Ctx, media, img, u, path); err != nil {
			return err
		}
		files[j.slot][j.pos] = path
		return nil
	}); err != nil {
		return err
	}

	for s, seq := range files {
		if len(seq) == 0 {
			continue
		}
		loc, ty := locales[s/len(types)], types[s%len(types)]
		if tr[loc] == nil {
			tr[loc] = make(map[images.Type][]string)
		}
		tr[loc][ty] = seq
	}
	return nil
}

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

const opImagesDownload = "images.download"

func download(ctx context.Context, hc *http.Client, url string, w io.Writer) error {
	dst := &cappedWriter{w: w, max: maxImageBytes}
	_, err := api.Download(ctx, hc, api.Call{URL: url, Op: opImagesDownload}, dst)
	if err == nil {
		return nil
	}
	var ae *api.Error
	if !errors.As(err, &ae) || ae.StatusCode == 0 {
		return err
	}
	if ae.StatusCode < 200 || ae.StatusCode >= 300 {
		return &api.Error{Operation: opImagesDownload, StatusCode: ae.StatusCode, Message: fmt.Sprintf("downloading %s: HTTP %d", url, ae.StatusCode)}
	}
	var tooBig *imageTooLargeError
	if errors.As(ae.Cause, &tooBig) {
		return &api.Error{Operation: opImagesDownload, Message: fmt.Sprintf("downloading %s: %v", url, tooBig), Cause: ae.Cause}
	}
	return &api.Error{Operation: opImagesDownload, Message: fmt.Sprintf("downloading %s: read image: %v", url, ae.Cause), Cause: ae.Cause}
}

func checkSha256(img images.Image, url, got string) error {
	if img.Sha256 == "" {
		return nil
	}
	if !strings.EqualFold(got, img.Sha256) {
		return &sha256MismatchError{id: img.ID, url: url, got: got, want: img.Sha256}
	}
	return nil
}

type sha256MismatchError struct{ id, url, got, want string }

func (e *sha256MismatchError) Error() string {
	return fmt.Sprintf("image %s downloaded from %s hashes to sha256 %s, but images.list reports %s: the host served a copy, not the stored file, so pull wrote nothing", e.id, e.url, e.got, e.want)
}

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

type imageTooLargeError struct{ max int64 }

func (e *imageTooLargeError) Error() string {
	return fmt.Sprintf("image exceeds the %d-byte cap", e.max)
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
