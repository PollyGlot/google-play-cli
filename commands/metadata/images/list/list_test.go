// Package imageslist_test exercises `gplay metadata images list` at the
// kernel level: a RunContext built by hand, a RoundTripper injected via the
// oauth2.HTTPClient context key, and Run invoked directly. The command is
// READ-ONLY: it enumerates the 9 image types across the app's locales
// inside one Edit that is opened and discarded, never committed. The
// transport FAILS on any :commit, upload, or mutating DELETE.
package imageslist_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	imageslist "github.com/PollyGlot/google-play-cli/commands/metadata/images/list"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// imagesRT configures the testkit.Fake that routes the read-only images-list
// sequence: edits.insert, listings.list (locale enumeration), one
// images.list per (locale, imageType) slot, edits.delete. Per-slot image
// payloads come from `slots` keyed by "<locale>/<type>"; an unlisted slot is
// empty. Any :commit, upload, or mutating call fails the test.
type imagesRT struct {
	editID       string
	listingsResp string
	slots        map[string]string // "<locale>/<type>" -> images.list body
}

func newFake(t *testing.T, r imagesRT) *testkit.Fake {
	t.Helper()
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return 200, fmt.Sprintf(`{"id":%q}`, r.editID), true
		case c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/listings"):
			return 200, r.listingsResp, true
		case isSlotGET(c):
			// Path tail is .../listings/<locale>/<imageType>
			parts := strings.Split(c.Path, "/listings/")
			body := r.slots[parts[len(parts)-1]]
			if body == "" {
				body = `{"images":[]}`
			}
			return 200, body, true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/") && !strings.Contains(c.Path, "/listings"):
			return 204, "", true
		}
		t.Errorf("unexpected request (read-only list must not write/commit): %s %s", c.Method, c.Path)
		return 0, "", false
	})
}

// isSlotGET reports an images.list read (GET .../listings/<locale>/<type>).
func isSlotGET(c testkit.Call) bool {
	return c.Method == http.MethodGet && strings.Contains(c.Path, "/listings/")
}

func slotGETs(f *testkit.Fake) int {
	n := 0
	for _, c := range f.Calls() {
		if isSlotGET(c) {
			n++
		}
	}
	return n
}

// calls lists the recorded API requests as "METHOD path" lines.
func calls(f *testkit.Fake) []string {
	var out []string
	for _, c := range f.Calls() {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
}

func newRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(testkit.ServiceAccountJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	var stdout bytes.Buffer
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &stdout}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc
}

// twoLocales is the canonical fixture: en-US and fr-FR have Listings (so they
// are the app's locales); en-US has an icon (1) and 2 phone screenshots,
// fr-FR has an icon (1).
func twoLocales() (string, map[string]string) {
	listingsResp := `{"listings":[{"language":"en-US","title":"My App"},{"language":"fr-FR","title":"Mon App"}]}`
	slots := map[string]string{
		"en-US/icon":             `{"images":[{"id":"i1","url":"u1","sha1":"s1","sha256":"ENUS_ICON"}]}`,
		"en-US/phoneScreenshots": `{"images":[{"id":"p1","url":"u2","sha1":"s2","sha256":"PHONE1"},{"id":"p2","url":"u3","sha1":"s3","sha256":"PHONE2"}]}`,
		"fr-FR/icon":             `{"images":[{"id":"i2","url":"u4","sha1":"s4","sha256":"FRFR_ICON"}]}`,
	}
	return listingsResp, slots
}

