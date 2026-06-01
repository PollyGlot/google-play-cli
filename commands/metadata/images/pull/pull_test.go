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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	imagespull "github.com/PollyGlot/google-play-cli/commands/metadata/images/pull"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

func png(tag string) []byte { return append([]byte("\x89PNG\r\n\x1a\n"), tag...) }
func jpg(tag string) []byte { return append([]byte("\xff\xd8\xff\xe0"), tag...) }

// pullRT routes the read-only pull sequence and serves image bytes keyed by
// url. slots maps "<locale>/<type>" -> images.list body; blobs maps an image
// url -> its bytes.
type pullRT struct {
	t            *testing.T
	editID       string
	listingsResp string
	slots        map[string]string
	blobs        map[string][]byte

	mu          sync.Mutex
	committed   bool
	downloads   int
	wroteToEdit bool
}

func (r *pullRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		return jsonResp(200, `{"access_token":"abc","token_type":"Bearer","expires_in":3600}`), nil
	}
	// Image blob downloads: any GET whose full URL is a known blob url.
	if b, ok := r.blobs[req.URL.String()]; ok && req.Method == http.MethodGet {
		r.downloads++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/octet-stream"}}, Body: io.NopCloser(bytes.NewReader(b))}, nil
	}
	switch {
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits"):
		return jsonResp(200, fmt.Sprintf(`{"id":%q}`, r.editID)), nil
	case strings.HasSuffix(req.URL.Path, ":commit"):
		r.committed = true
		r.t.Fatalf("pull must never commit")
		return nil, nil
	case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/listings"):
		return jsonResp(200, r.listingsResp), nil
	case req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/listings/"):
		parts := strings.Split(req.URL.Path, "/listings/")
		body := r.slots[parts[len(parts)-1]]
		if body == "" {
			body = `{"images":[]}`
		}
		return jsonResp(200, body), nil
	case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/") && !strings.Contains(req.URL.Path, "/listings"):
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	case req.Method != http.MethodGet:
		r.wroteToEdit = true
		r.t.Fatalf("pull must not mutate: %s %s", req.Method, req.URL)
	}
	r.t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
	return nil, nil
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func signedSAJSON(t *testing.T) []byte {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, _ := json.Marshal(map[string]any{
		"type": "service_account", "project_id": "test-proj",
		"private_key": string(pemBytes), "client_email": "ci@test.iam.gserviceaccount.com",
		"token_uri": "https://oauth2.googleapis.com/token",
	})
	return raw
}

func newRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
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
	rt := &pullRT{t: t, editID: "edit-pull", listingsResp: listingsResp, slots: slots, blobs: blobs}
	rc := newRC(t, rt)
	dir := t.TempDir()

	if _, err := imagespull.Run(rc, imagespull.Input{Package: "com.example.app", Dir: dir}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rt.committed {
		t.Fatal("pull committed the Edit")
	}
	if rt.downloads != 4 {
		t.Errorf("downloads = %d, want 4", rt.downloads)
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
// the codec to exactly the downloaded byte sequences — the structural basis of
// the pull → apply no-op (ADR-0013).
func TestRun_pullIsCodecRoundTrippable(t *testing.T) {
	listingsResp, slots, blobs := fixture()
	rt := &pullRT{t: t, editID: "edit-pull", listingsResp: listingsResp, slots: slots, blobs: blobs}
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
