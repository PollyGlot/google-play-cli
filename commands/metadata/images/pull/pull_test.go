package imagespull_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	imagespull "github.com/PollyGlot/google-play-cli/commands/metadata/images/pull"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func png(tag string) []byte { return append([]byte("\x89PNG\r\n\x1a\n"), tag...) }
func jpg(tag string) []byte { return append([]byte("\xff\xd8\xff\xe0"), tag...) }

func pullFake(t *testing.T, editID, listingsResp string, slots map[string]string, blobs map[string][]byte, first ...testkit.Responder) *testkit.Fake {
	t.Helper()
	return testkit.NewFake(append(first, func(c testkit.Call) (int, string, bool) {
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
	})...)
}

func redirect(from, to string) testkit.Responder {
	return testkit.ReplyHeader(func(c testkit.Call) (int, string, bool) {
		return http.StatusFound, "", c.Method == http.MethodGet && c.URL == from
	}, "Location", func(testkit.Call, int) string { return to })
}

func committed(f *testkit.Fake) bool {
	for _, c := range f.Calls() {
		if strings.HasSuffix(c.Path, ":commit") {
			return true
		}
	}
	return false
}

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

func fixture() (string, map[string]string, map[string][]byte) {
	listingsResp := `{"listings":[{"language":"en-US","title":"My App"},{"language":"fr-FR","title":"Mon App"}]}`
	blobs := map[string][]byte{
		"https://play-lh.test/en/icon": png("icon"),
		"https://play-lh.test/en/p1":   png("p1"),
		"https://play-lh.test/en/p2":   jpg("p2"),
		"https://play-lh.test/fr/feat": png("feat"),
	}
	slots := map[string]string{
		"en-US/icon":             listed(blobs, "https://play-lh.test/en/icon"),
		"en-US/phoneScreenshots": listed(blobs, "https://play-lh.test/en/p1", "https://play-lh.test/en/p2"),
		"fr-FR/featureGraphic":   listed(blobs, "https://play-lh.test/fr/feat"),
	}
	return listingsResp, slots, blobs
}

func listed(blobs map[string][]byte, urls ...string) string {
	imgs := make([]map[string]string, len(urls))
	for i, u := range urls {
		imgs[i] = map[string]string{"id": fmt.Sprintf("img%d", i+1), "url": u, "sha256": sum(blobs[u])}
	}
	b, _ := json.Marshal(map[string]any{"images": imgs})
	return string(b)
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

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
	if _, err := os.Stat(filepath.Join(dir, "en-US", "images", "tvBanner.png")); !os.IsNotExist(err) {
		t.Errorf("empty slot tvBanner must not be written")
	}
}

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

