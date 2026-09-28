// Package imagespull_test exercises `gplay metadata images pull` at the
// kernel level. The command is READ-ONLY against Play (open → list → download
// → discard, never commit) but WRITES the downloaded images to disk under
// <dir>/<locale>/images/. The transport routes the read sequence and serves
// image bytes from the per-image url; it fails on any :commit or mutating
// call.
package imagespull_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	imagespull "github.com/PollyGlot/google-play-cli/commands/metadata/images/pull"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func png(tag string) []byte { return append([]byte("\x89PNG\r\n\x1a\n"), tag...) }
func jpg(tag string) []byte { return append([]byte("\xff\xd8\xff\xe0"), tag...) }

// pullFake routes the read-only pull sequence and serves image bytes keyed by
// url. slots maps "<locale>/<type>" -> images.list body; blobs maps an image
// url -> its bytes. A commit, any other mutation or an unknown request fails
// t and is left unclaimed, so the round trip fails too.
func pullFake(t *testing.T, editID, listingsResp string, slots map[string]string, blobs map[string][]byte) *testkit.Fake {
	t.Helper()
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		// Image blob downloads: any GET whose full URL is a known blob url.
		if b, ok := blobs[c.URL]; ok && c.Method == http.MethodGet {
			return http.StatusOK, string(b), true
		}
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return http.StatusOK, fmt.Sprintf(`{"id":%q}`, editID), true
		case strings.HasSuffix(c.Path, ":commit"):
			t.Errorf("pull must never commit")
			return 0, "", false
		case c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/listings"):
			return http.StatusOK, listingsResp, true
		case c.Method == http.MethodGet && strings.Contains(c.Path, "/listings/"):
			parts := strings.Split(c.Path, "/listings/")
			body := slots[parts[len(parts)-1]]
			if body == "" {
				body = `{"images":[]}`
			}
			return http.StatusOK, body, true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/") && !strings.Contains(c.Path, "/listings"):
			return http.StatusNoContent, "", true
		case c.Method != http.MethodGet:
			t.Errorf("pull must not mutate: %s %s", c.Method, c.URL)
			return 0, "", false
		}
		t.Errorf("unexpected request: %s %s", c.Method, c.URL)
		return 0, "", false
	})
}

// committed reports whether the pull sent an Edit commit.
func committed(f *testkit.Fake) bool {
	for _, c := range f.Calls() {
		if strings.HasSuffix(c.Path, ":commit") {
			return true
		}
	}
	return false
}

// downloads counts the blob GETs the pull made.
func downloads(f *testkit.Fake, blobs map[string][]byte) int {
	n := 0
	for _, c := range f.Calls() {
		if _, ok := blobs[c.URL]; ok && c.Method == http.MethodGet {
			n++
		}
	}
	return n
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

// fixture: en-US has icon (1) + 2 phone screenshots; fr-FR has a feature
// graphic; tvBanner everywhere is empty (writes nothing).
func fixture() (string, map[string]string, map[string][]byte) {
	listingsResp := `{"listings":[{"language":"en-US","title":"My App"},{"language":"fr-FR","title":"Mon App"}]}`
	slots := map[string]string{
		"en-US/icon":             `{"images":[{"id":"i1","url":"https://play-lh.test/en/icon","sha256":"x"}]}`,
		"en-US/phoneScreenshots": `{"images":[{"id":"p1","url":"https://play-lh.test/en/p1","sha256":"y"},{"id":"p2","url":"https://play-lh.test/en/p2","sha256":"z"}]}`,
		"fr-FR/featureGraphic":   `{"images":[{"id":"f1","url":"https://play-lh.test/fr/feat","sha256":"w"}]}`,
	}
	blobs := map[string][]byte{
		"https://play-lh.test/en/icon": png("icon"),
		"https://play-lh.test/en/p1":   png("p1"),
		"https://play-lh.test/en/p2":   jpg("p2"),
		"https://play-lh.test/fr/feat": png("feat"),
	}
	return listingsResp, slots, blobs
}

// TestRun_writesLiveSlotsToDisk is the tracer bullet: pull downloads each
// non-empty slot and writes singular `<type>.<ext>` + gallery `<type>/N.<ext>`
// files with the downloaded bytes; empty slots write nothing; the Edit is
// discarded, never committed.
func TestRun_writesLiveSlotsToDisk(t *testing.T) {
	listingsResp, slots, blobs := fixture()
	rt := pullFake(t, "edit-pull", listingsResp, slots, blobs)
	rc := newRC(t, rt)
	dir := t.TempDir()

	if _, err := imagespull.Run(rc, imagespull.Input{Package: "com.example.app", Dir: dir}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if committed(rt) {
		t.Fatal("pull committed the Edit")
	}
	if n := downloads(rt, blobs); n != 4 {
		t.Errorf("downloads = %d, want 4", n)
	}

	checks := map[string][]byte{
		filepath.Join(dir, "en-US", "images", "icon.png"):                  png("icon"),
		filepath.Join(dir, "en-US", "images", "phoneScreenshots", "1.png"): png("p1"),
		filepath.Join(dir, "en-US", "images", "phoneScreenshots", "2.jpg"): jpg("p2"),
		filepath.Join(dir, "fr-FR", "images", "featureGraphic.png"):        png("feat"),
	}
	for path, want := range checks {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("missing %s: %v", path, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s bytes = %q, want %q", path, got, want)
		}
	}
	// An empty slot (tvBanner everywhere) writes no file.
	if _, err := os.Stat(filepath.Join(dir, "en-US", "images", "tvBanner.png")); !os.IsNotExist(err) {
		t.Errorf("empty slot tvBanner must not be written")
	}
}

// TestRun_pullIsCodecRoundTrippable asserts the on-disk result reads back via
// the codec to exactly the downloaded byte sequences: the structural basis of
// the pull → apply no-op (ADR-0013).
func TestRun_pullIsCodecRoundTrippable(t *testing.T) {
	listingsResp, slots, blobs := fixture()
	rt := pullFake(t, "edit-pull", listingsResp, slots, blobs)
	rc := newRC(t, rt)
	dir := t.TempDir()
	if _, err := imagespull.Run(rc, imagespull.Input{Package: "com.example.app", Dir: dir}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	tr, err := imagetree.Read(dir)
	if err != nil {
		t.Fatalf("imagetree.Read: %v", err)
	}
	en := tr["en-US"]
	if len(en[images.PhoneScreenshots]) != 2 {
		t.Fatalf("en-US phone screenshots = %d, want 2", len(en[images.PhoneScreenshots]))
	}
	if !bytes.Equal(en[images.PhoneScreenshots][0], png("p1")) || !bytes.Equal(en[images.PhoneScreenshots][1], jpg("p2")) {
		t.Errorf("phone screenshot order/bytes wrong after pull")
	}
	if !bytes.Equal(en[images.Icon][0], png("icon")) {
		t.Errorf("icon bytes wrong after pull")
	}
}
