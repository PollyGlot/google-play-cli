package list

import (
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/apihint"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
)

type Input struct {
	Package string
	Columns string
}

var StandardTracks = []string{"internal", "alpha", "beta", "production"}

type TrackRow struct {
	Track        string
	Kind         string
	Release      string
	Status       string
	UserFraction float64
	VersionCodes []string
	HasRelease   bool
}

func BuildRows(apiTracks []tracks.Track) []TrackRow {
	byName := make(map[string]tracks.Track, len(apiTracks))
	standard := make(map[string]bool, len(StandardTracks))
	for _, t := range apiTracks {
		byName[t.Track] = t
	}
	for _, s := range StandardTracks {
		standard[s] = true
	}

	rows := make([]TrackRow, 0, len(StandardTracks)+len(apiTracks))
	for _, s := range StandardTracks {
		rows = append(rows, makeRow(s, "standard", byName[s]))
	}
	for _, t := range apiTracks {
		if standard[t.Track] {
			continue
		}
		rows = append(rows, makeRow(t.Track, "custom", t))
	}
	return rows
}

func makeRow(name, kind string, t tracks.Track) TrackRow {
	row := TrackRow{Track: name, Kind: kind}
	if top, ok := topRelease(t.Releases); ok {
		row.HasRelease = true
		row.Release = top.Name
		row.Status = top.Status
		row.UserFraction = top.UserFraction
		row.VersionCodes = top.VersionCodes
	}
	return row
}

func topRelease(rels []tracks.Release) (tracks.Release, bool) {
	if len(rels) == 0 {
		return tracks.Release{}, false
	}
	best := rels[0]
	bestCode := maxVersionCode(best.VersionCodes)
	for _, r := range rels[1:] {
		if c := maxVersionCode(r.VersionCodes); c > bestCode {
			best, bestCode = r, c
		}
	}
	return best, true
}

func maxVersionCode(codes []string) int64 {
	max := int64(-1)
	for _, c := range codes {
		if n, err := strconv.ParseInt(c, 10, 64); err == nil && n > max {
			max = n
		}
	}
	return max
}

const (
	colTrack        = "track"
	colKind         = "kind"
	colRelease      = "release"
	colStatus       = "status"
	colUserFraction = "userFraction"
	colVersionCodes = "versionCodes"
)

var columns = output.NewColumnSet(
	output.Column[TrackRow]{Key: colTrack, Header: "TRACK", Value: func(r TrackRow) string { return r.Track }},
	output.Column[TrackRow]{Key: colKind, Header: "KIND", Value: func(r TrackRow) string { return r.Kind }},
	output.Column[TrackRow]{Key: colRelease, Header: "RELEASE", Value: func(r TrackRow) string { return r.Release }},
	output.Column[TrackRow]{Key: colStatus, Header: "STATUS", Value: func(r TrackRow) string { return r.Status }},
	output.Column[TrackRow]{Key: colUserFraction, Header: "USER_FRACTION", Value: formatUserFraction},
	output.Column[TrackRow]{Key: colVersionCodes, Header: "VERSION_CODES", Value: func(r TrackRow) string { return strings.Join(r.VersionCodes, ",") }},
)

func ResolveColumns(spec string) ([]output.Column[TrackRow], error) {
	return columns.Resolve(spec)
}

func formatUserFraction(r TrackRow) string {
	if !r.HasRelease {
		return ""
	}
	switch r.Status {
	case tracks.StatusCompleted, tracks.StatusDraft:
		return ""
	}
	return output.Percent(r.UserFraction)
}

type Payload struct {
	Raw     json.RawMessage           `json:"-"`
	Rows    []TrackRow                `json:"-"`
	Columns []output.Column[TrackRow] `json:"-"`
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, p.Columns, p.Rows) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, p.Columns, p.Rows) },
	}
}

func renderJSON(w io.Writer, p Payload) error {
	_, err := w.Write(p.Raw)
	return err
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}

	cols, err := ResolveColumns(in.Columns)
	if err != nil {
		return nil, err
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	var (
		parsed []tracks.Track
		raw    json.RawMessage
	)
	if err := edits.WithReadOnlyEdit(rc.Ctx, httpClient, pkg, func(editID string) error {
		ts, r, e := tracks.List(rc.Ctx, httpClient, pkg, editID)
		if e != nil {
			return e
		}
		parsed, raw = ts, r
		return nil
	}); err != nil {
		return nil, apihint.ForPackage(pkg, err)
	}

	return Payload{Raw: raw, Rows: BuildRows(parsed), Columns: cols}, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every track configured for a package",
		Long: `List every track configured for --package: the four standard tracks
(internal, alpha, beta, production) plus any custom closed tracks the
service account can see. Each row summarizes the track's top release (the
highest version code on it); a standard track that has never been used
still appears, with empty release columns.

Reads the tracks inside a read-only Edit (open → tracks.list → discard);
nothing is committed. Single-track listing of every coexisting release is
the job of ` + "`gplay releases list --track <T>`" + `.

Default table columns: track, kind, release, status, userFraction,
versionCodes. Override with --columns track,status,...  (--output json is
the raw tracks.list payload; --output markdown renders a Markdown table.)`,
		Example: `  gplay tracks list
  gplay tracks list --columns track,status,versionCodes
  gplay tracks list --output json | jq -r '.tracks[].track'`,
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
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns to show (default: "+strings.Join(columns.DefaultKeys(), ",")+")")
	return cmd
}
