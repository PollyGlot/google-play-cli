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

const (
	KindApk    = "apk"
	KindBundle = "bundle"
)

type Input struct {
	Package string
	Format  string
	Columns string
}

type Row struct {
	Kind        string
	VersionCode int64
	Sha256      string
}

var columns = output.NewColumnSet(
	output.Column[Row]{Key: "kind", Header: "KIND", Value: func(r Row) string { return r.Kind }},
	output.Column[Row]{Key: "versionCode", Header: "VERSION_CODE", Value: func(r Row) string { return strconv.FormatInt(r.VersionCode, 10) }},
	output.Column[Row]{Key: "sha256", Header: "SHA256", Value: func(r Row) string { return r.Sha256 }},
)

func ResolveColumns(spec string) ([]output.Column[Row], error) { return columns.Resolve(spec) }

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
	_, err := fmt.Fprintf(w, "{\"apks\":%s,\"bundles\":%s}\n", bytesOrNull(p.Apks), bytesOrNull(p.Bundles))
	return err
}

func bytesOrNull(b json.RawMessage) string {
	if len(b) == 0 {
		return "null"
	}
	return strings.TrimSpace(string(b))
}

func normalizeKind(k string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "":
		return "", nil
	case KindApk:
		return KindApk, nil
	case KindBundle:
		return KindBundle, nil
	default:
		return "", exit.Usagef("--format must be apk or bundle")
	}
}

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

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	kind, err := normalizeKind(in.Format)
	if err != nil {
		return nil, err
	}
	cols, err := ResolveColumns(in.Columns)
	if err != nil {
		return nil, err
	}
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
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

--format apk|bundle restricts the listing to one resource (only that request
is sent). Rows are ordered by versionCode.

Default table columns: kind, versionCode, sha256. --output json is the raw
API response: {"apks": ..., "bundles": ...} with both kinds, or the single
list response verbatim with --format.`,
		Example: `  gplay releases artifacts list
  gplay releases artifacts list --format bundle
  gplay releases artifacts list --output json`,
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
	cmd.Flags().StringVar(&in.Format, "format", "", "restrict to one artifact format: apk or bundle (default: both)")
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns (default: "+strings.Join(columns.DefaultKeys(), ",")+")")
	return cmd
}
