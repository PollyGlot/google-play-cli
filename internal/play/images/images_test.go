// Package images_test exercises the play-layer Store image operations
// (edits.images) against a fake transport. List/Upload/Delete/DeleteAll are
// the four verbs behind `gplay metadata images list / pull / apply`; each
// runs inside an already-open Edit, so the tests drive the bare operation,
// not the Edit lifecycle.
package images_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

// rt is a minimal RoundTripper that returns a canned response and records
// the request line (method + path), query, and body for assertion.
type rt struct {
	status int
	body   string

	gotPath   string
	gotMethod string
	gotQuery  string
	gotBody   []byte
	gotType   string
}

func (r *rt) RoundTrip(req *http.Request) (*http.Response, error) {
	r.gotPath = req.URL.Path
	r.gotMethod = req.Method
	r.gotQuery = req.URL.RawQuery
	r.gotType = req.Header.Get("Content-Type")
	if req.Body != nil {
		r.gotBody, _ = io.ReadAll(req.Body)
	}
	status := r.status
	if status == 0 {
		status = 200
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(r.body)),
	}, nil
}

// TestTypes_modelsTheNineApiTypes asserts the package models exactly the 9
// AppImageType values the API exposes, with the singular/gallery split
// explicit, and no Chromebook type (not in AppImageType).
func TestTypes_modelsTheNineApiTypes(t *testing.T) {
	got := images.Types()
	if len(got) != 9 {
		t.Fatalf("got %d types, want 9: %v", len(got), got)
	}
	wantGallery := map[images.Type]bool{
		images.Icon: false, images.FeatureGraphic: false, images.TvBanner: false, images.PromoGraphic: false,
		images.PhoneScreenshots: true, images.SevenInchScreenshots: true, images.TenInchScreenshots: true,
		images.TvScreenshots: true, images.WearScreenshots: true,
	}
	for ty, gallery := range wantGallery {
		if ty.Gallery() != gallery {
			t.Errorf("%s.Gallery() = %v, want %v", ty, ty.Gallery(), gallery)
		}
	}
	for _, ty := range got {
		if _, ok := wantGallery[ty]; !ok {
			t.Errorf("unexpected type %q in Types()", ty)
		}
	}
	if _, ok := images.ParseType("chromebookScreenshots"); ok {
		t.Errorf("chromebookScreenshots must not be an addressable type (not in AppImageType)")
	}
}

// TestList_parsesImages_andReturnsRawBody asserts List GETs
// edits.images.list on a (language, imageType) slot, parses the
// {"images":[...]} envelope into an Image slice in display (list) order, and
// hands back the raw body verbatim for the ADR-0003 JSON pass-through.
func TestList_parsesImages_andReturnsRawBody(t *testing.T) {
	raw := `{"images":[` +
		`{"id":"a1","url":"https://play/img/a1","sha1":"deadbeef","sha256":"aaaa"},` +
		`{"id":"b2","url":"https://play/img/b2","sha1":"feedface","sha256":"bbbb"}` +
		`],"kind":"androidpublisher#imagesListResponse"}`
	transport := &rt{body: raw}
	hc := &http.Client{Transport: transport}

	got, gotRaw, err := images.List(context.Background(), hc, "com.example.app", "edit-1", "en-US", images.PhoneScreenshots)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	wantPath := "/androidpublisher/v3/applications/com.example.app/edits/edit-1/listings/en-US/phoneScreenshots"
	if transport.gotMethod != http.MethodGet || transport.gotPath != wantPath {
		t.Errorf("request = %s %s, want GET %s", transport.gotMethod, transport.gotPath, wantPath)
	}
	if len(got) != 2 || got[0].ID != "a1" || got[1].Sha256 != "bbbb" {
		t.Fatalf("images = %+v, want [a1, b2 with sha256 bbbb]", got)
	}
	if strings.TrimSpace(string(gotRaw)) != strings.TrimSpace(raw) {
		t.Errorf("raw = %s, want verbatim envelope", gotRaw)
	}
}

// TestList_absentSlotIsEmpty asserts a slot with no images online (the API
// returns no `images` key, or an empty list) parses to a zero-length slice
// and no error: the "missing == empty" precondition on the read side.
func TestList_absentSlotIsEmpty(t *testing.T) {
	transport := &rt{body: `{"kind":"androidpublisher#imagesListResponse"}`}
	hc := &http.Client{Transport: transport}
	got, _, err := images.List(context.Background(), hc, "com.example.app", "e", "fr-FR", images.Icon)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d images for an absent slot, want 0", len(got))
	}
}

// TestUpload_usesMediaProtocol_andParsesImage asserts Upload POSTs the bytes
// to the upload sub-host with uploadType=media + octet-stream, and parses
// the {"image":{...}} envelope.
func TestUpload_usesMediaProtocol_andParsesImage(t *testing.T) {
	transport := &rt{body: `{"image":{"id":"new1","url":"https://play/img/new1","sha1":"s1","sha256":"s256"}}`}
	hc := &http.Client{Transport: transport}
	data := []byte("\x89PNG\r\n\x1a\nfake-bytes")

	img, err := images.Upload(context.Background(), hc, "com.example.app", "edit-1", "en-US", images.Icon, data)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if !strings.HasPrefix(transport.gotPath, "/upload/androidpublisher/v3/") {
		t.Errorf("upload path = %q, want /upload sub-host", transport.gotPath)
	}
	wantTail := "/applications/com.example.app/edits/edit-1/listings/en-US/icon"
	if !strings.HasSuffix(transport.gotPath, wantTail) {
		t.Errorf("upload path = %q, want suffix %q", transport.gotPath, wantTail)
	}
	if transport.gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", transport.gotMethod)
	}
	if !strings.Contains(transport.gotQuery, "uploadType=media") {
		t.Errorf("query = %q, want uploadType=media", transport.gotQuery)
	}
	if transport.gotType != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", transport.gotType)
	}
	if !bytes.Equal(transport.gotBody, data) {
		t.Errorf("uploaded body = %q, want the image bytes verbatim", transport.gotBody)
	}
	if img.ID != "new1" || img.Sha256 != "s256" {
		t.Errorf("image = %+v, want id=new1 sha256=s256", img)
	}
}

