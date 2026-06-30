package kernel_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// TestConfirmMutation_implicitVsExplicit pins the DESIGN §8 invariant: the
// committed ✓ appears only in implicit mode. In explicit mode the change is
// staged into a user-owned open Edit (not yet published), so the line must NOT
// carry ✓ and must point at `gplay edits commit`.
func TestConfirmMutation_implicitVsExplicit(t *testing.T) {
	t.Run("implicit prints the committed checkmark", func(t *testing.T) {
		var stderr bytes.Buffer
		rc := kernel.NewForTest(context.Background(), kernel.Boot{Stderr: &stderr}, kernel.Inputs{Format: output.FormatJSON})
		rc.ConfirmMutation("", "uploaded versionCode %d", 7)
		out := stderr.String()
		if !strings.HasPrefix(out, "✓ ") {
			t.Errorf("implicit mode should print the committed ✓; got %q", out)
		}
		if !strings.Contains(out, "uploaded versionCode 7") {
			t.Errorf("message body missing; got %q", out)
		}
	})

	t.Run("explicit prints a non-checkmark staged line", func(t *testing.T) {
		var stderr bytes.Buffer
		rc := kernel.NewForTest(context.Background(), kernel.Boot{Stderr: &stderr}, kernel.Inputs{Format: output.FormatJSON})
		rc.ConfirmMutation("edit-42", "uploaded versionCode %d", 7)
		out := stderr.String()
		if strings.Contains(out, "✓") {
			t.Errorf("explicit mode must NOT print ✓ (nothing is committed yet); got %q", out)
		}
		if !strings.Contains(out, "edit-42") || !strings.Contains(out, "gplay edits commit") {
			t.Errorf("explicit line should name the open edit and point at commit; got %q", out)
		}
		if !strings.Contains(out, "uploaded versionCode 7") {
			t.Errorf("message body missing; got %q", out)
		}
	})
}
