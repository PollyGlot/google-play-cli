package expansionfiles_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/expansionfiles"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func writeOBB(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "assets.obb")
	if err := os.WriteFile(p, []byte("fake obb bytes"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

// onlyCall returns the single request the Fake served, failing the test on
// any other count.
func onlyCall(t *testing.T, f *testkit.Fake) testkit.Call {
	t.Helper()
	calls := f.Calls()
	if len(calls) != 1 {
		t.Fatalf("want exactly one request, got %d: %+v", len(calls), calls)
	}
	return calls[0]
}

// resumable speaks the resumable-upload protocol through a
// testkit.RoundTripFunc (a Fake cannot set the Location header the protocol
// needs): the POST initiate answers with a session URI in Location, then the
// chunk PUT carries the bytes and returns the final resource body. It records
// the initiate URL, the X-Upload-Content-Type, and the method sequence.
type resumable struct {
	body string

	methods   []string
	initiURL  string
	initCType string
}

// client returns an http.Client whose every request lands on r.
func (r *resumable) client() *http.Client {
	return &http.Client{Transport: testkit.RoundTripFunc(r.serve)}
}

func (r *resumable) serve(req *http.Request) (*http.Response, error) {
	if resp, ok := testkit.TokenResponse(req); ok {
		return resp, nil
	}
	r.methods = append(r.methods, req.Method)
	if req.Method == http.MethodPost {
		r.initiURL = req.URL.String()
		r.initCType = req.Header.Get("X-Upload-Content-Type")
		resp := testkit.Response(200, "")
		resp.Header.Set("Location", req.URL.Scheme+"://"+req.URL.Host+req.URL.Path+"?upload_id=session-1")
		return resp, nil
	}
	return testkit.Response(200, r.body), nil
}

// TestUpload_resumableShape asserts the resumable initiate targets the
// expansionFiles endpoint with uploadType=resumable, the media type travels
// in X-Upload-Content-Type, and the response body passes through verbatim.
func TestUpload_resumableShape(t *testing.T) {
	rt := &resumable{body: `{"expansionFile":{"fileSize":"123"}}`}
	raw, err := expansionfiles.Upload(context.Background(), rt.client(), "com.example.app", "edit1", 142, expansionfiles.TypeMain, writeOBB(t))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	for _, want := range []string{"/upload/androidpublisher/v3/applications/com.example.app/edits/edit1/apks/142/expansionFiles/main", "uploadType=resumable"} {
		if !strings.Contains(rt.initiURL, want) {
			t.Errorf("initiate url %q missing %q", rt.initiURL, want)
		}
	}
	if len(rt.methods) != 2 || rt.methods[0] != http.MethodPost || rt.methods[1] != http.MethodPut {
		t.Errorf("method sequence = %v, want [POST PUT]", rt.methods)
	}
	if rt.initCType != "application/octet-stream" {
		t.Errorf("X-Upload-Content-Type = %q, want application/octet-stream", rt.initCType)
	}
	if !strings.Contains(string(raw), `"fileSize":"123"`) {
		t.Errorf("raw %s should pass through", raw)
	}
}

// TestUpload_directory_exit20 asserts a non-regular path is exit 20, no network.
func TestUpload_directory_exit20(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(200, `{}`))
	hc := &http.Client{Transport: fake}
	_, err := expansionfiles.Upload(context.Background(), hc, "com.example.app", "edit1", 142, expansionfiles.TypeMain, t.TempDir())
	var ioErr *expansionfiles.LocalIOError
	if !errors.As(err, &ioErr) || ioErr.ExitCode() != 20 {
		t.Errorf("err = %v, want *LocalIOError exit 20", err)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("a directory must not reach the network: %+v", calls)
	}
}

// TestUpdate_putsReferencesVersion asserts the PUT body carries referencesVersion.
func TestUpdate_putsReferencesVersion(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(200, `{"referencesVersion":140}`))
	hc := &http.Client{Transport: fake}
	if _, err := expansionfiles.Update(context.Background(), hc, "com.example.app", "edit1", 142, expansionfiles.TypeMain, 140); err != nil {
		t.Fatalf("Update: %v", err)
	}
	c := onlyCall(t, fake)
	if c.Method != http.MethodPut {
		t.Errorf("method = %s, want PUT", c.Method)
	}
	if !strings.HasSuffix(c.Path, "/expansionFiles/main") || c.Query != "" {
		t.Errorf("url %s?%s should address the main expansion file", c.Path, c.Query)
	}
	if !strings.Contains(string(c.Body), `"referencesVersion":140`) {
		t.Errorf("body %q should carry referencesVersion 140", c.Body)
	}
}

// TestGet_parsesFileSizeXorReferences asserts the parsed view.
func TestGet_parsesFileSizeXorReferences(t *testing.T) {
	hc := &http.Client{Transport: testkit.NewFake(testkit.Any(200, `{"fileSize":"99999"}`))}
	ef, _, err := expansionfiles.Get(context.Background(), hc, "com.example.app", "edit1", 142, expansionfiles.TypeMain)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ef.HasFile() || ef.FileSize != "99999" {
		t.Errorf("ef = %+v, want its own file of size 99999", ef)
	}
}