// TestDelete_targetsOneImage asserts Delete DELETEs the per-image path
// (.../{imageType}/{imageId}).
func TestDelete_targetsOneImage(t *testing.T) {
	transport := &rt{status: 204}
	hc := &http.Client{Transport: transport}
	if err := images.Delete(context.Background(), hc, "com.example.app", "edit-1", "en-US", images.PhoneScreenshots, "img-9"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	wantPath := "/androidpublisher/v3/applications/com.example.app/edits/edit-1/listings/en-US/phoneScreenshots/img-9"
	if transport.gotMethod != http.MethodDelete || transport.gotPath != wantPath {
		t.Errorf("request = %s %s, want DELETE %s", transport.gotMethod, transport.gotPath, wantPath)
	}
}

// TestDeleteAll_clearsTheSlot asserts DeleteAll DELETEs the slot path (no
// imageId): the only way to reorder a gallery per ADR-0013.
func TestDeleteAll_clearsTheSlot(t *testing.T) {
	transport := &rt{body: `{"deleted":[{"id":"a1"},{"id":"b2"}]}`}
	hc := &http.Client{Transport: transport}
	if err := images.DeleteAll(context.Background(), hc, "com.example.app", "edit-1", "en-US", images.PhoneScreenshots); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}
	wantPath := "/androidpublisher/v3/applications/com.example.app/edits/edit-1/listings/en-US/phoneScreenshots"
	if transport.gotMethod != http.MethodDelete || transport.gotPath != wantPath {
		t.Errorf("request = %s %s, want DELETE %s", transport.gotMethod, transport.gotPath, wantPath)
	}
}

// TestList_non2xx_returnsApiError asserts an API failure surfaces as an
// *api.Error carrying the operation and status, like the rest of the play
// layer.
func TestList_non2xx_returnsApiError(t *testing.T) {
	transport := &rt{status: 403, body: `{"error":{"message":"no access","errors":[{"reason":"forbidden"}]}}`}
	hc := &http.Client{Transport: transport}
	_, _, err := images.List(context.Background(), hc, "com.example.app", "e", "en-US", images.Icon)
	if err == nil {
		t.Fatal("want error on 403, got nil")
	}
	if !strings.Contains(err.Error(), "images.list") || !strings.Contains(err.Error(), "403") {
		t.Errorf("err = %v, want images.list + HTTP 403", err)
	}
}

// playStore is a fake transport that models Google Play's image store the
// way ADR-0013's soundness assumption requires: it keeps uploaded bytes
// verbatim and reports each image's sha256 = sha256(bytes). It is the
// in-test stand-in for the live integration the assumption ultimately needs.
type playStore struct {
	images []images.Image
	bytes  map[string][]byte
}

func (s *playStore) RoundTrip(req *http.Request) (*http.Response, error) {
	switch {
	case req.Method == http.MethodPost && strings.HasPrefix(req.URL.Path, "/upload/"):
		body, _ := io.ReadAll(req.Body)
		sum := sha256.Sum256(body)
		id := hex.EncodeToString(sum[:])[:8]
		img := images.Image{ID: id, URL: "https://play/img/" + id, Sha256: hex.EncodeToString(sum[:])}
		if s.bytes == nil {
			s.bytes = map[string][]byte{}
		}
		s.bytes[id] = body
		s.images = append(s.images, img)
		out, _ := json.Marshal(map[string]images.Image{"image": img})
		return jsonOK(out), nil
	case req.Method == http.MethodGet:
		out, _ := json.Marshal(map[string][]images.Image{"images": s.images})
		return jsonOK(out), nil
	}
	return jsonOK([]byte(`{}`)), nil
}

func jsonOK(body []byte) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

// TestSha256Soundness_uploadThenList is the ADR-0013 soundness gate: upload
// an image, List the slot, and assert the remote sha256 equals sha256 of the
// exact local bytes. If this ever fails against live Play, the fine-grained
// content diff in `images apply` is unsound and the always-replace fallback
// must be used instead. Here it is verified against a transport that stores
// bytes verbatim (the assumption made concrete); the result is recorded on
// issue #131.
func TestSha256Soundness_uploadThenList(t *testing.T) {
	local := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("pixels", 100))
	want := sha256.Sum256(local)
	wantHex := hex.EncodeToString(want[:])

	store := &playStore{}
	hc := &http.Client{Transport: store}

	if _, err := images.Upload(context.Background(), hc, "com.example.app", "e", "en-US", images.Icon, local); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	got, _, err := images.List(context.Background(), hc, "com.example.app", "e", "en-US", images.Icon)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d images after upload, want 1", len(got))
	}
	if got[0].Sha256 != wantHex {
		t.Errorf("remote sha256 = %q, want sha256(local) = %q: content-hash identity is UNSOUND, use always-replace fallback (ADR-0013)", got[0].Sha256, wantHex)
	}
}
