package images_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/images"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func fakeWith(status int, body string) *testkit.Fake {
	return testkit.NewFake(testkit.Any(status, body))
}

func onlyCall(t *testing.T, f *testkit.Fake) testkit.Call {
	t.Helper()
	calls := f.Calls()
	if len(calls) != 1 {
		t.Fatalf("want exactly one request, got %d: %+v", len(calls), calls)
	}
	return calls[0]
}

func TestTypes_modelsTheEightApiTypes(t *testing.T) {
	got := images.Types()
	if len(got) != 8 {
		t.Fatalf("got %d types, want 8: %v", len(got), got)
	}
	wantGallery := map[images.Type]bool{
		images.Icon: false, images.FeatureGraphic: false, images.TvBanner: false,
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
	for _, gone := range []string{"chromebookScreenshots", "promoGraphic"} {
		if _, ok := images.ParseType(gone); ok {
			t.Errorf("%s must not be an addressable type (not in AppImageType)", gone)
		}
	}
	if !images.Retired("promoGraphic") {
		t.Errorf(`Retired("promoGraphic") = false, want true`)
	}
	if images.Retired("icon") || images.Retired("chromebookScreenshots") {
		t.Errorf("Retired must only name slots removed from AppImageType")
	}
}

func TestList_parsesImages_andReturnsRawBody(t *testing.T) {
	raw := `{"images":[` +
		`{"id":"a1","url":"https://play/img/a1","sha1":"deadbeef","sha256":"aaaa"},` +
		`{"id":"b2","url":"https://play/img/b2","sha1":"feedface","sha256":"bbbb"}` +
		`],"kind":"androidpublisher#imagesListResponse"}`
	transport := fakeWith(200, raw)
	hc := &http.Client{Transport: transport}

	got, gotRaw, err := images.List(context.Background(), hc, "com.example.app", "edit-1", "en-US", images.PhoneScreenshots)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	wantPath := "/androidpublisher/v3/applications/com.example.app/edits/edit-1/listings/en-US/phoneScreenshots"
	c := onlyCall(t, transport)
	if c.Method != http.MethodGet || c.Path != wantPath {
		t.Errorf("request = %s %s, want GET %s", c.Method, c.Path, wantPath)
	}
	if len(got) != 2 || got[0].ID != "a1" || got[1].Sha256 != "bbbb" {
		t.Fatalf("images = %+v, want [a1, b2 with sha256 bbbb]", got)
	}
	if strings.TrimSpace(string(gotRaw)) != strings.TrimSpace(raw) {
		t.Errorf("raw = %s, want verbatim envelope", gotRaw)
	}
}

func TestList_absentSlotIsEmpty(t *testing.T) {
	transport := fakeWith(200, `{"kind":"androidpublisher#imagesListResponse"}`)
	hc := &http.Client{Transport: transport}
	got, _, err := images.List(context.Background(), hc, "com.example.app", "e", "fr-FR", images.Icon)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d images for an absent slot, want 0", len(got))
	}
}

func TestUpload_usesMediaProtocol_andParsesImage(t *testing.T) {
	transport := fakeWith(200, `{"image":{"id":"new1","url":"https://play/img/new1","sha1":"s1","sha256":"s256"}}`)
	hc := &http.Client{Transport: transport}
	data := []byte("\x89PNG\r\n\x1a\nfake-bytes")

	img, err := images.Upload(context.Background(), hc, "com.example.app", "edit-1", "en-US", images.Icon, data, "")
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	c := onlyCall(t, transport)
	if !strings.HasPrefix(c.Path, "/upload/androidpublisher/v3/") {
		t.Errorf("upload path = %q, want /upload sub-host", c.Path)
	}
	wantTail := "/applications/com.example.app/edits/edit-1/listings/en-US/icon"
	if !strings.HasSuffix(c.Path, wantTail) {
		t.Errorf("upload path = %q, want suffix %q", c.Path, wantTail)
	}
	if c.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", c.Method)
	}
	if !strings.Contains(c.Query, "uploadType=media") {
		t.Errorf("query = %q, want uploadType=media", c.Query)
	}
	if !bytes.Equal(c.Body, data) {
		t.Errorf("uploaded body = %q, want the image bytes verbatim", c.Body)
	}
	if img.ID != "new1" || img.Sha256 != "s256" {
		t.Errorf("image = %+v, want id=new1 sha256=s256", img)
	}
}

