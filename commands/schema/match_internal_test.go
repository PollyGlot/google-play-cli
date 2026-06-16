package schema

import (
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

func testIndex(t *testing.T) schemaindex.Index {
	t.Helper()
	idx, err := schemaindex.Embedded()
	if err != nil {
		t.Fatalf("load embedded index: %v", err)
	}
	return idx
}

// TestMatch_projections table-tests the pure match function across all three
// projections (method id, REST path, schema name), case-insensitivity, the
// --method filter, --list, and the no-match case.
func TestMatch_projections(t *testing.T) {
	idx := testIndex(t)

	tests := []struct {
		name        string
		in          Input
		wantMethod  string // a method id that must be present ("" = none required)
		wantSchema  string // a schema name that must be present ("" = none required)
		wantNoMatch bool
		check       func(t *testing.T, r result)
	}{
		{
			name:       "id projection",
			in:         Input{Query: "edits.tracks.update"},
			wantMethod: "androidpublisher.edits.tracks.update",
		},
		{
			name:       "id projection is case-insensitive",
			in:         Input{Query: "EDITS.TRACKS.UPDATE"},
			wantMethod: "androidpublisher.edits.tracks.update",
		},
		{
			name:       "path projection",
			in:         Input{Query: "tracks/{track}"},
			wantMethod: "androidpublisher.edits.tracks.update",
			check: func(t *testing.T, r result) {
				for id, m := range r.Methods {
					if !strings.Contains(strings.ToLower(id), "tracks/{track}") &&
						!strings.Contains(strings.ToLower(m.Path), "tracks/{track}") {
						t.Errorf("path-matched method %q has neither id nor path containing the query", id)
					}
				}
			},
		},
		{
			name:       "schema-name projection",
			in:         Input{Query: "TrackRelease"},
			wantSchema: "TrackRelease",
		},
		{
			name:        "no match",
			in:          Input{Query: "zzz-not-a-real-thing"},
			wantNoMatch: true,
		},
		{
			name: "method filter narrows to the verb",
			in:   Input{Query: "edits.tracks", Method: "GET"},
			check: func(t *testing.T, r result) {
				if len(r.Methods) == 0 {
					t.Fatal("expected some GET edits.tracks methods")
				}
				for id, m := range r.Methods {
					if m.HTTPMethod != "GET" {
						t.Errorf("method %q has verb %q, want GET", id, m.HTTPMethod)
					}
				}
			},
		},
		{
			name: "method filter alone (no query) browses the verb",
			in:   Input{Method: "DELETE"},
			check: func(t *testing.T, r result) {
				if len(r.Methods) < 5 {
					t.Errorf("DELETE-only browse returned %d methods, want the full DELETE set", len(r.Methods))
				}
				for id, m := range r.Methods {
					if m.HTTPMethod != "DELETE" {
						t.Errorf("method %q has verb %q, want DELETE", id, m.HTTPMethod)
					}
				}
				if len(r.Schemas) != 0 {
					t.Errorf("verb browse should not match schemas, got %d", len(r.Schemas))
				}
			},
		},
		{
			name: "list returns the full method surface",
			in:   Input{List: true},
			check: func(t *testing.T, r result) {
				if len(r.Methods) < methodFloorTest {
					t.Errorf("--list returned %d methods, want the full surface", len(r.Methods))
				}
				if len(r.Schemas) != 0 {
					t.Errorf("--list is a method catalog, want 0 schemas, got %d", len(r.Schemas))
				}
			},
		},
		{
			name: "list combined with method filter",
			in:   Input{List: true, Method: "PUT"},
			check: func(t *testing.T, r result) {
				if len(r.Methods) == 0 {
					t.Fatal("--list --method PUT returned nothing; PUT methods exist")
				}
				for id, m := range r.Methods {
					if m.HTTPMethod != "PUT" {
						t.Errorf("method %q has verb %q, want PUT", id, m.HTTPMethod)
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := match(idx, tc.in)
			if tc.wantNoMatch {
				if len(r.Methods) != 0 || len(r.Schemas) != 0 {
					t.Errorf("want no match, got %d methods / %d schemas", len(r.Methods), len(r.Schemas))
				}
			}
			if tc.wantMethod != "" {
				if _, ok := r.Methods[tc.wantMethod]; !ok {
					t.Errorf("missing expected method %q", tc.wantMethod)
				}
			}
			if tc.wantSchema != "" {
				if _, ok := r.Schemas[tc.wantSchema]; !ok {
					t.Errorf("missing expected schema %q", tc.wantSchema)
				}
			}
			if tc.check != nil {
				tc.check(t, r)
			}
		})
	}
}

const methodFloorTest = 120
