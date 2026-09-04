// Package schemaindex defines the normalized, embeddable projection of a Google
// API Discovery document: the Schema index (see CONTEXT.md and ADR-0022) that
// `gplay schema` queries offline.
//
// Unlike internal/discovery (build-and-maintenance tooling that is never
// compiled into the binary), this package IS compiled in: it carries the index
// data types, the deterministic derive/render logic, the embedded
// `schema_index.json` itself (Embedded), and the loader it is parsed with. It
// imports only the standard library (no net/http, no Google SDK) so embedding
// it does not contradict ADR-0007: the index is inert reference data, not an
// API-calling dependency.
//
// The index is the normalized two-section shape locked by the PRD (#199):
//
//   - Methods: keyed by Google's native RPC method id
//     (`androidpublisher.edits.tracks.update`). The id's leading segment is the
//     service discriminator, so the index is multi-service-ready with no schema
//     change (vitals, #49).
//   - Schemas: a dictionary keyed by type name. Methods point at their
//     request/response schema by name; nested properties point by `$ref` name
//     into the same dictionary. The dictionary itself is the depth mechanism;
//     there is no duplication and no arbitrary depth-truncation.
package schemaindex

import (
	_ "embed"
	"encoding/json"
)

// embeddedJSON is the committed, multi-service Schema index compiled into the
// binary: a deterministic projection of every Discovery snapshot under
// docs/discovery/ (regenerate via `make schema-index-update`). It lives here,
// in the shared index package rather than in any one command, so every
// in-binary consumer reads the SAME index: `gplay schema` introspection and the
// vitals metric/dimension validation (#49) both call Embedded(). Embedding it
// is what makes those features work offline straight from `go install` with no
// build-time generation; it is inert reference data, not an API-calling
// dependency, so it does not contradict ADR-0007.
//
//go:embed schema_index.json
var embeddedJSON []byte

// Embedded loads the index compiled into the binary. It is the single accessor
// every in-binary consumer uses, so the embedded bytes are parsed in one place.
func Embedded() (Index, error) {
	return Load(embeddedJSON)
}

// Index is the whole Schema index: a Methods section and a Schemas dictionary,
// plus the upstream Discovery revision stamp it was derived from.
type Index struct {
	Revision string             `json:"revision,omitempty"`
	Services map[string]Service `json:"services,omitempty"`
	Methods  map[string]Method  `json:"methods"`
	Schemas  map[string]Schema  `json:"schemas"`
}

// Service carries the two document-level values a method id alone cannot give:
// where the service lives and what constant prefix its paths hang off. It is
// keyed in Index.Services by the service discriminator, i.e. the leading
// segment of a method id (`androidpublisher`, `playdeveloperreporting`, ...).
//
// It exists because Method.Path is a base-relative fragment, and that base is
// NOT uniform across Google's documents: androidpublisher flatPaths start with
// `androidpublisher/v3/` while gamesConfiguration's start with
// `games/v1configuration/` and playdeveloperreporting's with `v1beta1/`.
// Recording the prefix that was actually stripped keeps the reconstruction
// exact: RootURL + BasePath + Method.Path is the method's absolute URL
// template, byte for byte what the hand-rolled call sites build today.
type Service struct {
	// RootURL is Discovery's `rootUrl`, with its trailing slash (e.g.
	// "https://androidpublisher.googleapis.com/").
	RootURL string `json:"rootUrl,omitempty"`
	// BasePath is the constant prefix stripped off every flatPath of this
	// document, trailing slash included ("androidpublisher/v3/"), or empty
	// when the document's flatPaths carry no such prefix.
	BasePath string `json:"basePath,omitempty"`
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
	// UploadPath is the media-upload path template of a method that accepts a
	// payload (Discovery's mediaUpload simple-protocol path), relative to the
	// service RootURL and WITHOUT a leading slash
	// ("upload/androidpublisher/v3/applications/{packageName}/edits/{editId}/bundles").
	// It is a second, distinct path (a different prefix, not a suffix of Path),
	// so it cannot be derived from Path: `releases upload`, the mapping upload
	// and the image uploads all need it verbatim. Empty for the vast majority
	// of methods, which take no media.
	UploadPath string `json:"uploadPath,omitempty"`
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
