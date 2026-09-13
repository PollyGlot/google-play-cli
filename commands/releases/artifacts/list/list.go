// Package list implements `gplay releases artifacts list`: the APKs and App
// Bundles currently attached to an app, with their version codes, so an
// operator can answer "which version codes are on Play right now?" before a
// promote or a rollout without opening the Console.
//
// Both resources are Edit-scoped (edits.apks.list / edits.bundles.list). The
// command opens a read-only Edit and discards it (never commits), or, when a
// `gplay edits begin` pin exists for the package, reads inside that explicit
// Edit so artifacts uploaded in it and not yet committed are visible. Ships
// [experimental].
package list

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/artifacts"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
)

// Artifact kinds, the values of --kind and of the `kind` column.
const (
	KindApk    = "apk"
	KindBundle = "bundle"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package string
	Kind    string
	Columns string
}

// Row is the one-line-per-artifact view merging both resources. Sha256 is the
// full hex digest so it can be compared with `sha256sum` on the local file.
type Row struct {
	Kind        string
	VersionCode int64
	Sha256      string
}

// columns is the single source of truth for the artifacts table (ADR-0018):
// declaration order is both the set of valid --columns keys and the default.
var columns = output.NewColumnSet(
	output.Column[Row]{Key: "kind", Header: "KIND", Value: func(r Row) string { return r.Kind }},
	output.Column[Row]{Key: "versionCode", Header: "VERSION_CODE", Value: func(r Row) string { return strconv.FormatInt(r.VersionCode, 10) }},
	output.Column[Row]{Key: "sha256", Header: "SHA256", Value: func(r Row) string { return r.Sha256 }},
)

// ResolveColumns turns a --columns spec into validated, ordered columns.
func ResolveColumns(spec string) ([]output.Column[Row], error) { return columns.Resolve(spec) }

// Payload renders the merged rows as a table, or the raw responses as JSON.
// With both kinds the JSON view is {"apks": <raw>, "bundles": <raw>}; with
// --kind it is that one response verbatim (ADR-0003).
type Payload struct {
	Rows    []Row
	Cols    []output.Column[Row]
	Apks    json.RawMessage
	Bundles json.RawMessage
	Kind    string
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, p.Cols, p.Rows) },
	}
}

func renderTable(w io.Writer, p Payload) error {
	if len(p.Rows) == 0 {
		_, err := fmt.Fprintln(w, "(no artifacts attached)")
		return err
	}
	return output.RenderTable(w, p.Cols, p.Rows)
}

func renderJSON(w io.Writer, p Payload) error {
	switch p.Kind {
	case KindApk:
		_, err := w.Write(p.Apks)
		return err
	case KindBundle:
		_, err := w.Write(p.Bundles)
		return err
	}
	// Both raw bodies are already valid JSON: splice them rather than
	// re-encoding, so each value stays byte-for-byte the API's response.
	_, err := fmt.Fprintf(w, "{\"apks\":%s,\"bundles\":%s}\n", bytesOrNull(p.Apks), bytesOrNull(p.Bundles))
	return err
}

func bytesOrNull(b json.RawMessage) string {
	if len(b) == 0 {
		return "null"
	}
	return strings.TrimSpace(string(b))
}

// normalizeKind validates --kind: "" means both, anything else must be one of
// the two artifact kinds (CLI misuse otherwise, exit 2).
func normalizeKind(k string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "":
		return "", nil
	case KindApk:
		return KindApk, nil
	case KindBundle:
		return KindBundle, nil
	default:
		return "", exit.Usagef("--kind must be apk or bundle")
	}
}

// BuildRows merges both responses into one list ordered by versionCode, with
// apk before bundle on a tie (the same version code cannot normally carry both,
// but the order must be deterministic either way).
func BuildRows(apks artifacts.ApksListResponse, bundles artifacts.BundlesListResponse) []Row {
	rows := make([]Row, 0, len(apks.Apks)+len(bundles.Bundles))
	for _, a := range apks.Apks {
		rows = append(rows, Row{Kind: KindApk, VersionCode: a.VersionCode, Sha256: a.Binary.Sha256})
	}
	for _, b := range bundles.Bundles {
		rows = append(rows, Row{Kind: KindBundle, VersionCode: b.VersionCode, Sha256: b.Sha256})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].VersionCode != rows[j].VersionCode {
			return rows[i].VersionCode < rows[j].VersionCode
		}
		return rows[i].Kind < rows[j].Kind
	})
	return rows
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	kind, err := normalizeKind(in.Kind)
	if err != nil {
		return nil, err
	}
	cols, err := ResolveColumns(in.Columns)
	if err != nil {
		return nil, err
	}
	pkg := strings.TrimSpace(in.Package)
	if pkg == "" && rc.Resolved != nil {
		pkg = strings.TrimSpace(rc.Resolved.Pin)
	}
	if pkg == "" {
		return nil, exit.Usagef("no package: pass --package <pkg> or run gplay init in your repo")
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	var (
		apks    artifacts.ApksListResponse
		bundles artifacts.BundlesListResponse
		rawA    json.RawMessage
		rawB    json.RawMessage
	)
	read := func(editID string) error {
		if kind != KindBundle {
			a, r, e := artifacts.ListApks(rc.Ctx, httpClient, pkg, editID)
			if e != nil {
				return e
			}
			apks, rawA = a, r
		}
		if kind != KindApk {
			b, r, e := artifacts.ListBundles(rc.Ctx, httpClient, pkg, editID)
			if e != nil {
				return e
			}
			bundles, rawB = b, r
		}
		return nil
	}

	// A pinned explicit Edit (`gplay edits begin`) is reused as-is: reading in
	// it shows artifacts uploaded there but not yet committed, and the pin's
	// owner decides when to commit or discard. Otherwise a read-only Edit is
	// opened and always discarded (DESIGN §4).
	explicitEditID, err := rc.ExplicitEditID(pkg)
	if err != nil {
		return nil, err
	}
	if explicitEditID != "" {
		err = read(explicitEditID)
	} else {
		err = edits.WithReadOnlyEdit(rc.Ctx, httpClient, pkg, read)
	}
	if err != nil {
		return nil, err
	}
	return Payload{Rows: BuildRows(apks, bundles), Cols: cols, Apks: rawA, Bundles: rawB, Kind: kind}, nil
}

// NewCommand returns the cobra command for `gplay releases artifacts list`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the APKs and App Bundles attached to an app",
		Long: `List the APKs and App Bundles currently attached to the app, one row per
artifact with its kind (apk or bundle), versionCode, and sha256: the version
codes you can reference from ` + "`gplay releases promote`" + ` or ` + "`gplay releases rollout`" + `.

Reads edits.apks.list and edits.bundles.list inside a read-only Edit
(open, read, discard; nothing is committed). When an explicit Edit is pinned
for the package (` + "`gplay edits begin`" + `), the read happens inside it instead, so
artifacts uploaded there and not yet committed are visible.

--kind apk|bundle restricts the listing to one resource (only that request
is sent). Rows are ordered by versionCode.

Default table columns: kind, versionCode, sha256. --output json is the raw
API response: {"apks": ..., "bundles": ...} with both kinds, or the single
list response verbatim with --kind (ADR-0003).`,
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
	cmd.Flags().StringVar(&in.Kind, "kind", "", "restrict to one artifact kind: apk or bundle (default: both)")
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns (default: "+strings.Join(columns.DefaultKeys(), ",")+")")
	return cmd
}
