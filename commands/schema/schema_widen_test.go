package schema_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	schemacmd "github.com/PollyGlot/google-play-cli/commands/schema"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

func renderMarkdown(t *testing.T, r output.Renderable) string {
	t.Helper()
	var b bytes.Buffer
	if err := r.Renderers().Markdown(&b); err != nil {
		t.Fatalf("Markdown render: %v", err)
	}
	return b.String()
}

// runCaptureStderr runs the command with a captured stderr so the zero-match
// note can be asserted; it fails the test if Run errors (zero match must be
// exit 0, not an error).
func runCaptureStderr(t *testing.T, in schemacmd.Input) (output.Renderable, string) {
	t.Helper()
	var errb bytes.Buffer
	rc := kernel.NewForTest(context.Background(), kernel.Boot{Stderr: &errb}, kernel.Inputs{Format: output.FormatJSON})
	rc.Resolved = &config.Resolved{}
	r, err := schemacmd.Run(rc, in)
	if err != nil {
		t.Fatalf("Run returned error (zero match must be exit 0): %v", err)
	}
	return r, errb.String()
}

// TestList_compactCatalog asserts --list prints the compact catalog (id ·
// httpMethod · path) of the full method surface, and that the JSON view carries
// the methods with an empty schemas section.
func TestList_compactCatalog(t *testing.T) {
	r := run(t, schemacmd.Input{List: true})

	table := renderTable(t, r)
	for _, want := range []string{
		"androidpublisher.edits.tracks.update",
		"androidpublisher.edits.commit",
		"androidpublisher.reviews.list",
	} {
		if !strings.Contains(table, want) {
			t.Errorf("catalog missing %q", want)
		}
	}

	v := parseJSON(t, renderJSON(t, r))
	if len(v.Methods) < 120 {
		t.Errorf("--list JSON has %d methods, want the full surface", len(v.Methods))
	}
	if len(v.Schemas) != 0 {
		t.Errorf("--list is a method catalog; want 0 schemas, got %d", len(v.Schemas))
	}
}

// TestMethodFilter_json asserts --method narrows to a single verb.
func TestMethodFilter_json(t *testing.T) {
	r := run(t, schemacmd.Input{Method: "DELETE"})
	v := parseJSON(t, renderJSON(t, r))
	if len(v.Methods) == 0 {
		t.Fatal("--method DELETE returned nothing")
	}
	for id, m := range v.Methods {
		if m.HTTPMethod != "DELETE" {
			t.Errorf("method %q verb %q, want DELETE", id, m.HTTPMethod)
		}
	}
}

// TestInlineExpansion_oneHop asserts a matched method inline-expands its
// request/response schema exactly one hop: the schema's direct properties are
// present, but a nested $ref is shown by name only (not expanded).
func TestInlineExpansion_oneHop(t *testing.T) {
	r := run(t, schemacmd.Input{Query: "edits.tracks.update"})
	v := parseJSON(t, renderJSON(t, r))

	track, ok := v.Schemas["Track"]
	if !ok {
		t.Fatalf("inline expansion missing the request/response schema Track; schemas: %v", schemaKeys(v))
	}
	if _, ok := track.Properties["releases"]; !ok {
		t.Error("Track expansion missing the releases property")
	}
	if track.Properties["releases"].Ref != "TrackRelease" {
		t.Errorf("releases should point to TrackRelease by name, got %+v", track.Properties["releases"])
	}
	// One hop only: TrackRelease (nested under Track.releases) is referenced by
	// name, NOT expanded into the schemas section.
	if _, expanded := v.Schemas["TrackRelease"]; expanded {
		t.Error("TrackRelease should be shown by name only, not expanded (two hops)")
	}

	// The table view shows the expanded property and the nested type name.
	table := renderTable(t, r)
	if !strings.Contains(table, "releases") || !strings.Contains(table, "TrackRelease") {
		t.Errorf("table inline expansion missing releases / TrackRelease\n%s", table)
	}
}

// TestSchemaQuery_directTypeWithEnum asserts a schema-name query resolves a type
// directly and surfaces its enum values and verbatim descriptions.
func TestSchemaQuery_directTypeWithEnum(t *testing.T) {
	r := run(t, schemacmd.Input{Query: "TrackRelease"})

	v := parseJSON(t, renderJSON(t, r))
	tr, ok := v.Schemas["TrackRelease"]
	if !ok {
		t.Fatalf("schema-name query did not resolve TrackRelease; schemas: %v", schemaKeys(v))
	}
	if _, ok := tr.Properties["status"]; !ok {
		t.Fatal("TrackRelease missing status property")
	}

	table := renderTable(t, r)
	for _, want := range []string{"TrackRelease", "status", "inProgress", "halted"} {
		if !strings.Contains(table, want) {
			t.Errorf("schema table missing %q\n%s", want, table)
		}
	}
}

// TestZeroMatch_noteOnStderrExit0 asserts a no-match query exits 0 (no error)
// and writes a note to stderr — so "found nothing" is not mistaken for failure.
func TestZeroMatch_noteOnStderrExit0(t *testing.T) {
	r, stderr := runCaptureStderr(t, schemacmd.Input{Query: "zzz-not-a-real-thing"})
	if stderr == "" {
		t.Error("zero match must write a note to stderr")
	}
	if !strings.Contains(stderr, "zzz-not-a-real-thing") {
		t.Errorf("note should echo the query, got %q", stderr)
	}
	// stdout (JSON) is still valid and empty.
	v := parseJSON(t, renderJSON(t, r))
	if len(v.Methods) != 0 || len(v.Schemas) != 0 {
		t.Errorf("zero match should render empty, got %d methods / %d schemas", len(v.Methods), len(v.Schemas))
	}
}

// TestMarkdown_renders asserts the third Format produces a valid GFM table.
func TestMarkdown_renders(t *testing.T) {
	r := run(t, schemacmd.Input{Query: "edits.tracks.update"})
	md := renderMarkdown(t, r)
	if !strings.Contains(md, "| --- |") && !strings.Contains(md, "---") {
		t.Errorf("markdown missing a GFM separator row\n%s", md)
	}
	for _, want := range []string{"androidpublisher.edits.tracks.update", "Track"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\n%s", want, md)
		}
	}
}

func schemaKeys(v jsonView) []string {
	out := make([]string, 0, len(v.Schemas))
	for k := range v.Schemas {
		out = append(out, k)
	}
	return out
}
