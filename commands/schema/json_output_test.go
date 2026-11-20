package schema

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

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

func payloadFor(in Input) Payload {
	idx := fixtureIndex()
	return Payload{Index: idx, Result: match(idx, in), Compact: in.List}
}

func TestRenderJSON_methodDetail_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "method_detail.json.golden", payloadFor(Input{Query: "edits.tracks.update"}))
}

func TestRenderJSON_schemaMatch_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "schema_match.json.golden", payloadFor(Input{Query: "track"}))
}

func TestRenderJSON_list_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "list.json.golden", payloadFor(Input{List: true}))
}

func TestRenderJSON_noMatch_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "no_match.json.golden", payloadFor(Input{Query: "nosuchthing"}))
}

func TestRenderJSON_codes_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "codes.json.golden", CodesPayload{})
}
