// Package apiregistry_test, resolver half: it proves the verb and URL the
// resolver hands a call site are Google's own, by confronting every registered
// method with the `flatPath` of the committed Discovery snapshots (the raw
// artefact, NOT the derived index the resolver reads: deriving both sides from
// the same file would prove nothing). Everything here is offline.
package apiregistry_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
)

// snapshotMethod is the slice of a Discovery method the resolver's contract
// depends on: the verb, the absolute path template, and the media-upload path.
type snapshotMethod struct {
	verb, flatPath, rootURL, uploadPath string
}

// snapshotMethods walks every committed Discovery snapshot and returns its
// methods keyed by native id.
func snapshotMethods(t *testing.T) map[string]snapshotMethod {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoDocs, "discovery", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("glob discovery snapshots: %v (%d files)", err, len(files))
	}
	out := map[string]snapshotMethod{}
	for _, f := range files {
		raw, err := os.ReadFile(f) //nolint:gosec // committed artefact, fixed path
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		var doc struct {
			RootURL   string                     `json:"rootUrl"`
			Resources map[string]json.RawMessage `json:"resources"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		collectMethods(t, doc.RootURL, doc.Resources, out)
	}
	return out
}

// collectMethods recurses through Discovery's resource tree. It is written
// against json.RawMessage rather than a typed mirror so this test stays
// independent of internal/schemaindex's own parsing.
func collectMethods(t *testing.T, rootURL string, resources map[string]json.RawMessage, out map[string]snapshotMethod) {
	t.Helper()
	for _, raw := range resources {
		var r struct {
			Methods map[string]struct {
				ID          string `json:"id"`
				HTTPMethod  string `json:"httpMethod"`
				FlatPath    string `json:"flatPath"`
				MediaUpload *struct {
					Protocols struct {
						Simple struct {
							Path string `json:"path"`
						} `json:"simple"`
					} `json:"protocols"`
				} `json:"mediaUpload"`
			} `json:"methods"`
			Resources map[string]json.RawMessage `json:"resources"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatalf("parse resource: %v", err)
		}
		for _, m := range r.Methods {
			sm := snapshotMethod{verb: m.HTTPMethod, flatPath: m.FlatPath, rootURL: rootURL}
			if m.MediaUpload != nil {
				sm.uploadPath = m.MediaUpload.Protocols.Simple.Path
			}
			out[m.ID] = sm
		}
		collectMethods(t, rootURL, r.Resources, out)
	}
}

// TestResolveMatchesDiscoveryFlatPath is the resolver's anchor: for EVERY
// registered method, the resolved verb and URL template must equal rootUrl +
// flatPath as Google publishes it, and the upload template must equal rootUrl +
// the media path. A drift here means a migrated call site would hit a different
// endpoint than before.
func TestResolveMatchesDiscoveryFlatPath(t *testing.T) {
	snap := snapshotMethods(t)
	for _, e := range apiregistry.Entries() {
		m, err := apiregistry.Resolve(e.MethodID)
		if err != nil {
			t.Errorf("Resolve(%q): %v", e.MethodID, err)
			continue
		}
		want, ok := snap[e.MethodID]
		if !ok {
			t.Errorf("method %q is registered but absent from the Discovery snapshots", e.MethodID)
			continue
		}
		if m.Verb != want.verb {
			t.Errorf("%s: verb = %q, want %q", e.MethodID, m.Verb, want.verb)
		}
		if got, wantURL := m.URLTemplate, want.rootURL+want.flatPath; got != wantURL {
			t.Errorf("%s: URL template = %q, want %q", e.MethodID, got, wantURL)
		}
		wantUpload := ""
		if want.uploadPath != "" {
			wantUpload = want.rootURL + strings.TrimPrefix(want.uploadPath, "/")
		}
		if m.UploadTemplate != wantUpload {
			t.Errorf("%s: upload template = %q, want %q", e.MethodID, m.UploadTemplate, wantUpload)
		}
	}
}

