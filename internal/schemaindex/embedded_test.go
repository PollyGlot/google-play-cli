package schemaindex_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

// TestEmbeddedCarriesBothServices asserts the embedded, multi-service index
// loaded via Embedded() carries methods from BOTH snapshotted services:
// androidpublisher (the publishing surface) and playdeveloperreporting (the
// read-only vitals service, #49). This is the single accessor every in-binary
// consumer (commands/schema, vitals validation) reads, so it must span every
// declared service, not just the primary one.
func TestEmbeddedCarriesBothServices(t *testing.T) {
	idx, err := schemaindex.Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	for _, id := range []string{
		"androidpublisher.reviews.list",
		"playdeveloperreporting.vitals.crashrate.query",
	} {
		if _, ok := idx.Methods[id]; !ok {
			t.Errorf("embedded index missing method %q", id)
		}
	}
}

// TestRenderAllMergesServices asserts RenderAll merges several normalized
// snapshots into one index whose Methods span every input, keyed by their
// service-prefixed RPC id (no collision). It is the deterministic generator the
// offline integrity gate re-runs, so a re-render is byte-identical.
func TestRenderAllMergesServices(t *testing.T) {
	a := []byte(`{"name":"svca","version":"v1","revision":"1","resources":{"r":{"methods":{"get":{"id":"svca.r.get","httpMethod":"GET","flatPath":"v1/a"}}}},"schemas":{"A":{"properties":{"x":{"type":"string"}}}}}`)
	b := []byte(`{"name":"svcb","version":"v2","revision":"2","resources":{"r":{"methods":{"list":{"id":"svcb.r.list","httpMethod":"GET","flatPath":"v2/b"}}}},"schemas":{"B":{"properties":{"y":{"type":"string"}}}}}`)

	out, err := schemaindex.RenderAll([][]byte{a, b})
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	idx, err := schemaindex.Load(out)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := idx.Methods["svca.r.get"]; !ok {
		t.Error("merged index missing svca.r.get")
	}
	if _, ok := idx.Methods["svcb.r.list"]; !ok {
		t.Error("merged index missing svcb.r.list")
	}
	if _, ok := idx.Schemas["A"]; !ok {
		t.Error("merged index missing schema A")
	}
	if _, ok := idx.Schemas["B"]; !ok {
		t.Error("merged index missing schema B")
	}

	// Determinism: a second render of the same inputs is byte-equal.
	out2, err := schemaindex.RenderAll([][]byte{a, b})
	if err != nil {
		t.Fatalf("RenderAll (2nd): %v", err)
	}
	if string(out) != string(out2) {
		t.Error("RenderAll is not deterministic across runs")
	}
}
