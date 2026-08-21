// Package pull implements `gplay metadata pull`: it rapatriates the Store
// front Listings live on Google Play for a package into the local Metadata
// tree on disk. It reads ONLY from Play, then writes the tree additively:
// resolve --package, open a read-only Edit, consume
// internal/play/listings.List, project each API Listing onto the managed
// fields keeping ONLY the non-empty values, and hand the resulting
// listing.Tree to internal/metadata/tree.Write. The Edit is opened and
// discarded via edits.WithReadOnlyEdit, never committed.
//
// The projection is the heart of the command and the source of the ADR-0011
// "pull then apply is a no-op" invariant: an online field that is empty ("")
// or absent is treated as NOT managed (never Set on the listing.Listing), so
// tree.Write writes no file for it; an online field that is non-empty is
// managed with its value, so tree.Write writes the file. Because the
// projection only ever surfaces non-empty values and tree.Read/tree.Write are
// value-level inverses, re-reading the tree after a pull yields exactly the
// projection: a later `apply` sees no delta.
//
// pull never deletes: tree.Write is additive, so a local locale absent online
// is left intact on disk. Removing locales/fields no longer online is the
// opt-in job of `metadata apply --prune`, never a side effect of pull.
package pull

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/metadata/tree"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/listings"
)

// DefaultDir is the on-disk root pull writes the Metadata tree under when
// --dir is not given. It matches the convention in CLAUDE.md's command
// roadmap (`gplay metadata apply --dir ./metadata`).
const DefaultDir = "./metadata"

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package string
	Dir     string
}

// usageError is a CLI-misuse error (no package); ExitCode()=2 per
// docs/DESIGN.md §9.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func (e *usageError) ExitCode() int { return 2 }

// packageNotFoundError wraps an edits.insert 404 with a hint pointing at
// `gplay apps list`. Like in `metadata list`, it carries no ExitCode of its
// own so the wrapped *api.Error (404 -> exit 30) stays authoritative through
// the Coder chain: an unknown package is an API 4xx, not a CLI misuse.
type packageNotFoundError struct {
	pkg   string
	cause error
}

func (e *packageNotFoundError) Error() string {
	return fmt.Sprintf("package %q not found: run `gplay apps list` to see the packages registered with gplay: %v", e.pkg, e.cause)
}

func (e *packageNotFoundError) Unwrap() error { return e.cause }

// forbiddenError wraps a 403 (service account not invited on the app) with
// the standard grant-access hint. It carries no ExitCode of its own so the
// wrapped *api.Error (403 -> exit 11) stays authoritative.
type forbiddenError struct {
	pkg   string
	cause error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account is not granted access to %q: in the Play Console, open Setup → API access and grant this service account permission on the app: %v", e.pkg, e.cause)
}

func (e *forbiddenError) Unwrap() error { return e.cause }

// classifyEditError adds an actionable hint to the two operator-facing
// failures of the read-only listings read: an unknown package (404) and a
// service account that has not been invited on the app (403), while leaving
// the wrapped *api.Error to drive the exit code. Every other failure (5xx,
// network, edit conflict) propagates verbatim. Mirrors `metadata list`.
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

// fieldValue returns the value of the managed field f on an API Listing. The
// four managed fields map onto the API struct's keys 1:1 (title,
// shortDescription, fullDescription, video); an out-of-range Field yields "".
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

