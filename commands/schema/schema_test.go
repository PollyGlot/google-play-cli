// Package schema_test exercises `gplay schema` at the business-function seam
// (Run → output.Renderable), against the REAL embedded index, so these tests
// double as proof the command runs fully OFFLINE (no Account, no transport) and
// that the embed is wired correctly.
package schema_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	schemacmd "github.com/PollyGlot/google-play-cli/commands/schema"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// newOfflineRC builds a RunContext with NO Account and NO transport: the
// command must complete here, proving it makes no network call and needs no
// credentials.
func newOfflineRC(t *testing.T) *kernel.RunContext {
	t.Helper()
	rc := kernel.NewForTest(context.Background(), kernel.Boot{}, kernel.Inputs{Format: output.FormatJSON})
	rc.Resolved = &config.Resolved{}
	return rc
}

func run(t *testing.T, in schemacmd.Input) output.Renderable {
	t.Helper()
	rc := newOfflineRC(t)
	r, err := schemacmd.Run(rc, in)
	if err != nil {
		t.Fatalf("schema must run offline: %v", err)
	}
	return r
}

func renderTable(t *testing.T, r output.Renderable) string {
	t.Helper()
	var b bytes.Buffer
	if err := r.Renderers().Table(&b); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	return b.String()
}

func renderJSON(t *testing.T, r output.Renderable) string {
	t.Helper()
	var b bytes.Buffer
	if err := r.Renderers().JSON(&b); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	return b.String()
}

// jsonView mirrors the synthesized --output json shape (the matched slice).
type jsonView struct {
	Methods map[string]struct {
		HTTPMethod string `json:"httpMethod"`
		Path       string `json:"path"`
		Request    string `json:"request"`
		Response   string `json:"response"`
		Parameters []struct {
			Name string `json:"name"`
			In   string `json:"in"`
		} `json:"parameters"`
	} `json:"methods"`
	Schemas map[string]struct {
		Properties map[string]struct {
			Type string `json:"type"`
			Ref  string `json:"$ref"`
		} `json:"properties"`
	} `json:"schemas"`
}

func parseJSON(t *testing.T, s string) jsonView {
	t.Helper()
	var v jsonView
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("JSON not parseable: %v\n%s", err, s)
	}
	return v
}

// TestQueryByID_table is the tracer bullet: a method-id query returns the
// matching method with httpMethod, path, sorted parameters, and request/
// response schema names.
func TestQueryByID_table(t *testing.T) {
	r := run(t, schemacmd.Input{Query: "edits.tracks.update"})
	table := renderTable(t, r)
	for _, want := range []string{
		"androidpublisher.edits.tracks.update",
		"PUT",
		"applications/{packageName}/edits/{editId}/tracks/{track}",
		"editId", "packageName", "track",
		"Track", // request/response schema name
	} {
		if !strings.Contains(table, want) {
			t.Errorf("table missing %q\n---\n%s", want, table)
		}
	}
}

// TestQueryByID_json asserts the synthesized JSON carries the matched method
// with its schema *names* (no inline expansion in the skeleton) and sorted
// path parameters.
func TestQueryByID_json(t *testing.T) {
	r := run(t, schemacmd.Input{Query: "edits.tracks.update"})
	v := parseJSON(t, renderJSON(t, r))

	m, ok := v.Methods["androidpublisher.edits.tracks.update"]
	if !ok {
		t.Fatalf("JSON missing matched method; got methods %v", keys(v))
	}
	if m.HTTPMethod != "PUT" {
		t.Errorf("httpMethod = %q, want PUT", m.HTTPMethod)
	}
	if m.Request != "Track" || m.Response != "Track" {
		t.Errorf("request/response = %q/%q, want Track/Track", m.Request, m.Response)
	}
	if len(m.Parameters) != 3 || m.Parameters[0].Name != "editId" {
		t.Errorf("parameters = %+v, want 3 sorted (editId first)", m.Parameters)
	}
	// A matched method inline-expands its request/response schema one hop, so
	// the synthesized slice carries Track (detailed one-hop coverage lives in
	// TestInlineExpansion_oneHop).
	if _, ok := v.Schemas["Track"]; !ok {
		t.Errorf("expected one-hop expansion to carry Track, got %v", v.Schemas)
	}
}

// TestQueryByID_substringMatchesMany asserts a partial id matches every method
// whose id contains it (case-insensitive).
func TestQueryByID_substringMatchesMany(t *testing.T) {
	r := run(t, schemacmd.Input{Query: "EDITS.TRACKS"})
	v := parseJSON(t, renderJSON(t, r))
	if len(v.Methods) < 2 {
		t.Errorf("expected several edits.tracks.* methods, got %d: %v", len(v.Methods), keys(v))
	}
	for id := range v.Methods {
		if !strings.Contains(id, "edits.tracks") {
			t.Errorf("matched non-matching id %q", id)
		}
	}
}

func keys(v jsonView) []string {
	out := make([]string, 0, len(v.Methods))
	for k := range v.Methods {
		out = append(out, k)
	}
	return out
}

// TestNewCommand_surface asserts the flag/name/experimental surface.
func TestNewCommand_surface(t *testing.T) {
	cmd := schemacmd.NewCommand(kernel.Boot{})
	if cmd.Use == "" || !strings.HasPrefix(cmd.Use, "schema") {
		t.Errorf("cmd.Use = %q, want it to start with schema", cmd.Use)
	}
	if cmd.Flags().Lookup("output") == nil {
		t.Error("missing --output flag")
	}
	// The [experimental] marker is no longer a plain-text prefix here (D9): it
	// is applied at registration by kernel.Experimental and pinned centrally by
	// TestStabilityRegistry_pinsPublicContract (ADR-0010/ADR-0042).
}