func TestRun_downloadsOriginalSize(t *testing.T) {
	listingsResp := `{"listings":[{"language":"en-US","title":"My App"}]}`
	const featURL, p1URL = "https://lh3.googleusercontent.com/feat", "https://play-lh.googleusercontent.com/p1=w512-h512"
	blobs := map[string][]byte{
		"https://lh3.googleusercontent.com/feat=s0":   png("feat original"),
		"https://play-lh.googleusercontent.com/p1=s0": png("p1 original"),
	}
	slots := map[string]string{
		"en-US/featureGraphic":   fmt.Sprintf(`{"images":[{"id":"f1","url":%q,"sha256":%q}]}`, featURL, sum(png("feat original"))),
		"en-US/phoneScreenshots": fmt.Sprintf(`{"images":[{"id":"p1","url":%q,"sha256":%q}]}`, p1URL, sum(png("p1 original"))),
	}
	rt := pullFake(t, "edit-pull", listingsResp, slots, blobs)
	dir := t.TempDir()
	if _, err := imagespull.Run(newRC(t, rt), imagespull.Input{Package: "com.example.app", Dir: dir}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := downloads(rt, blobs); n != 2 {
		t.Errorf("original-size downloads = %d, want 2", n)
	}
	got, err := os.ReadFile(filepath.Join(dir, "en-US", "images", "featureGraphic.png"))
	if err != nil || !bytes.Equal(got, png("feat original")) {
		t.Errorf("featureGraphic.png = %q, %v; want the original-size bytes", got, err)
	}
}

func TestRun_refusesBytesNotMatchingSha256(t *testing.T) {
	listingsResp := `{"listings":[{"language":"en-US","title":"My App"}]}`
	const u = "https://play-lh.test/en/icon"
	blobs := map[string][]byte{u: png("512px preview")}
	slots := map[string]string{
		"en-US/icon": fmt.Sprintf(`{"images":[{"id":"i1","url":%q,"sha256":%q}]}`, u, sum(png("original"))),
	}
	rt := pullFake(t, "edit-pull", listingsResp, slots, blobs)
	dir := t.TempDir()
	_, err := imagespull.Run(newRC(t, rt), imagespull.Input{Package: "com.example.app", Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("Run err = %v, want a sha256 mismatch", err)
	}
	if code := exit.For(err); code != 1 {
		t.Errorf("exit code = %d, want 1 (not retryable: a retry fetches the same bytes)", code)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("pull wrote %d entries after a mismatch, want none", len(entries))
	}
}

func TestRun_uppercaseSha256Matches(t *testing.T) {
	listingsResp := `{"listings":[{"language":"en-US","title":"My App"}]}`
	const u = "https://play-lh.test/en/icon"
	blobs := map[string][]byte{u: png("icon")}
	slots := map[string]string{
		"en-US/icon": fmt.Sprintf(`{"images":[{"id":"i1","url":%q,"sha256":%q}]}`, u, strings.ToUpper(sum(png("icon")))),
	}
	rt := pullFake(t, "edit-pull", listingsResp, slots, blobs)
	if _, err := imagespull.Run(newRC(t, rt), imagespull.Input{Package: "com.example.app", Dir: t.TempDir()}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func stagingDirs(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	return tmp
}

func TestRun_failedDownloadLeavesDirAndStagingClean(t *testing.T) {
	tmp := stagingDirs(t)
	listingsResp, slots, blobs := fixture()
	slots["fr-FR/featureGraphic"] = fmt.Sprintf(`{"images":[{"id":"f1","url":%q,"sha256":%q}]}`, "https://play-lh.test/fr/feat", sum(png("original")))
	rt := pullFake(t, "edit-pull", listingsResp, slots, blobs)
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "en-US", "images", "icon.png")
	if err := os.MkdirAll(filepath.Dir(sentinel), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sentinel, png("old icon"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := imagespull.Run(newRC(t, rt), imagespull.Input{Package: "com.example.app", Dir: dir}); err == nil {
		t.Fatal("Run succeeded with a failed download")
	}
	if n := downloads(rt, blobs); n != 4 {
		t.Errorf("downloads = %d, want 4 (the failure must come after the en-US images were staged)", n)
	}
	var files []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, _ error) error {
		if !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if len(files) != 1 || files[0] != sentinel {
		t.Errorf("files after a failed pull = %v, want only the untouched %s", files, sentinel)
	}
	if got, _ := os.ReadFile(sentinel); !bytes.Equal(got, png("old icon")) {
		t.Errorf("a failed pull overwrote %s", sentinel)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("a failed pull left %d staging entries in the temp dir, want none", len(left))
	}
}

func TestRun_successRemovesStaging(t *testing.T) {
	tmp := stagingDirs(t)
	listingsResp, slots, blobs := fixture()
	rt := pullFake(t, "edit-pull", listingsResp, slots, blobs)
	if _, err := imagespull.Run(newRC(t, rt), imagespull.Input{Package: "com.example.app", Dir: t.TempDir()}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("a pull left %d staging entries in the temp dir, want none", len(left))
	}
}

func TestRun_imageHostsGetNoCredential(t *testing.T) {
	listingsResp, slots, blobs := fixture()
	const moved = "https://cdn.elsewhere.test/icon"
	blobs[moved] = blobs["https://play-lh.test/en/icon"]
	rt := pullFake(t, "edit-pull", listingsResp, slots, blobs, redirect("https://play-lh.test/en/icon", moved))
	dir := t.TempDir()
	if _, err := imagespull.Run(newRC(t, rt), imagespull.Input{Package: "com.example.app", Dir: dir}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "en-US", "images", "icon.png")); !bytes.Equal(got, png("icon")) {
		t.Errorf("icon.png = %q, want the bytes served after the redirect", got)
	}
	var api, image int
	for _, c := range rt.Calls() {
		auth := c.Header.Get("Authorization")
		if c.Host == "androidpublisher.googleapis.com" {
			api++
			if auth == "" {
				t.Errorf("%s %s carried no Authorization: the Play API calls must stay authenticated", c.Method, c.URL)
			}
			continue
		}
		image++
		if auth != "" {
			t.Errorf("%s %s carried Authorization %q: the token leaked to an image host", c.Method, c.URL, auth)
		}
	}
	if api == 0 || image != 5 {
		t.Errorf("calls: %d to the API, %d to image hosts; want some and 5 (4 images + 1 redirect hop)", api, image)
	}
}

func TestRun_refusesNonHTTPSImageURL(t *testing.T) {
	const insecure = "http://play-lh.test/en/icon"
	for _, tc := range []struct {
		name  string
		url   string
		retry int
		first []testkit.Responder
	}{
		{name: "listed url", url: insecure},
		{name: "redirect", url: "https://play-lh.test/en/icon", first: []testkit.Responder{redirect("https://play-lh.test/en/icon", insecure)}},
		{name: "redirect under --retry", url: "https://play-lh.test/en/icon", retry: 1, first: []testkit.Responder{redirect("https://play-lh.test/en/icon", insecure)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listingsResp := `{"listings":[{"language":"en-US","title":"My App"}]}`
			blobs := map[string][]byte{insecure: png("icon")}
			slots := map[string]string{"en-US/icon": fmt.Sprintf(`{"images":[{"id":"i1","url":%q,"sha256":%q}]}`, tc.url, sum(png("icon")))}
			rt := pullFake(t, "edit-pull", listingsResp, slots, blobs, tc.first...)
			dir := t.TempDir()
			rc := newRC(t, rt)
			rc.Retry = tc.retry
			_, err := imagespull.Run(rc, imagespull.Input{Package: "com.example.app", Dir: dir})
			if err == nil || !strings.Contains(err.Error(), "refusing non-https URL "+insecure) {
				t.Fatalf("Run err = %v, want the non-https refusal", err)
			}
			if code := exit.For(err); code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}
			if n := downloads(rt, blobs); n != 0 {
				t.Errorf("%d requests reached %s, want none", n, insecure)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Errorf("pull wrote %d entries, want none", len(entries))
			}
		})
	}
}
