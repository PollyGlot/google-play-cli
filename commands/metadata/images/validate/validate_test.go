// Package imagesvalidate_test exercises `gplay metadata images validate` at
// the kernel level. The command is fully OFFLINE (no auth, no network) so
// the RunContext carries no account and no transport; it reads the on-disk
// image tree and runs the pure rule engine.
package imagesvalidate_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"

	imagesvalidate "github.com/PollyGlot/google-play-cli/commands/metadata/images/validate"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

func pngOf(w, h int) []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)))
	return b.Bytes()
}

func newRC(t *testing.T) *kernel.RunContext {
	t.Helper()
	var stdout bytes.Buffer
	return kernel.NewForTest(context.Background(), kernel.Boot{Stdout: &stdout}, kernel.Inputs{Format: output.FormatJSON})
}

func writeTree(t *testing.T, tr imagetree.Tree) string {
	t.Helper()
	dir := t.TempDir()
	if err := imagetree.Write(dir, tr); err != nil {
		t.Fatalf("seed tree: %v", err)
	}
	return dir
}

func exitCodeOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err %v (%T) has no ExitCode()", err, err)
	}
	return coder.ExitCode()
}

// TestRun_validTree_passesOffline is the tracer bullet: a clean tree validates
// with no error and no auth/network (the RunContext has neither).
func TestRun_validTree_passesOffline(t *testing.T) {
	dir := writeTree(t, imagetree.Tree{
		"en-US": {
			images.Icon:             {pngOf(512, 512)},
			images.PhoneScreenshots: {pngOf(1080, 1920), pngOf(1080, 1920)},
		},
	})
	r, err := imagesvalidate.Run(newRC(t), imagesvalidate.Input{Dir: dir})
	if err != nil {
		t.Fatalf("Run on a valid tree: %v", err)
	}
	var buf bytes.Buffer
	if err := r.Renderers().JSON(&buf); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if !strings.Contains(buf.String(), `"ok": true`) {
		t.Errorf("valid tree JSON should report ok:true:\n%s", buf.String())
	}
}

// TestRun_badImage_isValidationError_exit20 asserts a rule violation (a
// 500×500 icon) fails with exit 20 and names the problem.
func TestRun_badImage_isValidationError_exit20(t *testing.T) {
	dir := writeTree(t, imagetree.Tree{"en-US": {images.Icon: {pngOf(500, 500)}}})
	_, err := imagesvalidate.Run(newRC(t), imagesvalidate.Input{Dir: dir})
	if got := exitCodeOf(t, err); got != 20 {
		t.Errorf("exit code = %d, want 20", got)
	}
	if !strings.Contains(err.Error(), "icon") || !strings.Contains(err.Error(), "512") {
		t.Errorf("error should name the icon dimensions rule: %v", err)
	}
}

// TestRun_missingDir_isExit20 asserts an unreadable --dir is a client-side
// validation failure (exit 20), like the text validate.
func TestRun_missingDir_isExit20(t *testing.T) {
	_, err := imagesvalidate.Run(newRC(t), imagesvalidate.Input{Dir: "/no/such/metadata/dir"})
	if got := exitCodeOf(t, err); got != 20 {
		t.Errorf("exit code = %d, want 20", got)
	}
}
