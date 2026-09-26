// Migration proof for #518: sharing now takes its verb and its media-upload
// URL from internal/apiregistry instead of local literals. sharing_test.go is
// untouched; what is added here is the ABSOLUTE URL (the /upload/ host path,
// which is a genuinely different endpoint and not a suffix of the data plane)
// and the verb of both artifact kinds.
package sharing_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/testkit"

	"github.com/PollyGlot/google-play-cli/internal/play/sharing"
)

// pinned answers {} to every call and records what was sent.
func pinned() *testkit.Fake { return testkit.NewFake(testkit.Any(http.StatusOK, `{}`)) }

// first is the first call f recorded, or the zero Call when none was sent.
func first(f *testkit.Fake) testkit.Call {
	if calls := f.Calls(); len(calls) > 0 {
		return calls[0]
	}
	return testkit.Call{}
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
			rt := pinned()
			if err := tc.call(&http.Client{Transport: rt}); err != nil {
				t.Fatalf("call: %v", err)
			}
			if first(rt).URL != tc.want {
				t.Errorf("URL = %q, want %q", first(rt).URL, tc.want)
			}
			if first(rt).Method != http.MethodPost {
				t.Errorf("verb = %q, want POST", first(rt).Method)
			}
		})
	}
}
