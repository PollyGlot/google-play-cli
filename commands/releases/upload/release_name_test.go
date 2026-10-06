package upload_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/upload"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// TestRun_releaseName_sentOnTracksUpdate pins #665: --release-name names the
// release the tracks.update PUT carries.
func TestRun_releaseName_sentOnTracksUpdate(t *testing.T) {
	rt, transport := newUploadTransport(uploadAPI{editID: "edit-name", versionCode: 142})
	rc, _ := newRC(t, transport)

	if _, err := upload.Run(rc, upload.Input{
		Package: "com.example.app", Track: "internal", AABPath: writeFakeAAB(t), ReleaseName: "2.4.1",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !bytes.Contains(trackUpdateReq(rt), []byte(`"name":"2.4.1"`)) {
		t.Errorf("tracks.update body = %s, want name 2.4.1", trackUpdateReq(rt))
	}
}

// TestRun_dryRun_releaseName_previewed: the --dry-run preview shows the name
// the live call would send.
func TestRun_dryRun_releaseName_previewed(t *testing.T) {
	rt, transport := newUploadTransport(uploadAPI{})
	rc, _ := newRC(t, transport)

	r, err := upload.Run(rc, upload.Input{
		Package: "com.example.app", Track: "internal", AABPath: writeFakeAAB(t), DryRun: true, ReleaseName: "2.4.1",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var buf bytes.Buffer
	if err := r.Renderers().JSON(&buf); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode dry-run JSON: %v", err)
	}
	if string(got["releaseName"]) != `"2.4.1"` {
		t.Errorf("dry-run JSON = %s, want \"releaseName\": \"2.4.1\"", buf.Bytes())
	}
	if touched(rt) {
		t.Errorf("dry-run reached the transport: %v", apiCalls(rt))
	}
}

// TestRun_blankReleaseName_exit2_noHTTP: an empty or whitespace-only
// --release-name is a usage error before any request, token exchange
// included.
func TestRun_blankReleaseName_exit2_noHTTP(t *testing.T) {
	for _, name := range []string{"", "   ", "\t"} {
		rt, transport := newUploadTransport(uploadAPI{})
		rc, _ := newRC(t, transport)

		_, err := upload.Run(rc, upload.Input{
			Package: "com.example.app", Track: "internal", AABPath: writeFakeAAB(t),
			ReleaseName: name, ReleaseNameSet: true,
		})
		if got := exit.For(err); got != 2 {
			t.Errorf("--release-name %q: exit.For(err) = %d, want 2; err=%v", name, got, err)
		}
		if touched(rt) {
			t.Errorf("--release-name %q: RoundTripper saw calls on a usage error: %v", name, apiCalls(rt))
		}
	}
}

// TestNewCommand_registersReleaseNameFlag pins the cobra wiring.
func TestNewCommand_registersReleaseNameFlag(t *testing.T) {
	if upload.NewCommand(kernel.Boot{}).Flags().Lookup("release-name") == nil {
		t.Error("releases upload is missing --release-name")
	}
}