// TestResolveRejectsUnknownID guards the membership check both ways: a
// well-formed but unregistered id, and a nonsense one.
func TestResolveRejectsUnknownID(t *testing.T) {
	for _, id := range []string{
		"androidpublisher.edits.apks.list", // real method, deliberately not called by gplay
		"not.a.method",
		"",
	} {
		if _, err := apiregistry.Resolve(id); err == nil {
			t.Errorf("Resolve(%q) succeeded, want an error", id)
		}
	}
}

// TestURLFillsAndEscapes checks the two behaviours a migrated call site relies
// on: placeholders filled in order, and values path-escaped so a hostile
// package name cannot forge a path segment.
func TestURLFillsAndEscapes(t *testing.T) {
	m, err := apiregistry.Resolve("androidpublisher.edits.countryavailability.get")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got, err := m.URL(map[string]string{"packageName": "com.example.app", "editId": "edit-1", "track": "internal qa/x"})
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	want := "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/com.example.app/edits/edit-1/countryAvailability/internal%20qa%2Fx"
	if got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
}

// TestURLRejectsMissingParam is the error case the migration depends on: a
// forgotten or empty parameter must fail loudly, naming the parameter, instead
// of producing a URL with a hole in it.
func TestURLRejectsMissingParam(t *testing.T) {
	m, err := apiregistry.Resolve("androidpublisher.edits.countryavailability.get")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for name, params := range map[string]map[string]string{
		"absent": {"packageName": "com.example.app", "editId": "edit-1"},
		"empty":  {"packageName": "com.example.app", "editId": "edit-1", "track": ""},
	} {
		_, err := m.URL(params)
		if err == nil {
			t.Fatalf("%s: URL succeeded, want an error", name)
		}
		if !strings.Contains(err.Error(), `"track"`) {
			t.Errorf("%s: error = %v, want it to name the track parameter", name, err)
		}
	}
	if _, err := m.URL(map[string]string{
		"packageName": "com.example.app", "editId": "edit-1", "track": "production", "trck": "typo",
	}); err == nil {
		t.Error("URL accepted an unknown parameter, want an error naming it")
	}
}

// TestUploadURL covers the media endpoint: present on an upload method (a
// distinct /upload path, not a suffix), absent and refused elsewhere.
func TestUploadURL(t *testing.T) {
	m, err := apiregistry.Resolve("androidpublisher.edits.bundles.upload")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got, err := m.UploadURL(map[string]string{"packageName": "com.example.app", "editId": "edit-1"})
	if err != nil {
		t.Fatalf("UploadURL: %v", err)
	}
	want := "https://androidpublisher.googleapis.com/upload/androidpublisher/v3/applications/com.example.app/edits/edit-1/bundles"
	if got != want {
		t.Errorf("UploadURL = %q, want %q", got, want)
	}

	noMedia, err := apiregistry.Resolve("androidpublisher.edits.tracks.get")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, err := noMedia.UploadURL(map[string]string{"packageName": "p", "editId": "e", "track": "t"}); err == nil {
		t.Error("UploadURL on a method with no media endpoint succeeded, want an error")
	}
}

// TestResolveCoversEveryService keeps the multi-service reconstruction honest:
// the base path differs per document (androidpublisher/v3/ vs
// games/v1configuration/ vs v1beta1/), so one registered method per service is
// resolved and its URL checked against the constant the hand-rolled call sites
// use today.
func TestResolveCoversEveryService(t *testing.T) {
	cases := map[string]string{
		"androidpublisher.edits.tracks.list":                "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/",
		"gamesConfiguration.achievementConfigurations.list": "https://gamesconfiguration.googleapis.com/games/v1configuration/applications/",
		"playdeveloperreporting.vitals.crashrate.query":     "https://playdeveloperreporting.googleapis.com/v1beta1/",
		"playcustomapp.accounts.customApps.create":          "https://playcustomapp.googleapis.com/playcustomapp/v1/accounts/",
	}
	for id, prefix := range cases {
		m, err := apiregistry.Resolve(id)
		if err != nil {
			t.Errorf("Resolve(%q): %v", id, err)
			continue
		}
		if !strings.HasPrefix(m.URLTemplate, prefix) {
			t.Errorf("%s: URL template = %q, want it to start with %q", id, m.URLTemplate, prefix)
		}
	}
}
