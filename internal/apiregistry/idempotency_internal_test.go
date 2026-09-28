package apiregistry

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// sampleParams fills every placeholder of tmpl with a distinct value, the way
// a call site would.
func sampleParams(tmpl string) map[string]string {
	out := map[string]string{}
	for i, p := range placeholder.FindAllString(tmpl, -1) {
		out[strings.Trim(p, "{}")] = "v" + strconv.Itoa(i)
	}
	return out
}

// TestMatchRequestRoundTripsEveryEntry proves the classifier the transport
// relies on is exact: for every registered method, a URL built by the
// resolver (data plane and media endpoint alike, with a query string on top)
// maps back to that very method, never to a same-verb neighbour.
func TestMatchRequestRoundTripsEveryEntry(t *testing.T) {
	for _, e := range entries {
		m, err := Resolve(e.MethodID)
		if err != nil {
			t.Fatalf("Resolve(%s): %v", e.MethodID, err)
		}
		build := map[string]func(map[string]string) (string, error){"URL": m.URL}
		if m.UploadTemplate != "" {
			build["UploadURL"] = m.UploadURL
		}
		for name, fn := range build {
			tmpl := m.URLTemplate
			if name == "UploadURL" {
				tmpl = m.UploadTemplate
			}
			u, err := fn(sampleParams(tmpl))
			if err != nil {
				t.Fatalf("%s.%s: %v", e.MethodID, name, err)
			}
			req, err := http.NewRequestWithContext(t.Context(), m.Verb, u+"?uploadType=media&pageToken=x", nil)
			if err != nil {
				t.Fatalf("NewRequest(%s): %v", u, err)
			}
			got, ok := matchRequest(req)
			if !ok {
				t.Errorf("%s %s (%s): matched no registered method", m.Verb, u, e.MethodID)
				continue
			}
			if got.ID != e.MethodID {
				t.Errorf("%s %s: matched %s, want %s", m.Verb, u, got.ID, e.MethodID)
			}
		}
	}
}

// TestReplaySafePOSTsAreRegisteredPOSTs keeps the allowlist honest: an entry
// that is not a registered POST is either dead (the method left the registry)
// or pointless (a GET is idempotent anyway), and both hide a stale promise.
func TestReplaySafePOSTsAreRegisteredPOSTs(t *testing.T) {
	for id := range replaySafePOSTs {
		m, err := Resolve(id)
		if err != nil {
			t.Errorf("replay-safe entry %s: %v", id, err)
			continue
		}
		if m.Verb != http.MethodPost {
			t.Errorf("replay-safe entry %s is %s, not POST: drop it, the verb already decides", id, m.Verb)
		}
	}
}
