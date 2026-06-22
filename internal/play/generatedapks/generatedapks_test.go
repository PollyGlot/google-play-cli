package generatedapks_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/generatedapks"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func resp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

const listBody = `{
  "generatedApks": [
    {
      "certificateSha256Hash": "0123456789abcdef0123456789abcdef",
      "generatedSplitApks": [
        {"downloadId":"dl-split-base","moduleName":"base","variantId":3},
        {"downloadId":"dl-split-cfg","moduleName":"base","splitId":"config.xxhdpi","variantId":3}
      ],
      "generatedStandaloneApks": [{"downloadId":"dl-stand","variantId":5}],
      "generatedUniversalApk": {"downloadId":"dl-univ"},
      "generatedAssetPackSlices": [
        {"downloadId":"dl-asset","moduleName":"assets","sliceId":"assets-1","version":"7"}
      ],
      "generatedRecoveryModules": [
        {"downloadId":"dl-rec","moduleName":"base","recoveryId":"99","recoveryStatus":"RECOVERY_STATUS_ACTIVE"}
      ]
    }
  ]
}`

// TestList_noEdit_applicationScoped asserts the GET addresses the version code
// on the package axis (no /edits/) and parses every artifact family.
func TestList_noEdit_applicationScoped(t *testing.T) {
	var gotURL, gotMethod string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		gotMethod = r.Method
		return resp(200, listBody), nil
	})
	hc := &http.Client{Transport: rt}

	lr, raw, err := generatedapks.List(context.Background(), hc, "com.example.app", 142)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if !strings.HasSuffix(gotURL, "/applications/com.example.app/generatedApks/142") {
		t.Errorf("url %q is not the application-scoped generatedApks endpoint", gotURL)
	}
	if strings.Contains(gotURL, "/edits/") {
		t.Errorf("url %q must not open an Edit", gotURL)
	}
	if len(lr.GeneratedApks) != 1 {
		t.Fatalf("groups = %d, want 1", len(lr.GeneratedApks))
	}
	g := lr.GeneratedApks[0]
	if g.CertificateSha256Hash != "0123456789abcdef0123456789abcdef" {
		t.Errorf("cert = %q", g.CertificateSha256Hash)
	}
	if len(g.GeneratedSplitApks) != 2 || g.GeneratedSplitApks[1].SplitID != "config.xxhdpi" {
		t.Errorf("split apks = %+v", g.GeneratedSplitApks)
	}
	if len(g.GeneratedStandaloneApks) != 1 || g.GeneratedStandaloneApks[0].VariantID != 5 {
		t.Errorf("standalone apks = %+v", g.GeneratedStandaloneApks)
	}
	if g.GeneratedUniversalApk == nil || g.GeneratedUniversalApk.DownloadID != "dl-univ" {
		t.Errorf("universal = %+v", g.GeneratedUniversalApk)
	}
	if len(g.GeneratedAssetPackSlices) != 1 || g.GeneratedAssetPackSlices[0].SliceID != "assets-1" {
		t.Errorf("asset slices = %+v", g.GeneratedAssetPackSlices)
	}
	if len(g.GeneratedRecoveryModules) != 1 || g.GeneratedRecoveryModules[0].RecoveryID != "99" {
		t.Errorf("recovery modules = %+v", g.GeneratedRecoveryModules)
	}
	// ADR-0003: raw body is the verbatim response.
	if !strings.Contains(string(raw), `"dl-split-cfg"`) {
		t.Errorf("raw passthrough missing downloadId: %s", raw)
	}
}

// TestList_403_exit11 asserts a forbidden response maps to the authz exit code.
func TestList_403_exit11(t *testing.T) {
	rt := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return resp(403, `{"error":{"message":"caller does not have permission"}}`), nil
	})
	_, _, err := generatedapks.List(context.Background(), &http.Client{Transport: rt}, "com.example.app", 142)
	assertExit(t, err, 11)
}

// TestList_404_exit30 asserts an unknown version code maps to the not-found exit code.
func TestList_404_exit30(t *testing.T) {
	rt := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return resp(404, `{"error":{"message":"not found"}}`), nil
	})
	_, _, err := generatedapks.List(context.Background(), &http.Client{Transport: rt}, "com.example.app", 999)
	assertExit(t, err, 30)
}

// TestDownload_altMedia_streamsBytes_noEdit asserts the download GET carries
// alt=media, addresses the :download method (no /edits/), and streams the body
// to the writer verbatim.
func TestDownload_altMedia_streamsBytes_noEdit(t *testing.T) {
	var gotURL string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/octet-stream"}},
			Body:       io.NopCloser(strings.NewReader("PK\x03\x04rawapkbytes")),
		}, nil
	})
	var buf bytes.Buffer
	n, err := generatedapks.Download(context.Background(), &http.Client{Transport: rt}, "com.example.app", 142, "dl-abc", &buf)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !strings.Contains(gotURL, "/generatedApks/142/downloads/dl-abc:download") {
		t.Errorf("url %q is not the :download endpoint", gotURL)
	}
	if !strings.Contains(gotURL, "alt=media") {
		t.Errorf("url %q missing alt=media", gotURL)
	}
	if strings.Contains(gotURL, "/edits/") {
		t.Errorf("url %q must not open an Edit", gotURL)
	}
	if want := "PK\x03\x04rawapkbytes"; buf.String() != want {
		t.Errorf("body = %q, want %q", buf.String(), want)
	}
	if n != int64(len("PK\x03\x04rawapkbytes")) {
		t.Errorf("n = %d, want %d", n, len("PK\x03\x04rawapkbytes"))
	}
}

// TestDownload_403_exit11 / _404_exit30 cover the refusal/not-found paths.
func TestDownload_403_exit11(t *testing.T) {
	rt := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return resp(403, `{"error":{"message":"forbidden"}}`), nil
	})
	var buf bytes.Buffer
	_, err := generatedapks.Download(context.Background(), &http.Client{Transport: rt}, "com.example.app", 142, "dl", &buf)
	assertExit(t, err, 11)
	if buf.Len() != 0 {
		t.Errorf("error path wrote %d bytes, want 0", buf.Len())
	}
}

func TestDownload_404_exit30(t *testing.T) {
	rt := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return resp(404, `{"error":{"message":"unknown downloadId"}}`), nil
	})
	var buf bytes.Buffer
	_, err := generatedapks.Download(context.Background(), &http.Client{Transport: rt}, "com.example.app", 142, "bad", &buf)
	assertExit(t, err, 30)
}

func assertExit(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error with exit %d, got nil", want)
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err %v is not *api.Error", err)
	}
	if got := apiErr.ExitCode(); got != want {
		t.Errorf("exit = %d, want %d", got, want)
	}
}
