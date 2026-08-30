// Package schema implements `gplay schema`: an OFFLINE, no-auth,
// `[experimental]` reference command that introspects the Android Publisher API
// surface from an embedded Schema index (ADR-0022). It makes no network call
// and needs no credentials: it queries the index compiled into the binary.
//
// It is a category-3 reference/diagnostic meta-command (ADR-0019): a bare
// `gplay schema <query>` is a consistent shape, modeled on `team permissions`
// (a real Renderable command), not a verb-less resource read.
//
// The query matches, case-insensitively, across three projections OR'd
// together: native RPC method id, REST path, and schema/type name. A matched
// method inline-expands its request/response schema one hop (nested $refs are
// shown by name). --list prints the compact method catalog; --method filters by
// HTTP verb. A zero-match query prints a note on stderr and exits 0.
//
// --codes reuses this same offline introspection surface for a second catalog:
// gplay's own diagnostic codes (ADR-0044); see codes.go.
package schema

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

// Input is the request-shaped struct cobra builds from flags and args.
type Input struct {
	Query  string // free-text query, matched across id / path / schema name
	Method string // --method: filter the method projection by HTTP verb
	List   bool   // --list: print the compact catalog of all methods
	Codes  bool   // --codes: print gplay's diagnostic-code catalog instead of the API index
}

// result is the matched slice of the index: the methods, and the schemas
// matched directly by the schema-name projection. (One-hop inline expansions of
// a matched method's request/response schema are resolved at render time from
// the full index, not stored here.)
type result struct {
	Methods map[string]schemaindex.Method
	Schemas map[string]schemaindex.Schema
}

func (r result) empty() bool { return len(r.Methods) == 0 && len(r.Schemas) == 0 }

// match is the pure query function: given the index and the parsed Input, it
// returns the matched slice. Methods match when their id OR REST path contains
// the (case-insensitive) query; schemas match when their type name does. The
// three projections are OR'd. --method narrows the method projection to one
// verb; --list (or a bare --method with no query) browses the method surface.
func match(idx schemaindex.Index, in Input) result {
	r := result{
		Methods: map[string]schemaindex.Method{},
		Schemas: map[string]schemaindex.Schema{},
	}
	verb := strings.ToUpper(strings.TrimSpace(in.Method))
	q := strings.ToLower(strings.TrimSpace(in.Query))
	// Browse mode: --list, or a bare --method filter with no query text. Returns
	// the (verb-filtered) method catalog and no schemas.
	browse := in.List || (q == "" && verb != "")

	for id, m := range idx.Methods {
		if verb != "" && m.HTTPMethod != verb {
			continue
		}
		switch {
		case browse:
			r.Methods[id] = m
		case q != "" && (strings.Contains(strings.ToLower(id), q) || strings.Contains(strings.ToLower(m.Path), q)):
			r.Methods[id] = m
		}
	}

	// Schema-name projection: only in search mode (a browse is a method
	// catalog). A schema carries no HTTP verb, so --method does not apply to it.
	if !browse && q != "" {
		for name, s := range idx.Schemas {
			if strings.Contains(strings.ToLower(name), q) {
				r.Schemas[name] = s
			}
		}
	}
	return r
}

// Payload renders a matched slice of the Schema index. The JSON view is
// synthesized (the matched slice), NOT API pass-through: `gplay schema` wraps
// no Developer API call, so ADR-0003's pass-through rule does not apply (live
// precedent: `team permissions`).
type Payload struct {
	Index   schemaindex.Index // full index, for one-hop request/response expansion
	Result  result
	Compact bool // --list / verb-browse: render the compact method catalog
}

// Renderers satisfies output.Renderable.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.render(w, formatTable) },
		JSON:     func(w io.Writer) error { return p.renderJSON(w) },
		Markdown: func(w io.Writer) error { return p.render(w, formatMarkdown) },
	}
}

