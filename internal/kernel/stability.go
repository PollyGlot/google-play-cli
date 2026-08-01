// stability.go — the per-command stability label that makes the v1.0 Public
// contract granular (ADR-0010). Without it a major release freezes everything
// at once, which forces the false choice the ADR rejects: delay 1.0 until every
// command is certain, or over-promise on the ones that are three days old.
//
// Like MarkMutating and WithScope, the whole mechanism is one cobra annotation
// declared in one place per command (at registration in cmd/gplay/main.go), so
// the policy stays a small readable registry rather than a check scattered
// through the business functions.
package kernel

import (
	"strings"

	"github.com/spf13/cobra"
)

// stabilityAnnotation is the cobra annotation key carrying a command's
// stability state. Absent = the command is part of the frozen Public contract
// (ADR-0010): its name, flags, semantics and exit codes cannot change without a
// major bump. "Frozen by default" is deliberate — forgetting to label a
// command over-promises rather than under-promises, which is the failure mode
// a user can actually plan around.
const stabilityAnnotation = "gplay.stability"

// StabilityExperimental is the annotation value for a shipped-but-not-frozen
// command. ADR-0010 also reserves a DEPRECATED: state for a compatibility path
// kept during a migration; it has no helper yet because nothing is deprecated.
const StabilityExperimental = "experimental"

// experimentalTag is the marker prefixed onto an experimental command's Short,
// so the label shows up in the parent's subcommand list — the place a CI author
// actually browses — and not only in the command's own help page.
const experimentalTag = "[experimental]"

// experimentalNotice is the paragraph prefixed onto an experimental command's
// Long, i.e. what its own `--help` opens with. It states the consequence
// ("may change without a major bump") rather than just the label, because
// "[experimental]" alone reads as a vague warning; the contract it opts out of
// is the actionable part.
// It is hard-wrapped like the hand-written Long texts around it: cobra prints
// Long verbatim, so an unwrapped paragraph would be the one ragged block on the
// page.
const experimentalNotice = experimentalTag + " Outside the Public contract: flags, output and behavior\n" +
	"may change in any release, without a major version bump. Pin an exact\n" +
	"release if you depend on this in CI.\n" +
	"See https://gplay.sh/docs/concepts/stability/"

// Experimental declares cmd — and every subcommand already registered under it
// — as shipped but outside the frozen Public contract, and returns it so it
// composes inline at registration like MarkMutating:
//
//	root.AddCommand(kernel.Experimental(subscriptionsGroup))
//
// Call it AFTER the group's children have been added: it walks the subtree it
// is given so a whole young namespace is labelled in one call, and each child
// carries the tag in its own help rather than only inheriting it silently.
// Children added afterwards are still covered — IsExperimental walks up the
// parent chain — they just miss the visible tag, which is why registration
// order matters for the UX and not for the semantics.
//
// The function is idempotent: labelling a child and then its parent does not
// stack two tags.
func Experimental(cmd *cobra.Command) *cobra.Command {
	if cmd == nil {
		return cmd
	}
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[stabilityAnnotation] = StabilityExperimental
	tagExperimental(cmd)
	for _, sub := range cmd.Commands() {
		Experimental(sub)
	}
	return cmd
}

// tagExperimental decorates cmd's help text with the label. Short gets the
// short marker (it is rendered as one line in the parent's command list); Long
// gets the full notice, since it is what the command's own --help prints. A
// command with no Long gets one synthesised from its Short, so the notice is
// never invisible just because the author wrote a one-liner.
func tagExperimental(cmd *cobra.Command) {
	if !strings.HasPrefix(cmd.Short, experimentalTag) {
		cmd.Short = strings.TrimSpace(experimentalTag + " " + cmd.Short)
	}
	if strings.HasPrefix(cmd.Long, experimentalNotice) {
		return
	}
	body := cmd.Long
	if body == "" {
		// Short already carries the tag prefix at this point; strip it so the
		// synthesised Long does not repeat the marker inside the notice.
		body = strings.TrimSpace(strings.TrimPrefix(cmd.Short, experimentalTag))
	}
	cmd.Long = experimentalNotice + "\n\n" + body
}

// IsExperimental reports whether cmd is outside the Public contract, either
// because it was labelled directly or because an ancestor namespace was. The
// parent walk is what lets one Experimental call cover a namespace whose
// children are registered later, and it means a subcommand can never be more
// stable than the namespace containing it.
func IsExperimental(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Annotations[stabilityAnnotation] == StabilityExperimental {
			return true
		}
	}
	return false
}
