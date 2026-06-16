// Package mappings_test exercises the play-layer ProGuard/R8 mapping
// upload against a fake transport. Upload wraps the Android Publisher
// edits.deobfuscationfiles.upload endpoint: a simple-media POST of the
// mapping file into an already-open Edit, keyed by the APK versionCode
// and the deobfuscation file type (proguard / nativeCode).
package mappings_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/mappings"
)

// rt is a minimal RoundTripper that records the request line + body and
// returns a canned deobfuscationfiles.upload response.
type rt struct {
	status int
	body   string

	gotMethod string
	gotPath   string
	gotCT     string
	gotBody   []byte
}

func (r *rt) RoundTrip(req *http.Request) (*http.Response, error) {
	r.gotMethod = req.Method
	r.gotPath = req.URL.Path
	r.gotCT = req.Header.Get("Content-Type")
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
// Upload POSTs the mapping to the upload-host deobfuscationFiles path
// keyed by versionCode + file type, sends it as octet-stream media, and
// parses the {"deobfuscationFile":{"symbolType":...}} envelope while
// handing back the raw body for the ADR-0003 pass-through.
func TestUpload_happyPath_hitsDeobfuscationEndpoint_parsesSymbolType(t *testing.T) {
	mapping := writeMapping(t)
	transport := &rt{body: `{"deobfuscationFile":{"symbolType":"proguard"}}`}
	hc := &http.Client{Transport: transport}

	res, err := mappings.Upload(context.Background(), hc, "com.example.app", "edit-123", 142, mappings.TypeProguard, mapping)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	wantPath := "/upload/androidpublisher/v3/applications/com.example.app/edits/edit-123/apks/142/deobfuscationFiles/proguard"
	if transport.gotMethod != http.MethodPost || transport.gotPath != wantPath {
		t.Errorf("request = %s %s, want POST %s", transport.gotMethod, transport.gotPath, wantPath)
	}
	if transport.gotCT != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", transport.gotCT)
	}
	if len(transport.gotBody) == 0 {
		t.Error("uploader sent an empty body; want the mapping bytes")
	}
	if res.SymbolType != "proguard" {
		t.Errorf("SymbolType = %q, want proguard", res.SymbolType)
	}
	if !strings.Contains(string(res.Raw), "symbolType") {
		t.Errorf("Raw = %q, want the verbatim response body", string(res.Raw))
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
	if !strings.HasSuffix(transport.gotPath, "/deobfuscationFiles/nativeCode") {
		t.Errorf("path = %q, want it to end in /deobfuscationFiles/nativeCode", transport.gotPath)
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
	if transport.gotMethod != "" {
		t.Errorf("uploader hit the network on a missing file: %s %s", transport.gotMethod, transport.gotPath)
	}
}

// TestUpload_apiError_surfacesAPIError asserts a non-2xx response is
// mapped to *api.Error carrying the upstream status code.
func TestUpload_apiError_surfacesAPIError(t *testing.T) {
	mapping := writeMapping(t)
	transport := &rt{status: http.StatusForbidden, body: `{"error":{"code":403,"message":"caller lacks permission"}}`}
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