func TestUpload_announcesSniffedMediaType(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var pngBuf, jpegBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	if err := jpeg.Encode(&jpegBuf, img, nil); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}

	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"png", pngBuf.Bytes(), "image/png"},
		{"jpeg", jpegBuf.Bytes(), "image/jpeg"},
		{"gif passes through for Play to judge", []byte("GIF89a\x01\x00\x01\x00"), "image/gif"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport := fakeWith(200, `{"image":{"id":"new1","sha256":"s256"}}`)
			hc := &http.Client{Transport: transport}
			if _, err := images.Upload(context.Background(), hc, "com.example.app", "edit-1", "en-US", images.FeatureGraphic, tc.data, ""); err != nil {
				t.Fatalf("Upload: %v", err)
			}
			if got := onlyCall(t, transport).Header.Get("Content-Type"); got != tc.want {
				t.Errorf("Content-Type = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDelete_targetsOneImage(t *testing.T) {
	transport := fakeWith(204, ``)
	hc := &http.Client{Transport: transport}
	if err := images.Delete(context.Background(), hc, "com.example.app", "edit-1", "en-US", images.PhoneScreenshots, "img-9"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	wantPath := "/androidpublisher/v3/applications/com.example.app/edits/edit-1/listings/en-US/phoneScreenshots/img-9"
	c := onlyCall(t, transport)
	if c.Method != http.MethodDelete || c.Path != wantPath {
		t.Errorf("request = %s %s, want DELETE %s", c.Method, c.Path, wantPath)
	}
}

func TestDeleteAll_clearsTheSlot(t *testing.T) {
	transport := fakeWith(200, `{"deleted":[{"id":"a1"},{"id":"b2"}]}`)
	hc := &http.Client{Transport: transport}
	if err := images.DeleteAll(context.Background(), hc, "com.example.app", "edit-1", "en-US", images.PhoneScreenshots); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}
	wantPath := "/androidpublisher/v3/applications/com.example.app/edits/edit-1/listings/en-US/phoneScreenshots"
	c := onlyCall(t, transport)
	if c.Method != http.MethodDelete || c.Path != wantPath {
		t.Errorf("request = %s %s, want DELETE %s", c.Method, c.Path, wantPath)
	}
}

func TestList_non2xx_returnsApiError(t *testing.T) {
	transport := fakeWith(403, `{"error":{"message":"no access","errors":[{"reason":"forbidden"}]}}`)
	hc := &http.Client{Transport: transport}
	_, _, err := images.List(context.Background(), hc, "com.example.app", "e", "en-US", images.Icon)
	if err == nil {
		t.Fatal("want error on 403, got nil")
	}
	if !strings.Contains(err.Error(), "images.list") || !strings.Contains(err.Error(), "403") {
		t.Errorf("err = %v, want images.list + HTTP 403", err)
	}
}

func playStore() *testkit.Fake {
	var stored []images.Image
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasPrefix(c.Path, "/upload/"):
			sum := sha256.Sum256(c.Body)
			id := hex.EncodeToString(sum[:])[:8]
			img := images.Image{ID: id, URL: "https://play/img/" + id, Sha256: hex.EncodeToString(sum[:])}
			stored = append(stored, img)
			out, _ := json.Marshal(map[string]images.Image{"image": img})
			return http.StatusOK, string(out), true
		case c.Method == http.MethodGet:
			out, _ := json.Marshal(map[string][]images.Image{"images": stored})
			return http.StatusOK, string(out), true
		}
		return http.StatusOK, `{}`, true
	})
}

func TestSha256Soundness_uploadThenList(t *testing.T) {
	local := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("pixels", 100))
	want := sha256.Sum256(local)
	wantHex := hex.EncodeToString(want[:])

	hc := &http.Client{Transport: playStore()}

	if _, err := images.Upload(context.Background(), hc, "com.example.app", "e", "en-US", images.Icon, local, ""); err != nil {
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

func TestOriginalURL(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"bare url gains =s0", "https://lh3.googleusercontent.com/AbC-123_x", "https://lh3.googleusercontent.com/AbC-123_x=s0"},
		{"size option replaced", "https://play-lh.googleusercontent.com/abc=w512-h512", "https://play-lh.googleusercontent.com/abc=s0"},
		{"already original", "https://lh3.googleusercontent.com/abc=s0", "https://lh3.googleusercontent.com/abc=s0"},
		{"nested path", "https://lh3.googleusercontent.com/d/abc=w100", "https://lh3.googleusercontent.com/d/abc=s0"},
		{"query kept", "https://lh3.googleusercontent.com/abc=w512?authuser=0", "https://lh3.googleusercontent.com/abc=s0?authuser=0"},
		{"fragment kept", "https://lh3.googleusercontent.com/abc#x", "https://lh3.googleusercontent.com/abc=s0#x"},
		{"other host untouched", "https://play-lh.test/en/icon", "https://play-lh.test/en/icon"},
		{"lookalike host untouched", "https://googleusercontent.com.evil.test/abc", "https://googleusercontent.com.evil.test/abc"},
		{"no image id untouched", "https://lh3.googleusercontent.com/", "https://lh3.googleusercontent.com/"},
		{"no path untouched", "https://lh3.googleusercontent.com", "https://lh3.googleusercontent.com"},
		{"unparsable untouched", "://bad", "://bad"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := images.OriginalURL(c.in); got != c.want {
				t.Errorf("OriginalURL(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
