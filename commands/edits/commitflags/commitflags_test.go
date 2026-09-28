package commitflags_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/edits/commitflags"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
)

func parse(t *testing.T, args ...string) (commitflags.Flags, error) {
	t.Helper()
	var f commitflags.Flags
	cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	commitflags.Register(cmd, &f)
	cmd.SetArgs(args)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	return f, err
}

func TestRegister_defaultsSendNothing(t *testing.T) {
	f, err := parse(t)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !f.Options().IsZero() {
		t.Errorf("unset flags = %+v, want the zero CommitOptions (Google's default)", f.Options())
	}
}

func TestRegister_parsesBothFlags(t *testing.T) {
	f, err := parse(t, "--changes-in-review", "error", "--changes-not-sent-for-review")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := edits.CommitOptions{ChangesInReview: edits.ChangesInReviewError, ChangesNotSentForReview: true}
	if f.Options() != want {
		t.Errorf("Options = %+v, want %+v", f.Options(), want)
	}
}

// A bad value fails at parse time, which the root's FlagErrorFunc turns into
// exit 2 before auth or any HTTP.
func TestRegister_rejectsUnknownValue(t *testing.T) {
	_, err := parse(t, "--changes-in-review", "ERROR_IF_IN_REVIEW")
	if err == nil || !strings.Contains(err.Error(), "must be cancel or error") {
		t.Fatalf("err = %v, want a parse error naming cancel and error", err)
	}
}

func TestFor_pinnedEditWarnsAndSendsNothing(t *testing.T) {
	var stderr bytes.Buffer
	rc := kernel.NewForTest(context.Background(), kernel.Boot{Stderr: &stderr}, kernel.Inputs{})
	f := commitflags.Flags{ChangesInReview: edits.ChangesInReviewError}

	if got := f.For(rc, "edit-pinned"); !got.IsZero() {
		t.Errorf("For(pinned) = %+v, want zero: the pinned Edit is not committed here", got)
	}
	msg := stderr.String()
	if !strings.HasPrefix(msg, "warning: ") || !strings.Contains(msg, "--changes-in-review") || !strings.Contains(msg, "gplay edits commit") {
		t.Errorf("stderr = %q, want a warning naming the flag and `gplay edits commit`", msg)
	}
}

func TestFor_implicitModeIsSilent(t *testing.T) {
	var stderr bytes.Buffer
	rc := kernel.NewForTest(context.Background(), kernel.Boot{Stderr: &stderr}, kernel.Inputs{})
	f := commitflags.Flags{ChangesNotSentForReview: true}

	if got := f.For(rc, ""); !got.ChangesNotSentForReview {
		t.Errorf("For(implicit) = %+v, want the flag forwarded", got)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want nothing in implicit mode", stderr.String())
	}
}

// Pinned but no flag passed: nothing to warn about.
func TestFor_pinnedWithoutFlagsIsSilent(t *testing.T) {
	var stderr bytes.Buffer
	rc := kernel.NewForTest(context.Background(), kernel.Boot{Stderr: &stderr}, kernel.Inputs{})
	(commitflags.Flags{}).For(rc, "edit-pinned")
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want nothing when no commit flag was passed", stderr.String())
	}
}
