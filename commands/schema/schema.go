// Package schema implements `gplay schema`: an OFFLINE, no-auth,
// `[experimental]` reference command that introspects the Android Publisher API
// surface from an embedded Schema index (ADR-0022). It makes no network call
// and needs no credentials — it queries the index compiled into the binary.
//
// It is a category-3 reference/diagnostic meta-command (ADR-0019): a bare
// `gplay schema <query>` is a consistent shape, modeled on `team permissions`
// (a real Renderable command), not a verb-less resource read.
//
// The skeleton (#200) matches on the native RPC method id only and renders the
// matched methods with their request/response schema *names*. Wider projections
// (path, schema name), --list/--method, one-hop inline schema expansion, and
// the markdown renderer land in #201.
package schema

import (
	_ "embed"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

// indexJSON is the embedded Schema index — a committed, deterministic
// projection of the Discovery snapshot (regenerate via `make
// schema-index-update`). Embedding it is what makes `gplay schema` work offline
// straight from `go install` with no build-time generation (precedent:
// internal/compliance/datasafety embeds reference.csv). It is inert reference
// data, not an API-calling dependency, so it does not contradict ADR-0007.
//
//go:embed schema_index.json
var indexJSON []byte

// Input is the request-shaped struct cobra builds from flags and args.
type Input struct {
	Query string
}

// result is the matched slice of the index: the methods (and, from #201, the
// schemas) a query selected. It is what every renderer reads from.
type result struct {
	Methods map[string]schemaindex.Method
	Schemas map[string]schemaindex.Schema
}

// match is the pure query function: given the index and the parsed Input, it
// returns the matched slice. The skeleton matches a method when its native id
// contains the (case-insensitive) query; an empty query matches nothing (the
// explicit "show everything" surface is --list, #201).
func match(idx schemaindex.Index, in Input) result {
	r := result{
		Methods: map[string]schemaindex.Method{},
		Schemas: map[string]schemaindex.Schema{},
	}
	q := strings.ToLower(strings.TrimSpace(in.Query))
	if q == "" {
		return r
	}
	for id, m := range idx.Methods {
		if strings.Contains(strings.ToLower(id), q) {
			r.Methods[id] = m
		}
	}
	return r
}

// Payload renders a matched slice of the Schema index. The JSON view is
// synthesized (the matched slice), NOT API pass-through — `gplay schema` wraps
// no Developer API call, so ADR-0003's pass-through rule does not apply (live
// precedent: `team permissions`).
type Payload struct {
	Result result
}

// Renderers satisfies output.Renderable. The skeleton ships table + json;
// markdown lands in #201.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table: func(w io.Writer) error { return p.renderTable(w) },
		JSON:  func(w io.Writer) error { return p.renderJSON(w) },
	}
}

// sortedMethodIDs returns the matched method ids in stable order.
func (p Payload) sortedMethodIDs() []string {
	ids := make([]string, 0, len(p.Result.Methods))
	for id := range p.Result.Methods {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
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

func (p Payload) renderTable(w io.Writer) error {
	for i, id := range p.sortedMethodIDs() {
		if i > 0 {
			if _, err := io.WriteString(w, "\n"); err != nil {
				return err
			}
		}
		if err := p.renderMethodTable(w, id, p.Result.Methods[id]); err != nil {
			return err
		}
	}
	return nil
}

func (p Payload) renderMethodTable(w io.Writer, id string, m schemaindex.Method) error {
	if _, err := fmt.Fprintf(w, "%s  %s %s\n", id, m.HTTPMethod, m.Path); err != nil {
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
		if err := output.RenderTable(w, paramCols, m.Parameters); err != nil {
			return err
		}
	}
	req := m.Request
	if req == "" {
		req = "—"
	}
	resp := m.Response
	if resp == "" {
		resp = "—"
	}
	_, err := fmt.Fprintf(w, "\nRequest:  %s\nResponse: %s\n", req, resp)
	return err
}

// jsonView is the stable, machine-parseable synthesized shape: the matched
// methods and schemas, keyed exactly as the index keys them.
type jsonView struct {
	Methods map[string]schemaindex.Method `json:"methods"`
	Schemas map[string]schemaindex.Schema `json:"schemas"`
}

func (p Payload) renderJSON(w io.Writer) error {
	return output.WriteJSON(w, jsonView{
		Methods: p.Result.Methods,
		Schemas: p.Result.Schemas,
	})
}

// Run is the business function the kernel invokes. Fully offline: it loads the
// embedded index, matches the query, and returns the matched slice. No Account,
// no network.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	idx, err := schemaindex.Load(indexJSON)
	if err != nil {
		return nil, fmt.Errorf("load embedded schema index: %w", err)
	}
	return Payload{Result: match(idx, in)}, nil
}

// NewCommand returns the cobra command for `gplay schema`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "schema [query]",
		Short: "[experimental] Introspect the Android Publisher API surface offline",
		Long: `Query an embedded, offline projection of the Android Publisher API
(the Schema index) — does a method exist, what does it send and return.

This command is OFFLINE: it makes no API call and needs no credentials. It
queries an index compiled into the binary, derived from the committed Discovery
snapshot.

The query matches the native RPC method id (e.g. ` + "`edits.tracks.update`" + `),
case-insensitive substring. A matched method shows its HTTP method, REST path,
parameters, and the names of its request/response schemas.

[experimental] — the surface, especially the --output json shape, may still
change.`,
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
	return cmd
}
