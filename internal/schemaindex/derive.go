package schemaindex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// discoveryDoc mirrors only the fields of a normalized Discovery document the
// derive walk reads. Everything else (scopes, etag, auth, icons, ...) is
// intentionally dropped — the index is a trimmed projection (D4).
type discoveryDoc struct {
	Name      string                       `json:"name"`
	Version   string                       `json:"version"`
	Revision  string                       `json:"revision"`
	Resources map[string]discoveryResource `json:"resources"`
	Schemas   map[string]discoverySchema   `json:"schemas"`
}

type discoveryResource struct {
	Methods   map[string]discoveryMethod   `json:"methods"`
	Resources map[string]discoveryResource `json:"resources"`
}

type discoveryMethod struct {
	ID          string                    `json:"id"`
	HTTPMethod  string                    `json:"httpMethod"`
	FlatPath    string                    `json:"flatPath"`
	Path        string                    `json:"path"`
	Description string                    `json:"description"`
	Parameters  map[string]discoveryParam `json:"parameters"`
	Request     *discoveryRef             `json:"request"`
	Response    *discoveryRef             `json:"response"`
}

type discoveryParam struct {
	Description string   `json:"description"`
	Location    string   `json:"location"`
	Required    bool     `json:"required"`
	Type        string   `json:"type"`
	Enum        []string `json:"enum"`
}

type discoveryRef struct {
	Ref string `json:"$ref"`
}

type discoverySchema struct {
	Description string                       `json:"description"`
	Properties  map[string]discoveryProperty `json:"properties"`
}

type discoveryProperty struct {
	Description string          `json:"description"`
	Type        string          `json:"type"`
	Ref         string          `json:"$ref"`
	Enum        []string        `json:"enum"`
	Required    bool            `json:"required"`
	Items       *discoveryItems `json:"items"`
}

type discoveryItems struct {
	Ref  string `json:"$ref"`
	Type string `json:"type"`
}

// Derive transforms one normalized Discovery snapshot into the Schema index. It
// is a straight walk of the Discovery structure — no envelope rewrite — keying
// methods by their native id and copying the schema dictionary by type name.
// Both the generator and the offline integrity test call Derive (via Render),
// so a re-derivation is byte-identical to the committed index.
func Derive(normalized []byte) (Index, error) {
	var doc discoveryDoc
	if err := json.Unmarshal(normalized, &doc); err != nil {
		return Index{}, fmt.Errorf("parse discovery snapshot: %w", err)
	}

	idx := Index{
		Revision: doc.Revision,
		Methods:  map[string]Method{},
		Schemas:  map[string]Schema{},
	}

	// The constant path prefix Google prepends to every flatPath, e.g.
	// "androidpublisher/v3/". Stripping it yields the REST path fragment users
	// recognise from the API reference.
	prefix := doc.Name + "/" + doc.Version + "/"

	var walk func(rs map[string]discoveryResource)
	walk = func(rs map[string]discoveryResource) {
		for _, r := range rs {
			for _, m := range r.Methods {
				idx.Methods[m.ID] = deriveMethod(m, prefix)
			}
			walk(r.Resources)
		}
	}
	walk(doc.Resources)

	for name, s := range doc.Schemas {
		idx.Schemas[name] = deriveSchema(s)
	}

	return idx, nil
}

func deriveMethod(m discoveryMethod, prefix string) Method {
	out := Method{
		HTTPMethod:  m.HTTPMethod,
		Path:        strings.TrimPrefix(m.FlatPath, prefix),
		Description: m.Description,
	}
	if m.Request != nil {
		out.Request = m.Request.Ref
	}
	if m.Response != nil {
		out.Response = m.Response.Ref
	}
	for name, p := range m.Parameters {
		out.Parameters = append(out.Parameters, Param{
			Name:        name,
			In:          p.Location,
			Type:        p.Type,
			Enum:        p.Enum,
			Required:    p.Required,
			Description: p.Description,
		})
	}
	sort.Slice(out.Parameters, func(i, j int) bool {
		return out.Parameters[i].Name < out.Parameters[j].Name
	})
	return out
}

func deriveSchema(s discoverySchema) Schema {
	out := Schema{Description: s.Description}
	if len(s.Properties) == 0 {
		return out
	}
	out.Properties = make(map[string]Property, len(s.Properties))
	for name, p := range s.Properties {
		out.Properties[name] = deriveProperty(p)
	}
	return out
}

func deriveProperty(p discoveryProperty) Property {
	out := Property{
		Description: p.Description,
		Required:    p.Required,
	}
	switch {
	case p.Items != nil:
		// An array: describe the element, never leak the outer "array" type.
		out.Repeated = true
		if p.Items.Ref != "" {
			out.Ref = p.Items.Ref
		} else {
			out.Type = p.Items.Type
		}
	case p.Ref != "":
		out.Ref = p.Ref
	default:
		out.Type = p.Type
		out.Enum = p.Enum
	}
	return out
}

// Merge folds several derived indexes into one. Methods are keyed by their
// service-prefixed RPC id and schemas by type name, so entries from distinct
// services (androidpublisher, playdeveloperreporting) coexist with no
// collision. The merged Revision is the FIRST input's revision — the primary
// service's stamp — keeping a single, deterministic top-level value (the index
// is keyed by id, not by revision, so this is a label only). Inputs are merged
// in slice order; with a stable Services order the result is deterministic.
func Merge(indexes ...Index) Index {
	out := Index{
		Methods: map[string]Method{},
		Schemas: map[string]Schema{},
	}
	for i, idx := range indexes {
		if i == 0 {
			out.Revision = idx.Revision
		}
		for id, m := range idx.Methods {
			out.Methods[id] = m
		}
		for name, s := range idx.Schemas {
			out.Schemas[name] = s
		}
	}
	return out
}

// marshal renders an Index to the committed/embedded form: 2-space indent, HTML
// escaping off (descriptions carry URLs and punctuation verbatim — ADR-0003),
// map keys emitted in sorted order by encoding/json, and a trailing newline.
func marshal(idx Index) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(idx); err != nil {
		return nil, fmt.Errorf("encode schema index: %w", err)
	}
	return buf.Bytes(), nil
}

// Render derives the index from a single normalized snapshot and marshals it to
// the committed/embedded form. Render is deterministic, so `make
// schema-index-update` and the integrity test agree byte-for-byte. Use
// RenderAll for the multi-service index the binary actually embeds.
func Render(normalized []byte) ([]byte, error) {
	idx, err := Derive(normalized)
	if err != nil {
		return nil, err
	}
	return marshal(idx)
}

// RenderAll derives and Merges every normalized snapshot into one multi-service
// index, then marshals it. This is the generator `make schema-index-update`
// runs and the offline integrity gate re-runs, so a re-render of the same
// committed snapshots (in the same order) is byte-identical to the embedded
// index.
func RenderAll(snapshots [][]byte) ([]byte, error) {
	indexes := make([]Index, 0, len(snapshots))
	for _, snap := range snapshots {
		idx, err := Derive(snap)
		if err != nil {
			return nil, err
		}
		indexes = append(indexes, idx)
	}
	return marshal(Merge(indexes...))
}
