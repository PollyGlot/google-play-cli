package kernel

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

// Removal kinds, the `replacement.kind` vocabulary of the JSON error envelope
// (ADR-0049). Append-only.
const (
	RemovedCommand = "command"
	RemovedFlag    = "flag"
)

// Removal is one row of the removed-surface table (ADR-0049): a verb or flag
// gplay shipped, then removed without an alias, and what replaced it.
//
//   - Path is the command the removed token applied to, without the leading
//     "gplay": the group that held a removed verb ("games achievements"), or
//     the leaf that held a removed flag ("releases rollout").
//   - Token is what the user typed there: a verb, possibly several words when
//     a whole branch moved ("upload apk"), or a flag name ("--to").
//   - Use is the replacement: a command path without "gplay" for a verb, a
//     flag name on the same Path for a flag.
//   - RemovedIn is the gplay version that removed it.
//   - Note, optional, says what a pure rename would not (a domain verb, an
//     argument that moved).
type Removal struct {
	Kind      string
	Path      string
	Token     string
	Use       string
	RemovedIn string
	Note      string
}

// removedSurface is the table itself. Every rename or removal of a verb or flag
// adds its row in the same pull request (ADR-0049); TestRemovedSurface_table in
// cmd/gplay fails on a row whose Use no longer resolves or whose removed path
// resolves again. The human mirror is the website's migration guide.
var removedSurface = []Removal{
	// ADR-0019: one verb per intent.
	{Kind: RemovedCommand, Path: "apps", Token: "info", Use: "apps view", RemovedIn: "0.4.1"},
	{Kind: RemovedCommand, Path: "tracks", Token: "status", Use: "tracks view", RemovedIn: "0.4.1"},
	{Kind: RemovedCommand, Path: "team grants", Token: "revoke", Use: "team grants remove", RemovedIn: "0.4.1"},

	// ADR-0048 (#597): the 2.0.0 vocabulary pass.
	{Kind: RemovedCommand, Path: "games achievements", Token: "update", Use: "games achievements set", RemovedIn: "2.0.0"},
	{Kind: RemovedCommand, Path: "games achievements", Token: "delete", Use: "games achievements remove", RemovedIn: "2.0.0"},
	{Kind: RemovedCommand, Path: "games leaderboards", Token: "update", Use: "games leaderboards set", RemovedIn: "2.0.0"},
	{Kind: RemovedCommand, Path: "games leaderboards", Token: "delete", Use: "games leaderboards remove", RemovedIn: "2.0.0"},
	{
		Kind: RemovedCommand, Path: "appstore", Token: "update", Use: "appstore submit", RemovedIn: "2.0.0",
		Note: "submit is the domain verb for sending the app to review, not a renamed update",
	},
	{
		Kind: RemovedCommand, Path: "appstore publish-status", Token: "published", Use: "appstore publish-status set", RemovedIn: "2.0.0",
		Note: "the state is now the argument of set: `gplay appstore publish-status set published`",
	},
	{
		Kind: RemovedCommand, Path: "appstore publish-status", Token: "unpublished", Use: "appstore publish-status set", RemovedIn: "2.0.0",
		Note: "the state is now the argument of set: `gplay appstore publish-status set unpublished`",
	},
	{Kind: RemovedCommand, Path: "appstore", Token: "upload apk", Use: "appstore apk upload", RemovedIn: "2.0.0"},
	{Kind: RemovedCommand, Path: "appstore", Token: "upload image", Use: "appstore image upload", RemovedIn: "2.0.0"},
	{Kind: RemovedCommand, Path: "appstore", Token: "upload policy", Use: "appstore policy upload", RemovedIn: "2.0.0"},
}

// RemovedSurface returns a copy of the removed-surface table, for the
// integrity test that walks it against the live command tree.
func RemovedSurface() []Removal {
	return append([]Removal(nil), removedSurface...)
}

// removedVerb looks up the verb the user called on group: args are the
// positional tokens cobra left over after resolving group, so the removed path
// is group's path followed by a prefix of args. Exact match only (ADR-0049): a
// near miss is the Levenshtein suggestion's job, never the table's.
func removedVerb(group *cobra.Command, args []string) (Removal, bool) {
	path := strings.TrimPrefix(strings.TrimPrefix(group.CommandPath(), group.Root().Name()), " ")
	for _, r := range removedSurface {
		if r.Kind != RemovedCommand || r.Path != path {
			continue
		}
		words := strings.Fields(r.Token)
		if len(args) >= len(words) && strings.Join(args[:len(words)], " ") == r.Token {
			return r, true
		}
	}
	return Removal{}, false
}

// removedVerbError is the unknown-command error for a removed verb: the same
// first line every unknown command gets, then the replacement, in text for
// stderr and as a typed value for the JSON envelope.
func removedVerbError(group *cobra.Command, r Removal) *exit.UsageError {
	root := group.Root().Name()
	use := root + " " + r.Use
	msg := fmt.Sprintf("unknown command %q for %q: removed in %s, use `%s`",
		strings.Fields(r.Token)[0], group.CommandPath(), r.RemovedIn, use)
	if r.Note != "" {
		msg += " (" + r.Note + ")"
	}
	return &exit.UsageError{
		Msg: msg,
		Replacement: &exit.Replacement{
			Kind:      RemovedCommand,
			Removed:   group.CommandPath() + " " + r.Token,
			Use:       use,
			RemovedIn: r.RemovedIn,
		},
	}
}

// FlagErrorFunc is the root's cobra flag-error func, inherited by the whole
// tree: a flag-parse failure is CLI misuse (exit 2, docs/DESIGN.md §9), not
// the generic exit 1 a plain cobra error would map to.
//
// It also unmasks a removed verb. cobra resolves a command path before parsing
// flags, so in `gplay games achievements update <id> --output json` the removed
// `update` is not a child: cobra parses `--output` on the group, which has no
// such flag, and the user would read `unknown flag: --output` for a command
// that does take it. When the flags failed on a group whose leading positional
// arguments are a removed verb, the removed-verb error wins (ADR-0049).
func FlagErrorFunc(cmd *cobra.Command, err error) error {
	if cmd.HasSubCommands() {
		if r, ok := removedVerb(cmd, cmd.Flags().Args()); ok {
			return removedVerbError(cmd, r)
		}
	}
	return exit.Usagef("%s", err)
}
