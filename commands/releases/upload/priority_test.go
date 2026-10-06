package upload_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/upload"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// TestRun_updatePriority_sentOnTracksUpdate pins #664: --update-priority N
// lands as inAppUpdatePriority on the release the tracks.update PUT carries.
func TestRun_updatePriority_sentOnTracksUpdate(t *testing.T) {
	rt, transport := newUploadTransport(uploadAPI{editID: "edit-prio", versionCode: 142})
	rc, _ := newRC(t, transport)

	if _, err := upload.Run(rc, upload.Input{
		Package: "com.example.app", Track: "internal", AABPath: writeFakeAAB(t),
		UpdatePriority: 4, UpdatePrioritySet: true,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var body struct {
		Releases []map[string]json.RawMessage `json:"releases"`
	}
	if err := json.Unmarshal(trackUpdateReq(rt), &body); err != nil {
		t.Fatalf("decode tracks.update body: %v", err)
	}
	if len(body.Releases) != 1 || string(body.Releases[0]["inAppUpdatePriority"]) != "4" {
		t.Errorf("tracks.update body = %s, want one release with inAppUpdatePriority 4", trackUpdateReq(rt))
	}
}

// TestRun_noUpdatePriority_fieldAbsentFromTracksUpdate keeps today's payload
// byte-identical without the flag: no inAppUpdatePriority key at all (an
// explicit 0 would reset a priority, so absence and 0 must differ).
func TestRun_noUpdatePriority_fieldAbsentFromTracksUpdate(t *testing.T) {
	rt, transport := newUploadTransport(uploadAPI{editID: "edit-noprio", versionCode: 142})
	rc, _ := newRC(t, transport)

	if _, err := upload.Run(rc, upload.Input{Package: "com.example.app", Track: "internal", AABPath: writeFakeAAB(t)}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if bytes.Contains(trackUpdateReq(rt), []byte("inAppUpdatePriority")) {
		t.Errorf("tracks.update body = %s, want no inAppUpdatePriority without the flag", trackUpdateReq(rt))
	}
}

// TestRun_updatePriorityZero_isSentExplicitly: 0 is a legal priority (the
// lowest), distinct from "unset", so --update-priority 0 must reach the wire.
func TestRun_updatePriorityZero_isSentExplicitly(t *testing.T) {
	rt, transport := newUploadTransport(uploadAPI{editID: "edit-zero", versionCode: 142})
	rc, _ := newRC(t, transport)

	if _, err := upload.Run(rc, upload.Input{
		Package: "com.example.app", Track: "internal", AABPath: writeFakeAAB(t),
		UpdatePriority: 0, UpdatePrioritySet: true,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !bytes.Contains(trackUpdateReq(rt), []byte(`"inAppUpdatePriority":0`)) {
		t.Errorf("tracks.update body = %s, want inAppUpdatePriority 0", trackUpdateReq(rt))
	}
}

// TestRun_dryRun_updatePriority_golden: the --dry-run preview shows the
// priority the live call would send.
func TestRun_dryRun_updatePriority_golden(t *testing.T) {
	_, transport := newUploadTransport(uploadAPI{})
	rc, _ := newRC(t, transport)

	r, err := upload.Run(rc, upload.Input{
		Package: "com.example.app", Track: "internal", AABPath: writeFakeAAB(t), DryRun: true,
		UpdatePriority: 4, UpdatePrioritySet: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var got map[string]json.RawMessage
	var buf bytes.Buffer
	if err := r.Renderers().JSON(&buf); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode dry-run JSON: %v", err)
	}
	if string(got["inAppUpdatePriority"]) != "4" {
		t.Errorf("dry-run JSON = %s, want \"inAppUpdatePriority\": 4", buf.Bytes())
	}
}

// TestRun_updatePriorityOutOfRange_exit2_noHTTP: Google accepts 0..5 only;
// anything else is a usage error before any request, token exchange included.
func TestRun_updatePriorityOutOfRange_exit2_noHTTP(t *testing.T) {
	for _, p := range []int{6, -1} {
		rt, transport := newUploadTransport(uploadAPI{})
		rc, _ := newRC(t, transport)

		_, err := upload.Run(rc, upload.Input{
			Package: "com.example.app", Track: "internal", AABPath: writeFakeAAB(t),
			UpdatePriority: p, UpdatePrioritySet: true,
		})
		if got := exit.For(err); got != 2 {
			t.Errorf("--update-priority %d: exit.For(err) = %d, want 2; err=%v", p, got, err)
		}
		if touched(rt) {
			t.Errorf("--update-priority %d: RoundTripper saw calls on a usage error: %v", p, apiCalls(rt))
		}
	}
}

// TestNewCommand_registersUpdatePriorityFlag pins the cobra wiring.
func TestNewCommand_registersUpdatePriorityFlag(t *testing.T) {
	if upload.NewCommand(kernel.Boot{}).Flags().Lookup("update-priority") == nil {
		t.Error("releases upload is missing --update-priority")
	}
}
