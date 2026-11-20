package apiregistry

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func sampleParams(tmpl string) map[string]string {
	out := map[string]string{}
	for i, p := range placeholder.FindAllString(tmpl, -1) {
		out[strings.Trim(p, "{}")] = "v" + strconv.Itoa(i)
	}
	return out
}

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
