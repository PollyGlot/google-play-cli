package commitflags

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
)

const (
	ChangesInReview         = "changes-in-review"
	ChangesNotSentForReview = "changes-not-sent-for-review"
)

type Flags struct {
	ChangesInReview         edits.ChangesInReview
	ChangesNotSentForReview bool
}

func Register(cmd *cobra.Command, f *Flags) {
	cmd.Flags().Var((*changesInReviewValue)(&f.ChangesInReview), ChangesInReview,
		"what the commit does when changes are already in review: cancel (cancel that review and submit everything again, Google's default when unset) or error (fail and leave the review untouched)")
	cmd.Flags().BoolVar(&f.ChangesNotSentForReview, ChangesNotSentForReview, false,
		"commit without sending the changes for review; they wait until sent from the Play Console")
}

func (f Flags) Options() edits.CommitOptions {
	return edits.CommitOptions{ChangesInReview: f.ChangesInReview, ChangesNotSentForReview: f.ChangesNotSentForReview}
}

func (f Flags) For(rc *kernel.RunContext, explicitEditID string) edits.CommitOptions {
	if explicitEditID == "" {
		return f.Options()
	}
	f.WarnStaged(rc, explicitEditID)
	return edits.CommitOptions{}
}

func (f Flags) WarnStaged(rc *kernel.RunContext, explicitEditID string) {
	if explicitEditID == "" || f.Options().IsZero() {
		return
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
}

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

func (v *changesInReviewValue) Type() string { return "cancel|error" }
