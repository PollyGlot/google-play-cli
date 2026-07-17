// Package apks_test exercises the play-layer legacy-APK upload against a
// fake transport. Upload wraps the Android Publisher edits.apks.upload
// endpoint: a resumable POST-initiate + chunked PUT of the APK into an
// already-open Edit, mirroring internal/play/bundles.
package apks_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/apks"
)

// rt is a RoundTripper that speaks the resumable-upload protocol: the POST
// initiate answers with a session URI in Location, then the chunk PUT(s)
// carry the bytes and return the final resource body. It records the
// method sequence plus the initiate path/query and the chunk headers.
//
// initiateStatus (when >= 400) short-circuits at initiate — the error the
// helper surfaces without ever opening the transfer. resumeAfter, when > 0,
// makes the first N chunk PUTs answer 308 (Resume Incomplete) so the
// resume path is exercised before the final chunk.
type rt struct {
	initiateStatus int // default 200
	putStatus      int // final chunk status, default 200
	body           string
	resumeAfter    int // number of 308 responses before the final chunk

	methods    []string
	initiPath  string
	initiQuery string
	initCType  string // X-Upload-Content-Type on the initiate
	putCType   string // Content-Type on the chunk PUT
	putBody    []byte
	putHits    int
}

func (r *rt) RoundTrip(req *http.Request) (*http.Response, error) {
	r.methods = append(r.methods, req.Method)

	if req.Method == http.MethodPost {
		r.initiPath = req.URL.Path
		r.initiQuery = req.URL.RawQuery
		r.initCType = req.Header.Get("X-Upload-Content-Type")
		status := r.initiateStatus
		if status == 0 {
			status = 200
		}
		if status >= 400 {
			return &http.Response{
				StatusCode: status,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(r.body)),
			}, nil
		}
		loc := req.URL.Scheme + "://" + req.URL.Host + req.URL.Path + "?upload_id=session-1"
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Location": []string{loc}},
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	}

	// Chunk PUT.
	r.putHits++
	r.putCType = req.Header.Get("Content-Type")
	b, _ := io.ReadAll(req.Body)
	r.putBody = append(r.putBody, b...)
	if r.putHits <= r.resumeAfter {
		// Resume Incomplete: acknowledge the bytes received so far.
		return &http.Response{
			StatusCode: 308,
			Header:     http.Header{"Range": []string{"bytes=0-" + strconv.Itoa(len(r.putBody)-1)}},
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	}
	status := r.putStatus
	if status == 0 {
		status = 200
	}
	body := r.body
	if body == "" {
		body = `{"versionCode":7}`
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func writeFakeAPK(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "app.apk")
	if err := os.WriteFile(p, []byte("fake-apk-content"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

// TestUpload_happyPath_resumableProtocol_returnsVersionCode asserts the
// initiate hits /apks with uploadType=resumable and the octet-stream media
// type in X-Upload-Content-Type, the chunk PUT carries the APK bytes, and
// the parsed versionCode comes back.
func TestUpload_happyPath_resumableProtocol_returnsVersionCode(t *testing.T) {
	apk := writeFakeAPK(t)
	transport := &rt{body: `{"versionCode":142}`}
	hc := &http.Client{Transport: transport}

	vc, err := apks.Upload(context.Background(), hc, "com.example.app", "edit-1", apk)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if vc != 142 {
		t.Errorf("versionCode = %d, want 142", vc)
	}
	wantPath := "/upload/androidpublisher/v3/applications/com.example.app/edits/edit-1/apks"
	if transport.initiPath != wantPath {
		t.Errorf("initiate path = %q, want %q", transport.initiPath, wantPath)
	}
	if transport.initiQuery != "uploadType=resumable" {
		t.Errorf("query = %q, want uploadType=resumable", transport.initiQuery)
	}
	// Protocol: POST initiate then PUT chunk.
	if len(transport.methods) != 2 || transport.methods[0] != http.MethodPost || transport.methods[1] != http.MethodPut {
		t.Errorf("method sequence = %v, want [POST PUT]", transport.methods)
	}
	if transport.initCType != "application/octet-stream" {
		t.Errorf("X-Upload-Content-Type = %q, want application/octet-stream", transport.initCType)
	}
	if transport.putCType != "application/octet-stream" {
		t.Errorf("chunk Content-Type = %q, want application/octet-stream", transport.putCType)
	}
	if string(transport.putBody) != "fake-apk-content" {
		t.Errorf("uploaded bytes = %q, want the APK content", transport.putBody)
	}
}

// TestUpload_resumeAfter308_completes asserts the helper resumes after an
// intermediate 308 and still returns the final versionCode.
func TestUpload_resumeAfter308_completes(t *testing.T) {
	apk := writeFakeAPK(t)
	transport := &rt{body: `{"versionCode":9}`, resumeAfter: 1}
	hc := &http.Client{Transport: transport}

	vc, err := apks.Upload(context.Background(), hc, "com.example.app", "edit-1", apk)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if vc != 9 {
		t.Errorf("versionCode = %d, want 9", vc)
	}
	if transport.putHits < 2 {
		t.Errorf("putHits = %d, want at least 2 (a 308 then the final chunk)", transport.putHits)
	}
}

// TestUpload_400_mapsToExit20 asserts a 400 at initiate (malformed APK /
// AAB-required app) surfaces as client-side validation (exit 20).
func TestUpload_400_mapsToExit20(t *testing.T) {
	apk := writeFakeAPK(t)
	transport := &rt{initiateStatus: 400, body: `{"error":{"code":400,"message":"APK not allowed; app requires an App Bundle."}}`}
	hc := &http.Client{Transport: transport}

	_, err := apks.Upload(context.Background(), hc, "com.example.app", "edit-1", apk)
	if err == nil {
		t.Fatal("Upload accepted a 400 response")
	}
	if got := exit.For(err); got != 20 {
		t.Errorf("exit.For(err) = %d, want 20; err=%v", got, err)
	}
}

// TestUpload_404_mapsToExit20 mirrors the 400 case: a 404 is also treated
// as malformed-artifact client-side validation (exit 20).
func TestUpload_404_mapsToExit20(t *testing.T) {
	apk := writeFakeAPK(t)
	transport := &rt{initiateStatus: 404, body: `{"error":{"code":404,"message":"Not found."}}`}
	hc := &http.Client{Transport: transport}

	_, err := apks.Upload(context.Background(), hc, "com.example.app", "edit-1", apk)
	if err == nil {
		t.Fatal("Upload accepted a 404 response")
	}
	if got := exit.For(err); got != 20 {
		t.Errorf("exit.For(err) = %d, want 20; err=%v", got, err)
	}
}

// TestUpload_directoryPath_returnsLocalIOError_exit20_noHTTP asserts a
// non-regular path (a directory) is rejected as a client-side validation
// error (exit 20) BEFORE any HTTP. Mirrors bundles.Upload's guard.
func TestUpload_directoryPath_returnsLocalIOError_exit20_noHTTP(t *testing.T) {
	dir := t.TempDir() // a directory, not a regular file
	transport := &rt{}
	hc := &http.Client{Transport: transport}

	_, err := apks.Upload(context.Background(), hc, "com.example.app", "edit-1", dir)
	if err == nil {
		t.Fatal("Upload accepted a directory as an APK path")
	}
	if got := exit.For(err); got != 20 {
		t.Errorf("exit.For(err) = %d, want 20; err=%v", got, err)
	}
	var ioErr *apks.LocalIOError
	if !errors.As(err, &ioErr) {
		t.Errorf("error %v is not *apks.LocalIOError", err)
	}
	if len(transport.methods) != 0 {
		t.Errorf("uploader hit the network for a directory path: %v", transport.methods)
	}
}
