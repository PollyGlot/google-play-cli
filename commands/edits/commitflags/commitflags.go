// Package commitflags binds the two opt-in edits.commit parameters,
// --changes-in-review and --changes-not-sent-for-review, on every command that
// commits an Edit: `gplay edits commit` and each write command that commits its
// own implicit Edit. One registration keeps the names, the help text and the
// validation identical across the 15 leaves (#598).
//
// Both flags are additive opt-ins: left alone they send nothing, so the commit
// request and Google's default (cancel a pending review, resubmit everything)
// are unchanged. They ship [experimental] in their help text, the sub-feature
// label of docs/DESIGN.md §11, because the commands carrying them are frozen
// and a flag-level annotation does not exist.
package commitflags

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
)

// Flag names, exported so tests and hints spell them once.
const (
	ChangesInReview         = "changes-in-review"
	ChangesNotSentForReview = "changes-not-sent-for-review"
)

// Flags is what cobra fills from the two flags.
type Flags struct {
	ChangesInReview         edits.ChangesInReview
	ChangesNotSentForReview bool
}

// Register declares both flags on cmd, bound to f.
func Register(cmd *cobra.Command, f *Flags) {
	cmd.Flags().Var((*changesInReviewValue)(&f.ChangesInReview), ChangesInReview,
		"[experimental] what the commit does when changes are already in review: cancel (cancel that review and submit everything again, Google's default when unset) or error (fail and leave the review untouched)")
	cmd.Flags().BoolVar(&f.ChangesNotSentForReview, ChangesNotSentForReview, false,
		"[experimental] commit without sending the changes for review; they wait until sent from the Play Console")
}

// Options returns the commit parameters for an Edit this invocation commits
// itself (`edits commit`, or a write command in implicit mode).
func (f Flags) Options() edits.CommitOptions {
	return edits.CommitOptions{ChangesInReview: f.ChangesInReview, ChangesNotSentForReview: f.ChangesNotSentForReview}
}

// For returns the commit parameters for a write command. With an explicit Edit
// pinned the command stages into it and does not commit, so the flags cannot
// apply: a warning says where they belong rather than dropping them silently,
// since an ignored `--changes-in-review error` is exactly the review
// cancellation the caller asked to avoid.
func (f Flags) For(rc *kernel.RunContext, explicitEditID string) edits.CommitOptions {
	opts := f.Options()
	if explicitEditID == "" || opts.IsZero() {
		return opts
	}
	var set []string
	if f.ChangesInReview != edits.ChangesInReviewUnset {
		set = append(set, "--"+ChangesInReview)
	}
	if f.ChangesNotSentForReview {
		set = append(set, "--"+ChangesNotSentForReview)
	}
	rc.Warnf("%s ignored: this change is staged in open edit %s, which only `gplay edits commit` commits; pass the flag there",
		strings.Join(set, " and "), explicitEditID)
	return edits.CommitOptions{}
}

// changesInReviewValue validates --changes-in-review while flags are parsed,
// so a bad value is CLI misuse (exit 2) before auth, before any HTTP and on a
// --dry-run too, without a check in each command.
type changesInReviewValue edits.ChangesInReview

func (v *changesInReviewValue) String() string { return string(*v) }

func (v *changesInReviewValue) Set(s string) error {
	parsed, err := edits.ParseChangesInReview(s)
	if err != nil {
		return err
	}
	*v = changesInReviewValue(parsed)
	return nil
}

// Type names the accepted values, so help renders
// `--changes-in-review cancel|error`. It is deliberately not "string": the
// flag-type registry test in cmd/gplay samples every "string" flag with an
// arbitrary value, which this one rightly refuses.
func (v *changesInReviewValue) Type() string { return "cancel|error" }
