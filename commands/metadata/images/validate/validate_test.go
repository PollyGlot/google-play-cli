package imagesvalidate_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	imagesvalidate "github.com/PollyGlot/google-play-cli/commands/metadata/images/validate"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

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

func TestRun_validTree_passesOffline(t *testing.T) {
	dir := writeTree(t, imagetree.Tree{
		"en-US": {
			images.Icon:             {testkit.PNG(512, 512)},
			images.PhoneScreenshots: {testkit.PNG(1080, 1920), testkit.PNG(1080, 1920)},
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

func TestRun_badImage_isValidationError_exit20(t *testing.T) {
	dir := writeTree(t, imagetree.Tree{"en-US": {images.Icon: {testkit.PNG(500, 500)}}})
	_, err := imagesvalidate.Run(newRC(t), imagesvalidate.Input{Dir: dir})
	if got := exitCodeOf(t, err); got != 20 {
		t.Errorf("exit code = %d, want 20", got)
	}
	if !strings.Contains(err.Error(), "icon") || !strings.Contains(err.Error(), "512") {
		t.Errorf("error should name the icon dimensions rule: %v", err)
	}
}

func TestRun_missingDir_isExit20(t *testing.T) {
	_, err := imagesvalidate.Run(newRC(t), imagesvalidate.Input{Dir: "/no/such/metadata/dir"})
	if got := exitCodeOf(t, err); got != 20 {
		t.Errorf("exit code = %d, want 20", got)
	}
}

func TestRun_retiredPromoGraphic_warnsAndPasses(t *testing.T) {
	dir := writeTree(t, imagetree.Tree{"en-US": {images.Icon: {testkit.PNG(512, 512)}}})
	promo := filepath.Join(dir, "en-US", "images", "promoGraphic.png")
	if err := os.WriteFile(promo, testkit.PNG(180, 120), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	rc := kernel.NewForTest(context.Background(), kernel.Boot{Stdout: &stdout, Stderr: &stderr}, kernel.Inputs{Format: output.FormatJSON})

	if _, err := imagesvalidate.Run(rc, imagesvalidate.Input{Dir: dir}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := stderr.String(); !strings.Contains(got, "warning: "+promo+": not an image type Play accepts") {
		t.Errorf("stderr = %q, want a warning naming %s", got, promo)
	}
}
