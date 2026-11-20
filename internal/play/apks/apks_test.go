package apks_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/apks"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

type resumable struct {
	initiateStatus int
	putStatus      int
	body           string
	resumeAfter    int

	methods    []string
	initiPath  string
	initiQuery string
	initCType  string
	putCType   string
	putBody    []byte
	putHits    int
}

func (r *resumable) client() *http.Client {
	return &http.Client{Transport: testkit.RoundTripFunc(r.serve)}
}

func (r *resumable) serve(req *http.Request) (*http.Response, error) {
	if resp, ok := testkit.TokenResponse(req); ok {
		return resp, nil
	}
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
			return testkit.Response(status, r.body), nil
		}
		resp := testkit.Response(status, "")
		resp.Header.Set("Location", req.URL.Scheme+"://"+req.URL.Host+req.URL.Path+"?upload_id=session-1")
		return resp, nil
	}

	r.putHits++
	r.putCType = req.Header.Get("Content-Type")
	b, _ := io.ReadAll(req.Body)
	r.putBody = append(r.putBody, b...)
	if r.putHits <= r.resumeAfter {
		resp := testkit.Response(308, "")
		resp.Header.Set("Range", "bytes=0-"+strconv.Itoa(len(r.putBody)-1))
		return resp, nil
	}
	status := r.putStatus
	if status == 0 {
		status = 200
	}
	body := r.body
	if body == "" {
		body = `{"versionCode":7}`
	}
	return testkit.Response(status, body), nil
}

func writeFakeAPK(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "app.apk")
	if err := os.WriteFile(p, []byte("fake-apk-content"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

func TestUpload_happyPath_resumableProtocol_returnsVersionCode(t *testing.T) {
	apk := writeFakeAPK(t)
	transport := &resumable{body: `{"versionCode":142}`}
	hc := transport.client()

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

func TestUpload_resumeAfter308_completes(t *testing.T) {
	apk := writeFakeAPK(t)
	transport := &resumable{body: `{"versionCode":9}`, resumeAfter: 1}
	hc := transport.client()

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

func TestUpload_400_mapsToExit20(t *testing.T) {
	apk := writeFakeAPK(t)
	transport := &resumable{initiateStatus: 400, body: `{"error":{"code":400,"message":"APK not allowed; app requires an App Bundle."}}`}
	hc := transport.client()

	_, err := apks.Upload(context.Background(), hc, "com.example.app", "edit-1", apk)
	if err == nil {
		t.Fatal("Upload accepted a 400 response")
	}
	if got := exit.For(err); got != 20 {
		t.Errorf("exit.For(err) = %d, want 20; err=%v", got, err)
	}
}

func TestUpload_404_mapsToExit20(t *testing.T) {
	apk := writeFakeAPK(t)
	transport := &resumable{initiateStatus: 404, body: `{"error":{"code":404,"message":"Not found."}}`}
	hc := transport.client()

	_, err := apks.Upload(context.Background(), hc, "com.example.app", "edit-1", apk)
	if err == nil {
		t.Fatal("Upload accepted a 404 response")
	}
	if got := exit.For(err); got != 20 {
		t.Errorf("exit.For(err) = %d, want 20; err=%v", got, err)
	}
}

func TestUpload_directoryPath_returnsLocalIOError_exit20_noHTTP(t *testing.T) {
	dir := t.TempDir()
	transport := &resumable{}
	hc := transport.client()

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
