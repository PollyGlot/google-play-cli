package schema

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

// fixtureIndex is a three-method slice of the Schema index. The goldens pin the
// view's shape, not the embedded Discovery surface (frozen by its own snapshot),
// so a Discovery refresh must not churn them. Descriptions carry <, > and &
// because Google's verbatim text does.
func fixtureIndex() schemaindex.Index {
	return schemaindex.Index{
		Methods: map[string]schemaindex.Method{
			"androidpublisher.edits.tracks.update": {
				HTTPMethod:  "PUT",
				Path:        "androidpublisher/v3/applications/{packageName}/edits/{editId}/tracks/{track}",
				Description: "Updates a track & its releases.",
				Parameters: []schemaindex.Param{
					{Name: "editId", In: "path", Type: "string", Required: true, Description: "Identifier of the edit."},
					{Name: "packageName", In: "path", Type: "string", Required: true, Description: "Package name of the app."},
					{Name: "track", In: "path", Type: "string", Required: true, Description: "Identifier of the track."},
				},
				Request:  "Track",
				Response: "Track",
			},
			"androidpublisher.edits.tracks.get": {
				HTTPMethod: "GET",
				Path:       "androidpublisher/v3/applications/{packageName}/edits/{editId}/tracks/{track}",
				Response:   "Track",
			},
			"androidpublisher.edits.bundles.list": {
				HTTPMethod: "GET",
				Path:       "androidpublisher/v3/applications/{packageName}/edits/{editId}/bundles",
				Response:   "BundlesListResponse",
			},
		},
		Schemas: map[string]schemaindex.Schema{
			"Track": {
				Description: "A track configuration. The maximum number of releases is <= 2.",
				Properties: map[string]schemaindex.Property{
					"releases": {Ref: "TrackRelease", Repeated: true, Description: "In a read request, represents all active releases in the track."},
					"track":    {Type: "string", Description: "Identifier of the track."},
				},
			},
			"TrackRelease": {
				Description: "A release within a track.",
				Properties: map[string]schemaindex.Property{
					"status": {Type: "string", Enum: []string{"statusUnspecified", "draft", "inProgress", "halted", "completed"}},
				},
			},
			"BundlesListResponse": {
				Properties: map[string]schemaindex.Property{
					"bundles": {Ref: "Bundle", Repeated: true},
				},
			},
		},
	}
}

// payloadFor mirrors Run minus the embedded index load, so the goldens exercise
// the real match and compact decision on the fixture.
func payloadFor(in Input) Payload {
	idx := fixtureIndex()
	return Payload{Index: idx, Result: match(idx, in), Compact: in.List}
}

// TestRenderJSON_methodDetail_golden freezes a method match: the method with
// its parameters, plus its request/response schema expanded one hop (Track),
// while the nested TrackRelease stays a $ref name.
func TestRenderJSON_methodDetail_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "method_detail.json.golden", payloadFor(Input{Query: "edits.tracks.update"}))
}

// TestRenderJSON_schemaMatch_golden freezes the OR of the projections: "track"
// matches both tracks methods by id and two schemas by name, while
// edits.bundles.list and its response stay out.
func TestRenderJSON_schemaMatch_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "schema_match.json.golden", payloadFor(Input{Query: "track"}))
}

// TestRenderJSON_list_golden freezes --list: the full method catalog and an
// empty schemas object, since a compact browse expands nothing.
func TestRenderJSON_list_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "list.json.golden", payloadFor(Input{List: true}))
}

// TestRenderJSON_noMatch_golden pins the zero-match body to two empty objects,
// never null: the command exits 0 and a consumer still gets both keys.
func TestRenderJSON_noMatch_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "no_match.json.golden", payloadFor(Input{Query: "nosuchthing"}))
}

// TestRenderJSON_codes_golden freezes the whole diagnostic-code catalog: each
// code string is a public contract (ADR-0044), so adding, renaming or
// reclassifying one must show up as a golden diff.
func TestRenderJSON_codes_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "codes.json.golden", CodesPayload{})
}
