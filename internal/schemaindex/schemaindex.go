// Package schemaindex defines the normalized, embeddable projection of a Google
// API Discovery document — the Schema index (see CONTEXT.md and ADR-0022) that
// `gplay schema` queries offline.
//
// Unlike internal/discovery (build-and-maintenance tooling that is never
// compiled into the binary), this package IS compiled in: it carries the index
// data types, the deterministic derive/render logic, and the loader the
// embedded `commands/schema/schema_index.json` is parsed with. It imports only
// the standard library — no net/http, no Google SDK — so embedding it does not
// contradict ADR-0007: the index is inert reference data, not an API-calling
// dependency.
//
// The index is the normalized two-section shape locked by the PRD (#199):
//
//   - Methods — keyed by Google's native RPC method id
//     (`androidpublisher.edits.tracks.update`). The id's leading segment is the
//     service discriminator, so the index is multi-service-ready with no schema
//     change (vitals, #49).
//   - Schemas — a dictionary keyed by type name. Methods point at their
//     request/response schema by name; nested properties point by `$ref` name
//     into the same dictionary. The dictionary itself is the depth mechanism;
//     there is no duplication and no arbitrary depth-truncation.
package schemaindex

import "encoding/json"

// Index is the whole Schema index: a Methods section and a Schemas dictionary,
// plus the upstream Discovery revision stamp it was derived from.
type Index struct {
	Revision string            `json:"revision,omitempty"`
	Methods  map[string]Method `json:"methods"`
	Schemas  map[string]Schema `json:"schemas"`
}

// Method is one API method entry, keyed in Index.Methods by its native RPC id.
// Request/Response carry the schema *name* (resolve it against Index.Schemas);
// they are omitted when the method has no body (e.g. edits.delete).
type Method struct {
	HTTPMethod  string  `json:"httpMethod"`
	Path        string  `json:"path"`
	Description string  `json:"description,omitempty"`
	Parameters  []Param `json:"parameters,omitempty"`
	Request     string  `json:"request,omitempty"`
	Response    string  `json:"response,omitempty"`
}

// Param is one method parameter. In is "path" or "query" (Discovery's
// `location`). Parameters are stored sorted by Name for a deterministic index.
type Param struct {
	Name        string   `json:"name"`
	In          string   `json:"in"`
	Type        string   `json:"type,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Description string   `json:"description,omitempty"`
}

// Schema is one type entry, keyed in Index.Schemas by its type name.
type Schema struct {
	Description string              `json:"description,omitempty"`
	Properties  map[string]Property `json:"properties,omitempty"`
}

// Property is one field of a Schema. Exactly one of Type (a scalar) or Ref (a
// pointer into Index.Schemas by name) carries the field's shape. Repeated marks
// an array; for a repeated field, Type/Ref describe the element. Enum lists the
// legal values of a scalar. Required is carried when Discovery marks it (the
// androidpublisher doc currently marks none, so it is omitted everywhere).
type Property struct {
	Type        string   `json:"type,omitempty"`
	Ref         string   `json:"$ref,omitempty"`
	Repeated    bool     `json:"repeated,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Description string   `json:"description,omitempty"`
}

// Load parses an embedded/committed index document into an Index. It is the
// inverse of Render's marshal step; commands/schema calls it on the embedded
// schema_index.json exactly once.
func Load(data []byte) (Index, error) {
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return Index{}, err
	}
	return idx, nil
}