func (p Payload) sortedMethodIDs() []string {
	ids := make([]string, 0, len(p.Result.Methods))
	for id := range p.Result.Methods {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (p Payload) sortedSchemaNames() []string {
	names := make([]string, 0, len(p.Result.Schemas))
	for name := range p.Result.Schemas {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// expandedSchemas is the set of schemas the JSON view carries: the schema-name
// matches, plus each matched method's request/response schema resolved one hop
// from the full index. Nested $refs inside those schemas are NOT expanded:
// they appear as `$ref` names, the depth mechanism the dictionary provides.
func (p Payload) expandedSchemas() map[string]schemaindex.Schema {
	out := make(map[string]schemaindex.Schema, len(p.Result.Schemas))
	for name, s := range p.Result.Schemas {
		out[name] = s
	}
	if !p.Compact {
		for _, m := range p.Result.Methods {
			for _, name := range []string{m.Request, m.Response} {
				if name == "" {
					continue
				}
				if s, ok := p.Index.Schemas[name]; ok {
					out[name] = s
				}
			}
		}
	}
	return out
}

// --- table / markdown rendering -------------------------------------------

type renderFormat int

const (
	formatTable renderFormat = iota
	formatMarkdown
)

// catalogRow is one compact-catalog line (--list): id · httpMethod · path.
type catalogRow struct {
	id, httpMethod, path string
}

var catalogCols = []output.Column[catalogRow]{
	{Key: "id", Header: "ID", Value: func(r catalogRow) string { return r.id }},
	{Key: "method", Header: "HTTP", Value: func(r catalogRow) string { return r.httpMethod }},
	{Key: "path", Header: "PATH", Value: func(r catalogRow) string { return r.path }},
}

var paramCols = []output.Column[schemaindex.Param]{
	{Key: "name", Header: "NAME", Value: func(p schemaindex.Param) string { return p.Name }},
	{Key: "in", Header: "IN", Value: func(p schemaindex.Param) string { return p.In }},
	{Key: "type", Header: "TYPE", Value: func(p schemaindex.Param) string { return p.Type }},
	{Key: "required", Header: "REQUIRED", Value: func(p schemaindex.Param) string {
		if p.Required {
			return "yes"
		}
		return ""
	}},
	{Key: "enum", Header: "ENUM", Value: func(p schemaindex.Param) string { return strings.Join(p.Enum, ", ") }},
	{Key: "description", Header: "DESCRIPTION", Value: func(p schemaindex.Param) string { return p.Description }},
}

// propRow is one rendered schema property line.
type propRow struct {
	name, typ, enum, desc string
	repeated              bool
}

func (r propRow) typeCell() string {
	t := r.typ
	if r.repeated {
		t = "[]" + t
	}
	return t
}

var propCols = []output.Column[propRow]{
	{Key: "field", Header: "FIELD", Value: func(r propRow) string { return r.name }},
	{Key: "type", Header: "TYPE", Value: func(r propRow) string { return r.typeCell() }},
	{Key: "enum", Header: "ENUM", Value: func(r propRow) string { return r.enum }},
	{Key: "description", Header: "DESCRIPTION", Value: func(r propRow) string { return r.desc }},
}

// propRows turns a schema's properties into sorted, render-ready rows. A
// property's type cell is its scalar type or its $ref'd type name (the one-hop
// pointer); `repeated` is surfaced via the `[]` prefix.
func propRows(s schemaindex.Schema) []propRow {
	names := make([]string, 0, len(s.Properties))
	for name := range s.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([]propRow, 0, len(names))
	for _, name := range names {
		p := s.Properties[name]
		typ := p.Type
		if p.Ref != "" {
			typ = p.Ref
		}
		rows = append(rows, propRow{
			name:     name,
			typ:      typ,
			enum:     strings.Join(p.Enum, ", "),
			desc:     p.Description,
			repeated: p.Repeated,
		})
	}
	return rows
}

func renderRows[T any](w io.Writer, f renderFormat, cols []output.Column[T], rows []T) error {
	if f == formatMarkdown {
		return output.RenderMarkdown(w, cols, rows)
	}
	return output.RenderTable(w, cols, rows)
}

func (p Payload) render(w io.Writer, f renderFormat) error {
	if p.Compact {
		return p.renderCatalog(w, f)
	}
	return p.renderDetail(w, f)
}

func (p Payload) renderCatalog(w io.Writer, f renderFormat) error {
	rows := make([]catalogRow, 0, len(p.Result.Methods))
	for _, id := range p.sortedMethodIDs() {
		m := p.Result.Methods[id]
		rows = append(rows, catalogRow{id: id, httpMethod: m.HTTPMethod, path: m.Path})
	}
	return renderRows(w, f, catalogCols, rows)
}

func (p Payload) renderDetail(w io.Writer, f renderFormat) error {
	shownSchemas := map[string]bool{}
	first := true
	sep := func() error {
		if !first {
			if _, err := io.WriteString(w, "\n"); err != nil {
				return err
			}
		}
		first = false
		return nil
	}

	for _, id := range p.sortedMethodIDs() {
		if err := sep(); err != nil {
			return err
		}
		if err := p.renderMethod(w, f, id, p.Result.Methods[id], shownSchemas); err != nil {
			return err
		}
	}

	// Standalone schemas matched by name, skipping any already inline-expanded
	// under a method above.
	for _, name := range p.sortedSchemaNames() {
		if shownSchemas[name] {
			continue
		}
		if err := sep(); err != nil {
			return err
		}
		if err := p.renderSchema(w, f, name, p.Result.Schemas[name]); err != nil {
			return err
		}
	}
	return nil
}

func heading(w io.Writer, f renderFormat, level int, text string) error {
	if f == formatMarkdown {
		_, err := fmt.Fprintf(w, "%s %s\n\n", strings.Repeat("#", level), text)
		return err
	}
	_, err := fmt.Fprintf(w, "%s\n", text)
	return err
}

func (p Payload) renderMethod(w io.Writer, f renderFormat, id string, m schemaindex.Method, shown map[string]bool) error {
	if err := heading(w, f, 2, fmt.Sprintf("%s  %s %s", id, m.HTTPMethod, m.Path)); err != nil {
		return err
	}
	if m.Description != "" {
		if _, err := fmt.Fprintf(w, "%s\n", m.Description); err != nil {
			return err
		}
	}
	if len(m.Parameters) > 0 {
		if _, err := io.WriteString(w, "\nParameters\n"); err != nil {
			return err
		}
		if err := renderRows(w, f, paramCols, m.Parameters); err != nil {
			return err
		}
	}

	// One-hop inline expansion of request/response. When both point at the same
	// schema (common: e.g. tracks.update), render it once under a combined
	// label.
	if m.Request != "" && m.Request == m.Response {
		return p.renderRefSchema(w, f, "Request & response: "+m.Request, m.Request, shown)
	}
	if m.Request != "" {
		if err := p.renderRefSchema(w, f, "Request: "+m.Request, m.Request, shown); err != nil {
			return err
		}
	}
	if m.Response != "" {
		if err := p.renderRefSchema(w, f, "Response: "+m.Response, m.Response, shown); err != nil {
			return err
		}
	}
	if m.Request == "" && m.Response == "" {
		_, err := io.WriteString(w, "\n(no request or response body)\n")
		return err
	}
	return nil
}

// renderRefSchema inline-expands a request/response schema one hop. An unknown
// name (should not happen for a well-formed index) degrades to the name only.
func (p Payload) renderRefSchema(w io.Writer, f renderFormat, label, name string, shown map[string]bool) error {
	if _, err := fmt.Fprintf(w, "\n%s\n", label); err != nil {
		return err
	}
	s, ok := p.Index.Schemas[name]
	if !ok || len(s.Properties) == 0 {
		return nil
	}
	shown[name] = true
	return renderRows(w, f, propCols, propRows(s))
}

func (p Payload) renderSchema(w io.Writer, f renderFormat, name string, s schemaindex.Schema) error {
	if err := heading(w, f, 2, "Schema: "+name); err != nil {
		return err
	}
	if s.Description != "" {
		if _, err := fmt.Fprintf(w, "%s\n\n", s.Description); err != nil {
			return err
		}
	}
	if len(s.Properties) == 0 {
		return nil
	}
	return renderRows(w, f, propCols, propRows(s))
}

// --- JSON rendering --------------------------------------------------------

// jsonView is the stable, machine-parseable synthesized shape: the matched
// methods and the schemas (name matches plus one-hop request/response
// expansions), keyed exactly as the index keys them.
type jsonView struct {
	Methods map[string]schemaindex.Method `json:"methods"`
	Schemas map[string]schemaindex.Schema `json:"schemas"`
}

func (p Payload) renderJSON(w io.Writer) error {
	return output.WriteJSON(w, jsonView{
		Methods: p.Result.Methods,
		Schemas: p.expandedSchemas(),
	})
}

// --- command wiring --------------------------------------------------------

// Run is the business function the kernel invokes. Fully offline: it loads the
// embedded index, matches the query, and returns the matched slice. A zero-match
// query writes a note to stderr and still returns a (empty) Renderable so the
// command exits 0: "found nothing" is not a failure.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	// --codes answers a different question than the rest of the command (what
	// can fail, not what can be called), so it short-circuits before the index
	// is even loaded and ignores the query projections.
	if in.Codes {
		return CodesPayload{}, nil
	}
	idx, err := schemaindex.Embedded()
	if err != nil {
		return nil, fmt.Errorf("load embedded schema index: %w", err)
	}
	res := match(idx, in)
	compact := in.List || (strings.TrimSpace(in.Query) == "" && strings.TrimSpace(in.Method) != "")

	if res.empty() {
		// Best-effort note; a stderr write failure must not fail the command.
		_, _ = fmt.Fprintln(rc.Stderr, zeroMatchNote(in))
	}
	return Payload{Index: idx, Result: res, Compact: compact}, nil
}

// zeroMatchNote explains an empty result on stderr, echoing the query so a
// scripted caller can log it, and pointing at the catalog.
func zeroMatchNote(in Input) string {
	q := strings.TrimSpace(in.Query)
	switch {
	case q != "":
		return fmt.Sprintf("gplay schema: no API method or schema matches %q (try `gplay schema --list`)", q)
	case strings.TrimSpace(in.Method) != "":
		return fmt.Sprintf("gplay schema: no method with HTTP verb %q", strings.ToUpper(strings.TrimSpace(in.Method)))
	default:
		return "gplay schema: give a query (e.g. `gplay schema edits.tracks.update` or `gplay schema Track`) or use --list"
	}
}

// NewCommand returns the cobra command for `gplay schema`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "schema [query]",
		Short: "Introspect the Android Publisher API surface offline",
		Long: `Query an embedded, offline projection of the Android Publisher API
(the Schema index): does a method exist, what does it send and return, what
fields and enums does a type carry.

This command is OFFLINE: it makes no API call and needs no credentials. It
queries an index compiled into the binary, derived from the committed Discovery
snapshot.

The query matches, case-insensitively, across three projections:
  - the native RPC method id   (e.g. ` + "`edits.tracks.update`" + `)
  - a REST path fragment       (e.g. ` + "`tracks/{track}`" + `)
  - a schema / type name       (e.g. ` + "`Track`" + `)

A matched method shows its HTTP method, REST path, parameters, and a one-hop
inline expansion of its request/response schema (fields, types, enums, Google's
verbatim descriptions); nested types are shown by name. A matched schema is
rendered directly.

  --list             print the compact catalog (id · http · path) of all methods
  --method GET|POST|PATCH|PUT|DELETE
                     filter the method surface by HTTP verb (combinable)
  --codes            print gplay's diagnostic-code catalog (the stable "code"
                     values the JSON error envelope carries) instead of the
                     API index; ignores the query and the other flags

A query that matches nothing prints a note on stderr and exits 0.`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				in.Query = args[0]
			}
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().BoolVar(&in.List, "list", false, "print the compact catalog of all methods")
	cmd.Flags().StringVar(&in.Method, "method", "", "filter methods by HTTP verb (GET, POST, PATCH, PUT, DELETE)")
	cmd.Flags().BoolVar(&in.Codes, "codes", false, "print gplay's diagnostic-code catalog instead of the API index")
	return cmd
}
