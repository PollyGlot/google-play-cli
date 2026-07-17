// Package mappings_test exercises the play-layer ProGuard/R8 mapping
// upload against a fake transport. Upload wraps the Android Publisher
// edits.deobfuscationfiles.upload endpoint: a resumable POST-initiate +
// chunked PUT of the mapping file into an already-open Edit, keyed by the
// APK versionCode and the deobfuscation file type (proguard / nativeCode).
package mappings_test

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
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/mappings"
)

// rt speaks the resumable-upload protocol: POST initiate returns a session
// URI in Location, then the chunk PUT(s) carry the bytes and return the
// final resource body. initiateStatus (>= 400) short-circuits at initiate;
// resumeAfter makes the first N chunk PUTs answer 308 before the final one.
type rt struct {
	initiateStatus int
	putStatus      int
	body           string
	resumeAfter    int

	methods   []string
	initiPath string
	initCType string
	putCType  string
	putBody   []byte
	putHits   int
}

func (r *rt) RoundTrip(req *http.Request) (*http.Response, error) {
	r.methods = append(r.methods, req.Method)

	if req.Method == http.MethodPost {
		r.initiPath = req.URL.Path
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

	r.putHits++
	r.putCType = req.Header.Get("Content-Type")
	b, _ := io.ReadAll(req.Body)
	r.putBody = append(r.putBody, b...)
	if r.putHits <= r.resumeAfter {
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
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(r.body)),
	}, nil
}

// writeMapping creates a non-empty mapping.txt the uploader can stream.
func writeMapping(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mapping.txt")
	if err := os.WriteFile(p, []byte("com.example.Foo -> a.a:\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

// TestUpload_happyPath_hitsDeobfuscationEndpoint_parsesSymbolType asserts
// Upload initiates the resumable upload against the upload-host
// deobfuscationFiles path keyed by versionCode + file type, streams the
// mapping bytes in the chunk PUT as octet-stream, and parses the
// {"deobfuscationFile":{"symbolType":...}} envelope while handing back the
// raw body for the ADR-0003 pass-through.
func TestUpload_happyPath_hitsDeobfuscationEndpoint_parsesSymbolType(t *testing.T) {
	mapping := writeMapping(t)
	transport := &rt{body: `{"deobfuscationFile":{"symbolType":"proguard"}}`}
	hc := &http.Client{Transport: transport}

	res, err := mappings.Upload(context.Background(), hc, "com.example.app", "edit-123", 142, mappings.TypeProguard, mapping)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	wantPath := "/upload/androidpublisher/v3/applications/com.example.app/edits/edit-123/apks/142/deobfuscationFiles/proguard"
	if transport.initiPath != wantPath {
		t.Errorf("initiate path = %q, want %q", transport.initiPath, wantPath)
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
	if len(transport.putBody) == 0 {
		t.Error("uploader sent an empty body; want the mapping bytes")
	}
	if res.SymbolType != "proguard" {
		t.Errorf("SymbolType = %q, want proguard", res.SymbolType)
	}
	if !strings.Contains(string(res.Raw), "symbolType") {
		t.Errorf("Raw = %q, want the verbatim response body", string(res.Raw))
	}
}

// TestUpload_resumeAfter308_completes asserts the helper resumes after an
// intermediate 308 and still parses the final resource body.
func TestUpload_resumeAfter308_completes(t *testing.T) {
	mapping := writeMapping(t)
	transport := &rt{body: `{"deobfuscationFile":{"symbolType":"proguard"}}`, resumeAfter: 1}
	hc := &http.Client{Transport: transport}

	res, err := mappings.Upload(context.Background(), hc, "com.example.app", "edit-1", 7, mappings.TypeProguard, mapping)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if res.SymbolType != "proguard" {
		t.Errorf("SymbolType = %q, want proguard", res.SymbolType)
	}
	if transport.putHits < 2 {
		t.Errorf("putHits = %d, want at least 2 (a 308 then the final chunk)", transport.putHits)
	}
}

// TestUpload_nativeCodeType_inPath asserts the file-type segment is taken
// verbatim from the Discovery enum (nativeCode), not lowercased/invented.
func TestUpload_nativeCodeType_inPath(t *testing.T) {
	mapping := writeMapping(t)
	transport := &rt{body: `{"deobfuscationFile":{"symbolType":"nativeCode"}}`}
	hc := &http.Client{Transport: transport}

	if _, err := mappings.Upload(context.Background(), hc, "com.example.app", "edit-1", 7, mappings.TypeNativeCode, mapping); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if !strings.HasSuffix(transport.initiPath, "/deobfuscationFiles/nativeCode") {
		t.Errorf("path = %q, want it to end in /deobfuscationFiles/nativeCode", transport.initiPath)
	}
}

// TestUpload_missingFile_returnsLocalIOError_exit20 asserts a missing
// mapping path fails as a client-side validation error (exit 20) before
// any HTTP — distinct from a transport error (50).
func TestUpload_missingFile_returnsLocalIOError_exit20(t *testing.T) {
	transport := &rt{}
	hc := &http.Client{Transport: transport}

	_, err := mappings.Upload(context.Background(), hc, "com.example.app", "edit-1", 142, mappings.TypeProguard, "/no/such/mapping.txt")
	if err == nil {
		t.Fatal("Upload returned nil error for a missing file")
	}
	if got := exit.For(err); got != 20 {
		t.Errorf("exit.For(err) = %d, want 20; err=%v", got, err)
	}
	if len(transport.methods) != 0 {
		t.Errorf("uploader hit the network on a missing file: %v", transport.methods)
	}
}

// TestUpload_apiError_surfacesAPIError asserts a non-2xx response is
// mapped to *api.Error carrying the upstream status code.
func TestUpload_apiError_surfacesAPIError(t *testing.T) {
	mapping := writeMapping(t)
	transport := &rt{initiateStatus: http.StatusForbidden, body: `{"error":{"code":403,"message":"caller lacks permission"}}`}
	hc := &http.Client{Transport: transport}

	_, err := mappings.Upload(context.Background(), hc, "com.example.app", "edit-1", 142, mappings.TypeProguard, mapping)
	if err == nil {
		t.Fatal("Upload returned nil error for a 403 response")
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %v is not *api.Error", err)
	}
	if apiErr.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, want 403", apiErr.StatusCode)
	}
}

// TestUpload_directoryPath_returnsLocalIOError_exit20_noHTTP asserts a
// non-regular path (a directory) is rejected as a client-side validation
// error (exit 20) BEFORE any HTTP. Regression for the PR #264 review.
func TestUpload_directoryPath_returnsLocalIOError_exit20_noHTTP(t *testing.T) {
	dir := t.TempDir() // a directory, not a regular file
	transport := &rt{}
	hc := &http.Client{Transport: transport}

	_, err := mappings.Upload(context.Background(), hc, "com.example.app", "edit-1", 142, mappings.TypeProguard, dir)
	if err == nil {
		t.Fatal("Upload accepted a directory as a mapping path")
	}
	if got := exit.For(err); got != 20 {
		t.Errorf("exit.For(err) = %d, want 20; err=%v", got, err)
	}
	var ioErr *mappings.LocalIOError
	if !errors.As(err, &ioErr) {
		t.Errorf("error %v is not *mappings.LocalIOError", err)
	}
	if len(transport.methods) != 0 {
		t.Errorf("uploader hit the network for a directory path: %v", transport.methods)
	}
}
