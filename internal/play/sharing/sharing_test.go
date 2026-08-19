package sharing_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/sharing"
)

// roundTripperFunc adapts a function to http.RoundTripper so a test can route
// the single upload request without a network. No /token exchange here: these
// API-layer tests call the package directly with a bare *http.Client.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func resp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

const artifactBody = `{"downloadUrl":"https://play.google.com/apps/test/abc123","certificateFingerprint":"AA:BB:CC","sha256":"deadbeef"}`

// TestUploadAPK_happyPath asserts the request hits the apk artifact endpoint
// with the media-upload shape and the parsed + raw artifact comes back.
func TestUploadAPK_happyPath(t *testing.T) {
	var gotURL, gotMethod, gotContentType string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		return resp(200, artifactBody), nil
	})
	hc := &http.Client{Transport: rt}

	art, raw, err := sharing.UploadAPK(context.Background(), hc, "com.example.app", writeFile(t, "app.apk", "PK\x03\x04 fake apk"))
	if err != nil {
		t.Fatalf("UploadAPK: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	for _, want := range []string{"/upload/androidpublisher/v3/applications/internalappsharing/com.example.app/artifacts/apk", "uploadType=media"} {
		if !strings.Contains(gotURL, want) {
			t.Errorf("url %q missing %q", gotURL, want)
		}
	}
	if gotContentType != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", gotContentType)
	}
	if art.DownloadURL != "https://play.google.com/apps/test/abc123" {
		t.Errorf("downloadUrl = %q, want the shareable link", art.DownloadURL)
	}
	if strings.TrimSpace(string(raw)) != artifactBody {
		t.Errorf("raw passthrough = %s, want verbatim body", raw)
	}
}

// TestUploadBundle_pathSegment asserts the bundle endpoint is used for an AAB.
func TestUploadBundle_pathSegment(t *testing.T) {
	var gotURL string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		return resp(200, artifactBody), nil
	})
	hc := &http.Client{Transport: rt}
	if _, _, err := sharing.UploadBundle(context.Background(), hc, "com.example.app", writeFile(t, "app.aab", "fake aab")); err != nil {
		t.Fatalf("UploadBundle: %v", err)
	}
	if !strings.Contains(gotURL, "/artifacts/bundle") {
		t.Errorf("url %q should hit the bundle artifact endpoint", gotURL)
	}
}

// TestUpload_directory_exit20 asserts a non-regular path is a client-side
// *LocalIOError (exit 20), never a network call.
func TestUpload_directory_exit20(t *testing.T) {
	called := false
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		return resp(200, artifactBody), nil
	})
	hc := &http.Client{Transport: rt}

	_, _, err := sharing.UploadAPK(context.Background(), hc, "com.example.app", t.TempDir())
	if err == nil {
		t.Fatal("expected an error for a directory path")
	}
	var ioErr *sharing.LocalIOError
	if !errors.As(err, &ioErr) {
		t.Fatalf("err = %v (%T), want *sharing.LocalIOError", err, err)
	}
	if ioErr.ExitCode() != 20 {
		t.Errorf("ExitCode = %d, want 20", ioErr.ExitCode())
	}
	if called {
		t.Error("a non-regular file must not reach the network")
	}
}

// TestUpload_apiError_mapsExit asserts an API 4xx surfaces as *api.Error (exit
// 30 via StatusToExitCode).
func TestUpload_apiError_mapsExit(t *testing.T) {
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return resp(400, `{"error":{"code":400,"message":"artifact too large"}}`), nil
	})
	hc := &http.Client{Transport: rt}

	_, _, err := sharing.UploadAPK(context.Background(), hc, "com.example.app", writeFile(t, "app.apk", "x"))
	if err == nil {
		t.Fatal("expected an API error")
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.Error", err, err)
	}
	if apiErr.ExitCode() != 30 {
		t.Errorf("ExitCode = %d, want 30", apiErr.ExitCode())
	}
}