// BuildTree projects the online edits.listings.list payload onto the managed
// listing.Tree pull writes to disk. It is the structural encoding of the
// ADR-0011 "missing ≠ empty" rule for the pull direction: a field is Set on a
// locale's listing.Listing ONLY when its online value is non-empty, so it
// becomes a managed field tree.Write materializes as a file; an empty ("") or
// absent online field is never Set, so it stays unmanaged and tree.Write
// writes no file for it. A locale whose every managed field is empty online
// contributes no entry at all (an empty Listing carries no information and
// would only create an empty directory). Locale order does not matter: the
// Tree is keyed by locale and tree.Write iterates it in sorted order.
//
// Because the projection only surfaces non-empty values and tree.Read/Write
// are value-level inverses, tree.Read(dir) after a pull returns exactly
// BuildTree(online): a later `apply` sees no delta. This is the no-op
// invariant, provable as a pure function here without any I/O.
func BuildTree(apiListings []listings.Listing) listing.Tree {
	tr := make(listing.Tree)
	for _, al := range apiListings {
		l := listing.NewListing(al.Language)
		for _, f := range listing.Fields() {
			v := fieldValue(al, f)
			if v == "" {
				// Empty online -> NOT managed: skip, so tree.Write writes
				// no file and a later apply leaves the field untouched.
				continue
			}
			l.Set(f, v)
		}
		if l.Empty() {
			// Every field empty online: nothing to write for this locale.
			continue
		}
		tr[al.Language] = l
	}
	return tr
}

// localeReport is one locale's pull result for the gplay summary: the locale
// code and the API JSON keys (camelCase, from listing.SpecOf) of the fields
// written to disk, in canonical field order.
type localeReport struct {
	Locale string   `json:"locale"`
	Fields []string `json:"fields"`
}

// summary is the run-level totals: how many locales were written and how many
// field files in total. A CI gate can read `.summary.files` in one jq line.
type summary struct {
	Locales int `json:"locales"`
	Files   int `json:"files"`
}

// Payload satisfies output.Renderable. It is a gplay-defined summary of what
// pull WROTE, not an API pass-through (ADR-0011: pull's JSON is a gplay
// object, not the edits.listings.list body, since the on-disk result, not the
// wire payload, is what the operator cares about). Pulled is in tree-sorted
// locale order for deterministic output.
type Payload struct {
	Package string         `json:"package"`
	Dir     string         `json:"dir"`
	Pulled  []localeReport `json:"pulled"`
	Summary summary        `json:"summary"`
}

// newPayload builds the Payload from the projected Tree: one localeReport per
// locale (in sorted order), each listing the API keys of its managed fields,
// plus the run totals.
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

// Renderers satisfies output.Renderable with one renderer per Format. JSON is
// the gplay summary object; table and markdown are a one-row-per-locale view
// (LOCALE, FIELDS).
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

// headers returns the human-table header row.
func headers() []string { return []string{"LOCALE", "FIELDS"} }

// row renders one locale's cells: the locale code and a comma-separated list
// of the API field keys written for it.
func row(r localeReport) []string {
	return []string{r.Locale, strings.Join(r.Fields, ", ")}
}

// renderTable writes a tab-aligned LOCALE + FIELDS table, one row per locale.
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

// renderMarkdown writes the LOCALE × FIELDS view as a GitHub-Flavored Markdown
// table via output.MarkdownTable.
func renderMarkdown(w io.Writer, p Payload) error {
	rows := make([][]string, 0, len(p.Pulled))
	for _, r := range p.Pulled {
		rows = append(rows, row(r))
	}
	return output.MarkdownTable(w, headers(), rows)
}

// Run is the business function the kernel invokes. It resolves the package and
// the output dir, builds an authenticated HTTP client, opens a read-only Edit,
// lists every locale's Listing, projects them onto the managed-field model
// (non-empty values only), and writes the resulting Tree to disk additively.
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

	var parsed []listings.Listing
	if err := edits.WithReadOnlyEdit(rc.Ctx, httpClient, pkg, func(editID string) error {
		ls, _, e := listings.List(rc.Ctx, httpClient, pkg, editID)
		if e != nil {
			return e
		}
		parsed = ls
		return nil
	}); err != nil {
		return nil, classifyEditError(pkg, err)
	}

	tr := BuildTree(parsed)
	if err := tree.Write(dir, tr); err != nil {
		return nil, err
	}

	return newPayload(pkg, dir, tr), nil
}

// NewCommand returns the cobra command for `gplay metadata pull`. It does NOT
// create the parent `metadata` command; cmd/gplay/main.go wires it under it.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Rapatriate the Store front Listings live on Play into the local Metadata tree",
		Long: `Rapatriate the Store front Listings currently live on Google Play for
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
is a guaranteed no-op (ADR-0011).`,
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
