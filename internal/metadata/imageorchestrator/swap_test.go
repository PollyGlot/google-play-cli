package imageorchestrator_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imageorchestrator"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/pathguard"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// applyWithSwap scans a tree holding a valid 512x512 icon that differs from
// the live one (a rewrite: deleteall, then upload), and runs swap while the
// fake answers the deleteall, so between the plan and the upload. It returns
// the uploads the fake received and Apply's error.
func applyWithSwap(t *testing.T, swap func(icon string)) (uploads int, err error) {
	t.Helper()
	dir := t.TempDir()
	imgDir := filepath.Join(dir, "en-US", "images")
	if err := os.MkdirAll(imgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	icon := filepath.Join(imgDir, "icon.png")
	if err := os.WriteFile(icon, pngOf(t, 512, 512), 0o644); err != nil {
		t.Fatal(err)
	}
	local, scanErr := imagetree.Scan(dir)
	if scanErr != nil {
		t.Fatal(scanErr)
	}

	swapped := false
	fake := testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return 200, `{"id":"e"}`, true
		case c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/listings/en-US/icon"):
			return 200, `{"images":[{"id":"old","sha256":"0000"}]}`, true
		case c.Method == http.MethodGet && strings.Contains(c.Path, "/listings/"):
			return 200, `{"images":[]}`, true
		case c.Method == http.MethodDelete && strings.HasSuffix(c.Path, "/listings/en-US/icon"):
			swap(icon)
			swapped = true
			return 200, `{"deleted":[]}`, true
		case c.Method == http.MethodPost && strings.HasPrefix(c.Path, "/upload/"):
			uploads++
			return 200, `{"image":{"id":"up","sha256":"s"}}`, true
		case c.Method == http.MethodDelete && strings.HasSuffix(c.Path, "/edits/e"):
			return 204, ``, true
		case strings.HasSuffix(c.Path, ":commit"):
			t.Error("the Edit was committed with a file changed after the plan")
			return 200, `{}`, true
		}
		t.Errorf("unexpected: %s %s", c.Method, c.Path)
		return 0, "", false
	})
	_, err = imageorchestrator.Apply(context.Background(), &http.Client{Transport: fake}, local, imageorchestrator.Opts{
		Package: "com.example.app", Dir: dir, Confirm: true,
	})
	if !swapped {
		t.Fatal("the fake never reached the deleteall, so nothing was swapped")
	}
	return uploads, err
}

// Finding 1: icon.png replaced by a symlink out of the tree after the scan.
// With the hatch closed the upload re-checks containment on the file it
// opens and refuses it: the outside file is never sent.
func TestApply_iconSwappedForExternalSymlinkAfterPlan_refused(t *testing.T) {
	t.Setenv(pathguard.EnvAllowExternalSymlinks, "")
	secret := filepath.Join(t.TempDir(), "secret.png")
	if err := os.WriteFile(secret, pngOf(t, 512, 512), 0o600); err != nil {
		t.Fatal(err)
	}
	uploads, err := applyWithSwap(t, func(icon string) {
		if err := os.Remove(icon); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(secret, icon); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	})
	if uploads != 0 {
		t.Fatalf("%d upload(s) of the file outside the tree were accepted", uploads)
	}
	if err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("want a containment refusal, got %v", err)
	}
	if got := exit.For(err); got != 2 {
		t.Errorf("exit = %d, want 2 (the scan-time refusal's code)", got)
	}
}

// Finding 2: the validated 512x512 icon rewritten as 1024x512 after the plan.
// The upload hashes what it read, finds it is not the planned content, and
// refuses before any byte of it is sent.
func TestApply_iconRewrittenAfterPlan_refusedAsChanged(t *testing.T) {
	uploads, err := applyWithSwap(t, func(icon string) {
		if err := os.WriteFile(icon, pngOf(t, 1024, 512), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	if uploads != 0 {
		t.Fatalf("%d upload(s) of the unvalidated 1024x512 icon were accepted", uploads)
	}
	var changed *imageorchestrator.ChangedError
	if !errors.As(err, &changed) || !strings.HasSuffix(changed.Path, "icon.png") {
		t.Fatalf("want a ChangedError naming icon.png, got %v", err)
	}
	if got := exit.For(err); got != 20 {
		t.Errorf("exit = %d, want 20", got)
	}
}