// TestRun_enumeratesNineTypesAcrossLocales is the tracer bullet: /token →
// edits.insert → listings.list (locales) → 9 images.list per locale → Edit
// discarded. The JSON output carries each non-empty slot with its verbatim
// per-image sha256 (ADR-0003 pass-through), and never commits.
func TestRun_enumeratesNineTypesAcrossLocales(t *testing.T) {
	listingsResp, slots := twoLocales()
	rt := newFake(t, imagesRT{editID: "edit-img", listingsResp: listingsResp, slots: slots})
	rc := newRC(t, rt)

	r, err := imageslist.Run(rc, imageslist.Input{Package: "com.example.app"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rt.TokenExchanges() == 0 {
		t.Errorf("no /token exchange; calls=%v", calls(rt))
	}
	// 9 types × 2 locales = 18 slot reads.
	if slotGETs(rt) != 18 {
		t.Errorf("slot GETs = %d, want 18 (9 types × 2 locales)", slotGETs(rt))
	}
	// Last call must be the Edit discard, never a :commit.
	all := calls(rt)
	last := all[len(all)-1]
	if !strings.HasPrefix(last, "DELETE ") || strings.Contains(last, ":commit") {
		t.Errorf("last call = %q, want a DELETE discard (read-only)", last)
	}

	var buf bytes.Buffer
	if err := r.Renderers().JSON(&buf); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"ENUS_ICON", "PHONE1", "PHONE2", "FRFR_ICON", "com.example.app"} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON output missing %q:\n%s", want, out)
		}
	}
	// Verify it is structured as a slots document with counts.
	var doc struct {
		Package string `json:"package"`
		Slots   []struct {
			Locale    string          `json:"locale"`
			ImageType string          `json:"imageType"`
			Count     int             `json:"count"`
			Images    json.RawMessage `json:"images"`
		} `json:"slots"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("JSON not parseable as slots doc: %v\n%s", err, out)
	}
	if doc.Package != "com.example.app" {
		t.Errorf("doc.package = %q", doc.Package)
	}
	// Only the 3 non-empty slots are reported.
	if len(doc.Slots) != 3 {
		t.Fatalf("got %d slots, want 3 non-empty: %+v", len(doc.Slots), doc.Slots)
	}
	byKey := map[string]int{}
	for _, s := range doc.Slots {
		byKey[s.Locale+"/"+s.ImageType] = s.Count
	}
	if byKey["en-US/phoneScreenshots"] != 2 {
		t.Errorf("en-US/phoneScreenshots count = %d, want 2", byKey["en-US/phoneScreenshots"])
	}
	if byKey["en-US/icon"] != 1 {
		t.Errorf("en-US/icon count = %d, want 1", byKey["en-US/icon"])
	}
}

// TestRun_tableShowsCountsAndSha256 asserts the human table view lists each
// non-empty slot with its count and per-image sha256.
func TestRun_tableShowsCountsAndSha256(t *testing.T) {
	listingsResp, slots := twoLocales()
	rt := newFake(t, imagesRT{editID: "edit-img", listingsResp: listingsResp, slots: slots})
	rc := newRC(t, rt)

	r, err := imageslist.Run(rc, imageslist.Input{Package: "com.example.app"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var buf bytes.Buffer
	if err := r.Renderers().Table(&buf); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"LOCALE", "IMAGE_TYPE", "COUNT", "SHA256", "phoneScreenshots", "PHONE1", "PHONE2", "FRFR_ICON"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
}

// TestRun_typeFilter_readsOnlyThatType asserts --type icon narrows the
// read to the icon slot across all locales: the transport must see NO
// requests for the other eight types. With the two-locale fixture that
// is exactly 2 slot GETs (en-US/icon, fr-FR/icon).
func TestRun_typeFilter_readsOnlyThatType(t *testing.T) {
	listingsResp, slots := twoLocales()
	rt := newFake(t, imagesRT{editID: "edit-img", listingsResp: listingsResp, slots: slots})
	rc := newRC(t, rt)

	if _, err := imageslist.Run(rc, imageslist.Input{Package: "com.example.app", Type: "icon"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if slotGETs(rt) != 2 {
		t.Errorf("slot GETs = %d, want 2 (icon × 2 locales)", slotGETs(rt))
	}
	// No slot read may address any type other than icon.
	for _, c := range calls(rt) {
		if strings.Contains(c, "/listings/") && strings.HasSuffix(c, "/listings") {
			continue // the listings.list enumeration call
		}
		if strings.Contains(c, "/listings/") && !strings.HasSuffix(c, "/icon") &&
			!strings.HasSuffix(c, "/edits") && !strings.HasPrefix(c, "DELETE ") {
			t.Errorf("saw a non-icon slot read with --type icon: %q", c)
		}
	}
}

// TestRun_invalidType_exit20_noHTTP asserts an unknown --type value is
// refused client-side (exit 20) before any HTTP round-trip.
func TestRun_invalidType_exit20_noHTTP(t *testing.T) {
	rt := newFake(t, imagesRT{editID: "edit-img"})
	rc := newRC(t, rt)

	_, err := imageslist.Run(rc, imageslist.Input{Package: "com.example.app", Type: "iconn"})
	var coder interface{ ExitCode() int }
	if !asCoder(err, &coder) || coder.ExitCode() != 20 {
		t.Fatalf("err = %v, want ExitCode 20", err)
	}
	if len(rt.Calls()) != 0 {
		t.Errorf("expected zero HTTP calls before validation error, saw: %v", calls(rt))
	}
}

// TestRun_typeFilter_jsonMatchesUnfiltered asserts that for the icon
// slot, --type icon --output json is byte-identical to the icon slot in
// the unfiltered JSON: --type only narrows which slots appear, never
// reshapes them.
func TestRun_typeFilter_jsonMatchesUnfiltered(t *testing.T) {
	listingsResp, slots := twoLocales()

	iconSlot := func(in imageslist.Input) map[string]json.RawMessage {
		rt := newFake(t, imagesRT{editID: "edit-img", listingsResp: listingsResp, slots: slots})
		rc := newRC(t, rt)
		r, err := imageslist.Run(rc, in)
		if err != nil {
			t.Fatalf("Run(%+v): %v", in, err)
		}
		var buf bytes.Buffer
		if err := r.Renderers().JSON(&buf); err != nil {
			t.Fatalf("JSON render: %v", err)
		}
		var doc struct {
			Slots []struct {
				Locale    string          `json:"locale"`
				ImageType string          `json:"imageType"`
				Count     int             `json:"count"`
				Images    json.RawMessage `json:"images"`
			} `json:"slots"`
		}
		if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
			t.Fatalf("parse json: %v\n%s", err, buf.String())
		}
		out := map[string]json.RawMessage{}
		for _, s := range doc.Slots {
			if s.ImageType == "icon" {
				out[s.Locale] = s.Images
			}
		}
		return out
	}

	unfiltered := iconSlot(imageslist.Input{Package: "com.example.app"})
	filtered := iconSlot(imageslist.Input{Package: "com.example.app", Type: "icon"})

	if len(filtered) != len(unfiltered) || len(filtered) == 0 {
		t.Fatalf("icon slot counts differ: filtered=%d unfiltered=%d", len(filtered), len(unfiltered))
	}
	for loc, want := range unfiltered {
		got, ok := filtered[loc]
		if !ok {
			t.Errorf("filtered output missing icon slot for %q", loc)
			continue
		}
		if string(got) != string(want) {
			t.Errorf("icon slot %q differs:\n filtered=%s\n unfiltered=%s", loc, got, want)
		}
	}
}

// TestRun_noPackage_isUsageError asserts the missing-package guard (exit 2).
func TestRun_noPackage_isUsageError(t *testing.T) {
	rc := newRC(t, newFake(t, imagesRT{editID: "x"}))
	_, err := imageslist.Run(rc, imageslist.Input{})
	if err == nil {
		t.Fatal("want usage error on no package")
	}
	var coder interface{ ExitCode() int }
	if !asCoder(err, &coder) || coder.ExitCode() != 2 {
		t.Errorf("err = %v, want ExitCode 2", err)
	}
}

func asCoder(err error, target *interface{ ExitCode() int }) bool {
	for err != nil {
		if c, ok := err.(interface{ ExitCode() int }); ok {
			*target = c
			return true
		}
		type unwrap interface{ Unwrap() error }
		u, ok := err.(unwrap)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
