package schemaindex_test

import (
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

// fragment is a representative slice of a Discovery doc covering every transform
// the derive walk performs: a nested resource tree, a path-param method with a
// request/response $ref, a no-body method (DELETE), a query enum param, and
// schema properties of each shape (scalar+enum, direct $ref, array-of-$ref,
// array-of-scalar).
const fragment = `{
  "name": "androidpublisher",
  "version": "v3",
  "revision": "20260608",
  "resources": {
    "edits": {
      "methods": {
        "delete": {
          "id": "androidpublisher.edits.delete",
          "httpMethod": "DELETE",
          "flatPath": "androidpublisher/v3/applications/{packageName}/edits/{editId}",
          "description": "Deletes an app edit.",
          "parameters": {
            "packageName": {"description": "Package name of the app.", "location": "path", "required": true, "type": "string"},
            "editId": {"description": "Identifier of the edit.", "location": "path", "required": true, "type": "string"}
          }
        }
      },
      "resources": {
        "tracks": {
          "methods": {
            "update": {
              "id": "androidpublisher.edits.tracks.update",
              "httpMethod": "PUT",
              "flatPath": "androidpublisher/v3/applications/{packageName}/edits/{editId}/tracks/{track}",
              "description": "Updates a track.",
              "parameters": {
                "track": {"description": "Identifier of the track.", "location": "path", "required": true, "type": "string"},
                "packageName": {"description": "Package name of the app.", "location": "path", "required": true, "type": "string"},
                "editId": {"description": "Identifier of the edit.", "location": "path", "required": true, "type": "string"}
              },
              "request": {"$ref": "Track"},
              "response": {"$ref": "Track"}
            }
          }
        },
        "commit": {
          "methods": {
            "commit": {
              "id": "androidpublisher.edits.commit",
              "httpMethod": "POST",
              "flatPath": "androidpublisher/v3/applications/{packageName}/edits/{editId}:commit",
              "parameters": {
                "changesInReviewBehavior": {
                  "description": "Optional behavior.",
                  "location": "query",
                  "type": "string",
                  "enum": ["CANCEL_IN_REVIEW_AND_SUBMIT", "ERROR_IF_IN_REVIEW"]
                }
              },
              "response": {"$ref": "AppEdit"}
            }
          }
        }
      }
    }
  },
  "schemas": {
    "Track": {
      "id": "Track",
      "description": "A track configuration.",
      "type": "object",
      "properties": {
        "track": {"description": "Identifier of the track.", "type": "string"},
        "releases": {"description": "Active releases.", "type": "array", "items": {"$ref": "TrackRelease"}}
      }
    },
    "TrackRelease": {
      "id": "TrackRelease",
      "type": "object",
      "properties": {
        "status": {
          "description": "The status of the release.",
          "type": "string",
          "enum": ["draft", "inProgress", "halted", "completed"]
        },
        "countryTargeting": {"$ref": "CountryTargeting", "description": "Country restriction."},
        "versionCodes": {"description": "Version codes.", "type": "array", "items": {"format": "int64", "type": "string"}}
      }
    }
  }
}`

func deriveFragment(t *testing.T) schemaindex.Index {
	t.Helper()
	idx, err := schemaindex.Derive([]byte(fragment))
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	return idx
}

func TestDerive_revisionAndCounts(t *testing.T) {
	idx := deriveFragment(t)
	if idx.Revision != "20260608" {
		t.Errorf("Revision = %q, want 20260608", idx.Revision)
	}
	if len(idx.Methods) != 3 {
		t.Errorf("got %d methods, want 3 (delete, tracks.update, commit)", len(idx.Methods))
	}
	if len(idx.Schemas) != 2 {
		t.Errorf("got %d schemas, want 2", len(idx.Schemas))
	}
}

func TestDerive_methodKeyedByNativeID_pathPrefixStripped(t *testing.T) {
	idx := deriveFragment(t)
	m, ok := idx.Methods["androidpublisher.edits.tracks.update"]
	if !ok {
		t.Fatal("missing method androidpublisher.edits.tracks.update")
	}
	if m.HTTPMethod != "PUT" {
		t.Errorf("httpMethod = %q, want PUT", m.HTTPMethod)
	}
	// flatPath minus the constant `androidpublisher/v3/` prefix.
	if want := "applications/{packageName}/edits/{editId}/tracks/{track}"; m.Path != want {
		t.Errorf("path = %q, want %q", m.Path, want)
	}
	if m.Description != "Updates a track." {
		t.Errorf("description = %q (verbatim expected)", m.Description)
	}
	if m.Request != "Track" || m.Response != "Track" {
		t.Errorf("request/response = %q/%q, want Track/Track", m.Request, m.Response)
	}
}

func TestDerive_parametersSortedByName(t *testing.T) {
	idx := deriveFragment(t)
	m := idx.Methods["androidpublisher.edits.tracks.update"]
	got := make([]string, len(m.Parameters))
	for i, p := range m.Parameters {
		got[i] = p.Name
	}
	want := []string{"editId", "packageName", "track"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("param order = %v, want %v (sorted by name)", got, want)
	}
	for _, p := range m.Parameters {
		if p.In != "path" {
			t.Errorf("param %q in = %q, want path", p.Name, p.In)
		}
		if !p.Required {
			t.Errorf("param %q should be required", p.Name)
		}
	}
}

func TestDerive_queryParamEnum(t *testing.T) {
	idx := deriveFragment(t)
	m := idx.Methods["androidpublisher.edits.commit"]
	if len(m.Parameters) != 1 {
		t.Fatalf("commit params = %d, want 1", len(m.Parameters))
	}
	p := m.Parameters[0]
	if p.In != "query" {
		t.Errorf("param in = %q, want query", p.In)
	}
	if len(p.Enum) != 2 || p.Enum[0] != "CANCEL_IN_REVIEW_AND_SUBMIT" {
		t.Errorf("param enum = %v, want the two commit behaviors", p.Enum)
	}
}

func TestDerive_noBodyMethodOmitsRequestResponse(t *testing.T) {
	idx := deriveFragment(t)
	m := idx.Methods["androidpublisher.edits.delete"]
	if m.Request != "" || m.Response != "" {
		t.Errorf("delete request/response = %q/%q, want both empty", m.Request, m.Response)
	}
	// commit has a response but no request.
	c := idx.Methods["androidpublisher.edits.commit"]
	if c.Request != "" {
		t.Errorf("commit request = %q, want empty", c.Request)
	}
	if c.Response != "AppEdit" {
		t.Errorf("commit response = %q, want AppEdit", c.Response)
	}
}

func TestDerive_schemaProperties(t *testing.T) {
	idx := deriveFragment(t)

	track, ok := idx.Schemas["Track"]
	if !ok {
		t.Fatal("missing schema Track")
	}
	if track.Description != "A track configuration." {
		t.Errorf("Track description = %q (verbatim expected)", track.Description)
	}

	// scalar
	if got := track.Properties["track"]; got.Type != "string" || got.Repeated || got.Ref != "" {
		t.Errorf("Track.track = %+v, want scalar string", got)
	}
	// array-of-$ref → repeated + $ref to element, no "array" type leakage
	rel := track.Properties["releases"]
	if !rel.Repeated || rel.Ref != "TrackRelease" || rel.Type != "" {
		t.Errorf("Track.releases = %+v, want repeated $ref TrackRelease", rel)
	}

	tr := idx.Schemas["TrackRelease"]
	// scalar + enum
	st := tr.Properties["status"]
	if st.Type != "string" || len(st.Enum) != 4 || st.Repeated {
		t.Errorf("TrackRelease.status = %+v, want string enum[4]", st)
	}
	// direct $ref
	ct := tr.Properties["countryTargeting"]
	if ct.Ref != "CountryTargeting" || ct.Repeated || ct.Type != "" {
		t.Errorf("TrackRelease.countryTargeting = %+v, want direct $ref", ct)
	}
	// array-of-scalar → repeated + element type, no $ref
	vc := tr.Properties["versionCodes"]
	if !vc.Repeated || vc.Type != "string" || vc.Ref != "" {
		t.Errorf("TrackRelease.versionCodes = %+v, want repeated scalar string", vc)
	}
}

func TestRender_deterministicAndRoundTrips(t *testing.T) {
	a, err := schemaindex.Render([]byte(fragment))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	b, err := schemaindex.Render([]byte(fragment))
	if err != nil {
		t.Fatalf("Render (2nd): %v", err)
	}
	if string(a) != string(b) {
		t.Error("Render is not deterministic across calls")
	}
	if !strings.HasSuffix(string(a), "\n") {
		t.Error("rendered index should end with a trailing newline")
	}
	// Descriptions carry URLs / punctuation; HTML escaping must be off so the
	// text stays verbatim and readable.
	if strings.Contains(string(a), `<`) || strings.Contains(string(a), `&`) {
		t.Error("rendered index has HTML-escaped characters; want SetEscapeHTML(false)")
	}

	idx, err := schemaindex.Load(a)
	if err != nil {
		t.Fatalf("Load(Render()): %v", err)
	}
	if _, ok := idx.Methods["androidpublisher.edits.tracks.update"]; !ok {
		t.Error("round-tripped index lost tracks.update")
	}
}
