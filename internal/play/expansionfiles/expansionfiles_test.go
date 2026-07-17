package expansionfiles_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/expansionfiles"
)

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func resp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func writeOBB(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "assets.obb")
	if err := os.WriteFile(p, []byte("fake obb bytes"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

// resumableRT speaks the resumable-upload protocol: the POST initiate
// answers with a session URI in Location, then the chunk PUT carries the
// bytes and returns the final resource body. It records the initiate URL,
// the X-Upload-Content-Type, and the method sequence.
type resumableRT struct {
	body string

	methods   []string
	initiURL  string
	initCType string
}

func (r *resumableRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.methods = append(r.methods, req.Method)
	if req.Method == http.MethodPost {
		r.initiURL = req.URL.String()
		r.initCType = req.Header.Get("X-Upload-Content-Type")
		loc := req.URL.Scheme + "://" + req.URL.Host + req.URL.Path + "?upload_id=session-1"
		return &http.Response{StatusCode: 200, Header: http.Header{"Location": []string{loc}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	return resp(200, r.body), nil
}

// TestUpload_resumableShape asserts the resumable initiate targets the
// expansionFiles endpoint with uploadType=resumable, the media type travels
// in X-Upload-Content-Type, and the response body passes through verbatim.
func TestUpload_resumableShape(t *testing.T) {
	rt := &resumableRT{body: `{"expansionFile":{"fileSize":"123"}}`}
	hc := &http.Client{Transport: rt}
	raw, err := expansionfiles.Upload(context.Background(), hc, "com.example.app", "edit1", 142, expansionfiles.TypeMain, writeOBB(t))
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
	called := false
	rt := rtFunc(func(r *http.Request) (*http.Response, error) { called = true; return resp(200, `{}`), nil })
	hc := &http.Client{Transport: rt}
	_, err := expansionfiles.Upload(context.Background(), hc, "com.example.app", "edit1", 142, expansionfiles.TypeMain, t.TempDir())
	var ioErr *expansionfiles.LocalIOError
	if !errors.As(err, &ioErr) || ioErr.ExitCode() != 20 {
		t.Errorf("err = %v, want *LocalIOError exit 20", err)
	}
	if called {
		t.Error("a directory must not reach the network")
	}
}

// TestUpdate_putsReferencesVersion asserts the PUT body carries referencesVersion.
func TestUpdate_putsReferencesVersion(t *testing.T) {
	var gotMethod, gotURL string
	var gotBody []byte
	rt := rtFunc(func(r *http.Request) (*http.Response, error) {
		gotMethod = r.Method
		gotURL = r.URL.String()
		gotBody, _ = io.ReadAll(r.Body)
		return resp(200, `{"referencesVersion":140}`), nil
	})
	hc := &http.Client{Transport: rt}
	if _, err := expansionfiles.Update(context.Background(), hc, "com.example.app", "edit1", 142, expansionfiles.TypeMain, 140); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %s, want PUT", gotMethod)
	}
	if !strings.HasSuffix(gotURL, "/expansionFiles/main") {
		t.Errorf("url %q should address the main expansion file", gotURL)
	}
	if !strings.Contains(string(gotBody), `"referencesVersion":140`) {
		t.Errorf("body %q should carry referencesVersion 140", gotBody)
	}
}

// TestGet_parsesFileSizeXorReferences asserts the parsed view.
func TestGet_parsesFileSizeXorReferences(t *testing.T) {
	rt := rtFunc(func(r *http.Request) (*http.Response, error) {
		return resp(200, `{"fileSize":"99999"}`), nil
	})
	hc := &http.Client{Transport: rt}
	ef, _, err := expansionfiles.Get(context.Background(), hc, "com.example.app", "edit1", 142, expansionfiles.TypeMain)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ef.HasFile() || ef.FileSize != "99999" {
		t.Errorf("ef = %+v, want its own file of size 99999", ef)
	}
}
