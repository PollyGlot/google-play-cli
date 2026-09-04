// Migration proof for #518: sharing now takes its verb and its media-upload
// URL from internal/apiregistry instead of local literals. sharing_test.go is
// untouched; what is added here is the ABSOLUTE URL (the /upload/ host path,
// which is a genuinely different endpoint and not a suffix of the data plane)
// and the verb of both artifact kinds.
package sharing_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/sharing"
)

type pinRT struct {
	url, verb string
}

func (r *pinRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if r.url == "" {
		r.url, r.verb = req.URL.String(), req.Method
	}
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{}`)),
	}, nil
}

func TestResolvedUploadURLsUnchanged(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "app.bin")
	if err := os.WriteFile(artifact, []byte("payload"), 0o600); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	const base = "https://androidpublisher.googleapis.com/upload/androidpublisher/v3/applications/internalappsharing/com.example.app/artifacts/"

	cases := []struct {
		name string
		call func(*http.Client) error
		want string
	}{
		{
			name: "uploadapk",
			call: func(hc *http.Client) error {
				_, _, err := sharing.UploadAPK(context.Background(), hc, "com.example.app", artifact)
				return err
			},
			want: base + "apk?uploadType=media",
		},
		{
			name: "uploadbundle",
			call: func(hc *http.Client) error {
				_, _, err := sharing.UploadBundle(context.Background(), hc, "com.example.app", artifact)
				return err
			},
			want: base + "bundle?uploadType=media",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := &pinRT{}
			if err := tc.call(&http.Client{Transport: rt}); err != nil {
				t.Fatalf("call: %v", err)
			}
			if rt.url != tc.want {
				t.Errorf("URL = %q, want %q", rt.url, tc.want)
			}
			if rt.verb != http.MethodPost {
				t.Errorf("verb = %q, want POST", rt.verb)
			}
		})
	}
}
