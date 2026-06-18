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

// TestUpload_mediaShape asserts the media POST to the expansionFiles endpoint.
func TestUpload_mediaShape(t *testing.T) {
	var gotURL, gotCT string
	rt := rtFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		gotCT = r.Header.Get("Content-Type")
		return resp(200, `{"expansionFile":{"fileSize":"123"}}`), nil
	})
	hc := &http.Client{Transport: rt}
	raw, err := expansionfiles.Upload(context.Background(), hc, "com.example.app", "edit1", 142, expansionfiles.TypeMain, writeOBB(t))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	for _, want := range []string{"/upload/androidpublisher/v3/applications/com.example.app/edits/edit1/apks/142/expansionFiles/main", "uploadType=media"} {
		if !strings.Contains(gotURL, want) {
			t.Errorf("url %q missing %q", gotURL, want)
		}
	}
	if gotCT != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", gotCT)
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
