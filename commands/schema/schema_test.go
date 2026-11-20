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

func TestQueryByID_table(t *testing.T) {
	r := run(t, schemacmd.Input{Query: "edits.tracks.update"})
	table := renderTable(t, r)
	for _, want := range []string{
		"androidpublisher.edits.tracks.update",
		"PUT",
		"applications/{packageName}/edits/{editId}/tracks/{track}",
		"editId", "packageName", "track",
		"Track",
	} {
		if !strings.Contains(table, want) {
			t.Errorf("table missing %q\n---\n%s", want, table)
		}
	}
}

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
	if _, ok := v.Schemas["Track"]; !ok {
		t.Errorf("expected one-hop expansion to carry Track, got %v", v.Schemas)
	}
}

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

func TestNewCommand_surface(t *testing.T) {
	cmd := schemacmd.NewCommand(kernel.Boot{})
	if cmd.Use == "" || !strings.HasPrefix(cmd.Use, "schema") {
		t.Errorf("cmd.Use = %q, want it to start with schema", cmd.Use)
	}
	if cmd.Flags().Lookup("output") == nil {
		t.Error("missing --output flag")
	}
}
