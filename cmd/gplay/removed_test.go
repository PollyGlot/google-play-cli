package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// TestRemovedVerb_namesReplacement is #688: a verb 2.0.0 removed still fails as
// CLI misuse (exit 2, USAGE_ERROR), but the message and the JSON envelope name
// the verb that replaced it, so a 1.x script or an agent can recover alone.
func TestRemovedVerb_namesReplacement(t *testing.T) {
	t.Setenv(output.EnvDefaultOutput, "")
	stdout, code, err := runLikeMain(t, "games", "achievements", "update", "x")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; err=%v", code, err)
	}
	want := "unknown command \"update\" for \"gplay games achievements\": removed in 2.0.0, use `gplay games achievements set`"
	if err.Error() != want {
		t.Errorf("message = %q\nwant      %q", err.Error(), want)
	}
	d := envelopeOf(t, stdout)
	if d.Code != string(exit.CodeUsageError) || d.ExitCode != 2 {
		t.Errorf("envelope code/exitCode = %s/%d, want USAGE_ERROR/2", d.Code, d.ExitCode)
	}
	if d.Replacement == nil {
		t.Fatalf("envelope has no replacement:\n%s", stdout)
	}
	got := *d.Replacement
	wantR := output.Replacement{Kind: "command", Removed: "gplay games achievements update", Use: "gplay games achievements set", RemovedIn: "2.0.0"}
	if got != wantR {
		t.Errorf("replacement = %+v, want %+v", got, wantR)
	}
}

// TestRemovedVerb_beforeAGroupUnknownFlag covers the group-parses-flags-first
// quirk: `update` is not a child, so cobra parses `--output` on the group,
// which has no such flag. The removed verb, not the flag, is the real cause.
func TestRemovedVerb_beforeAGroupUnknownFlag(t *testing.T) {
	t.Setenv(output.EnvDefaultOutput, "")
	stdout, code, err := runLikeMain(t, "games", "achievements", "update", "x", "--output", "json")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; err=%v", code, err)
	}
	if !strings.Contains(err.Error(), "removed in 2.0.0, use `gplay games achievements set`") {
		t.Errorf("message = %q, want the removed-verb hint", err.Error())
	}
	if d := envelopeOf(t, stdout); d.Replacement == nil || d.Replacement.Use != "gplay games achievements set" {
		t.Errorf("envelope replacement = %+v, want use gplay games achievements set", d.Replacement)
	}
}

// TestRemovedVerb_afterAGroupUnknownFlag: the same quirk with the flag typed
// before the removed verb. pflag stops on `--output` before it reaches the
// verb, so the hint comes from the raw argv that follows the group's path,
// read the way cobra resolved that path (a known valued flag and an unknown
// one both consume the next token).
func TestRemovedVerb_afterAGroupUnknownFlag(t *testing.T) {
	t.Setenv(output.EnvDefaultOutput, "")
	for _, tc := range []struct {
		argv []string
		use  string
	}{
		{[]string{"games", "achievements", "--output", "json", "update", "x"}, "gplay games achievements set"},
		{[]string{"games", "achievements", "--output=json", "update", "x"}, "gplay games achievements set"},
		{[]string{"appstore", "upload", "--output", "json", "apk", "app.apk"}, "gplay appstore apk upload"},
		{[]string{"--account", "ci", "games", "--output", "json", "achievements", "update", "x"}, "gplay games achievements set"},
	} {
		stdout, code, err := runLikeMain(t, tc.argv...)
		if code != 2 {
			t.Fatalf("%v: exit code = %d, want 2; err=%v", tc.argv, code, err)
		}
		if !strings.Contains(err.Error(), "use `"+tc.use+"`") {
			t.Errorf("%v: message = %q, want the removed-verb hint", tc.argv, err.Error())
		}
		if d := envelopeOf(t, stdout); d.Replacement == nil || d.Replacement.Use != tc.use {
			t.Errorf("%v: envelope replacement = %+v, want use %s", tc.argv, d.Replacement, tc.use)
		}
	}

	// No false positive: the value of a flag is not a verb, and an unknown flag
	// before a live verb or a never-shipped one keeps pflag's own message.
	for _, argv := range [][]string{
		{"games", "achievements", "--output", "update", "x"},
		{"games", "achievements", "--output", "json", "bogus"},
		{"apps", "--bogus", "json"},
	} {
		stdout, code, err := runLikeMain(t, argv...)
		if code != 2 {
			t.Fatalf("%v: exit code = %d, want 2; err=%v", argv, code, err)
		}
		if strings.Contains(err.Error(), "removed in") || strings.Contains(stdout, "replacement") {
			t.Errorf("%v: err = %q, stdout = %s, want no replacement", argv, err.Error(), stdout)
		}
	}
}

// TestRemovedVerb_precedenceOverDidYouMean: on an exact removed path the table
// replaces the Levenshtein suggestion (`apps info` used to suggest `init`); a
// live typo keeps the suggestion and gets no replacement.
func TestRemovedVerb_precedenceOverDidYouMean(t *testing.T) {
	t.Setenv(output.EnvDefaultOutput, "")

	stdout, code, err := runLikeMain(t, "apps", "info")
	if code != 2 {
		t.Fatalf("apps info: exit code = %d, want 2; err=%v", code, err)
	}
	if !strings.Contains(err.Error(), "removed in 0.4.1, use `gplay apps view`") {
		t.Errorf("apps info: message = %q, want the apps view hint", err.Error())
	}
	if strings.Contains(err.Error(), "Did you mean") {
		t.Errorf("apps info: message = %q, still carries a did-you-mean", err.Error())
	}
	if d := envelopeOf(t, stdout); d.Replacement == nil || d.Replacement.Use != "gplay apps view" {
		t.Errorf("apps info: envelope replacement = %+v", d.Replacement)
	}

	stdout, code, err = runLikeMain(t, "apps", "veiw")
	if code != 2 {
		t.Fatalf("apps veiw: exit code = %d, want 2; err=%v", code, err)
	}
	if !strings.Contains(err.Error(), "Did you mean this?\n    view") || strings.Contains(err.Error(), "removed in") {
		t.Errorf("apps veiw: message = %q, want the did-you-mean and no removal", err.Error())
	}
	// Absent, not empty: the field is omitempty on an unmatched usage error.
	if strings.Contains(stdout, "replacement") {
		t.Errorf("apps veiw: envelope carries a replacement key:\n%s", stdout)
	}
}

// TestRemovedVerb_carriesItsNote: appstore update became the domain verb
// submit, not a pure rename, and the message says so.
func TestRemovedVerb_carriesItsNote(t *testing.T) {
	t.Setenv(output.EnvDefaultOutput, "table")
	stdout, code, err := runLikeMain(t, "appstore", "update")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; err=%v", code, err)
	}
	want := "unknown command \"update\" for \"gplay appstore\": removed in 2.0.0, use `gplay appstore submit` (submit is the domain verb for sending the app to review, not a renamed update)"
	if err.Error() != want {
		t.Errorf("message = %q\nwant      %q", err.Error(), want)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty under table", stdout)
	}
}

// TestRemovedVerb_keepsItsArgument: `appstore publish-status published` became
// `set published`, so `use` carries the argument the removed verb held: an
// agent that substitutes `removed` by `use` in its command keeps the state.
// The note, which says why, reaches the envelope too.
func TestRemovedVerb_keepsItsArgument(t *testing.T) {
	t.Setenv(output.EnvDefaultOutput, "")
	stdout, code, err := runLikeMain(t, "appstore", "publish-status", "published", "--store-package", "x", "--output", "json")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; err=%v", code, err)
	}
	want := "unknown command \"published\" for \"gplay appstore publish-status\": removed in 2.0.0, use `gplay appstore publish-status set published` (the state is now the argument of set)"
	if err.Error() != want {
		t.Errorf("message = %q\nwant      %q", err.Error(), want)
	}
	d := envelopeOf(t, stdout)
	if d.Replacement == nil {
		t.Fatalf("envelope has no replacement:\n%s", stdout)
	}
	wantR := output.Replacement{
		Kind: "command", Removed: "gplay appstore publish-status published", Use: "gplay appstore publish-status set published",
		RemovedIn: "2.0.0", Note: "the state is now the argument of set",
	}
	if *d.Replacement != wantR {
		t.Errorf("replacement = %+v, want %+v", *d.Replacement, wantR)
	}
}

// TestRemovedFlag_carriesItsNote: a flag row's note reaches the envelope.
func TestRemovedFlag_carriesItsNote(t *testing.T) {
	t.Setenv(output.EnvDefaultOutput, "")
	stdout, code, err := runLikeMain(t, "subscriptions", "prices", "migrate", "--region", "US")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; err=%v", code, err)
	}
	d := envelopeOf(t, stdout)
	if d.Replacement == nil || d.Replacement.Use != "--regions" || d.Replacement.Note != "--regions takes a comma-separated list or a repeated flag: `--regions US,CA`" {
		t.Errorf("envelope replacement = %+v, want use --regions with its note", d.Replacement)
	}
}

// TestRemovedVerb_movedBranch: a verb whose whole branch moved
// (`appstore upload apk` -> `appstore apk upload`) matches on its full path.
func TestRemovedVerb_movedBranch(t *testing.T) {
	t.Setenv(output.EnvDefaultOutput, "")
	stdout, code, err := runLikeMain(t, "appstore", "upload", "apk", "app.apk")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; err=%v", code, err)
	}
	if !strings.Contains(err.Error(), "removed in 2.0.0, use `gplay appstore apk upload`") {
		t.Errorf("message = %q", err.Error())
	}
	d := envelopeOf(t, stdout)
	if d.Replacement == nil || d.Replacement.Removed != "gplay appstore upload apk" {
		t.Errorf("envelope replacement = %+v, want removed gplay appstore upload apk", d.Replacement)
	}
}

// TestRemovedSurface_table keeps the removed-surface table honest against the
// live command tree (ADR-0051); removedSurfaceProblems holds the rules.
func TestRemovedSurface_table(t *testing.T) {
	for _, p := range removedSurfaceProblems(newRootCmd(kernel.Boot{}), kernel.RemovedSurface()) {
		t.Error(p)
	}
}

// TestRemovedSurface_tableCheck proves the integrity rules on a synthetic tree:
// a row is accepted at the root, with declared trailing arguments its
// replacement takes, and refused when its removed path resolves through an
// alias or its trailing arguments do not fit the replacement.
func TestRemovedSurface_tableCheck(t *testing.T) {
	tree := func(viewAliases ...string) *cobra.Command {
		root := &cobra.Command{Use: "gplay", Args: cobra.ArbitraryArgs, RunE: kernel.GroupRunE}
		apps := &cobra.Command{Use: "apps", Args: cobra.ArbitraryArgs, RunE: kernel.GroupRunE}
		noop := func(*cobra.Command, []string) error { return nil }
		apps.AddCommand(
			&cobra.Command{Use: "view", Aliases: viewAliases, Args: cobra.NoArgs, RunE: noop},
			&cobra.Command{Use: "set <state>", Args: cobra.ExactArgs(1), RunE: noop},
		)
		root.AddCommand(apps)
		return root
	}
	for _, tc := range []struct {
		name    string
		root    *cobra.Command
		row     kernel.Removal
		problem string // "" when the row must be accepted
	}{
		{"plain rename", tree(), kernel.Removal{Kind: kernel.RemovedCommand, Path: "apps", Token: "info", Use: "apps view", RemovedIn: "1.0.0"}, ""},
		{"row at the root", tree(), kernel.Removal{Kind: kernel.RemovedCommand, Path: "", Token: "info", Use: "apps view", RemovedIn: "1.0.0"}, ""},
		{"declared trailing argument", tree(), kernel.Removal{Kind: kernel.RemovedCommand, Path: "apps", Token: "published", Use: "apps set", UseArgs: "published", RemovedIn: "1.0.0"}, ""},
		{"removed path is a live alias", tree("info"), kernel.Removal{Kind: kernel.RemovedCommand, Path: "apps", Token: "info", Use: "apps view", RemovedIn: "1.0.0"}, "removed path still resolves"},
		{"trailing argument the leaf refuses", tree(), kernel.Removal{Kind: kernel.RemovedCommand, Path: "apps", Token: "info", Use: "apps view", UseArgs: "x", RemovedIn: "1.0.0"}, "does not accept"},
	} {
		got := strings.Join(removedSurfaceProblems(tc.root, []kernel.Removal{tc.row}), "\n")
		if tc.problem == "" && got != "" {
			t.Errorf("%s: problems = %q, want none", tc.name, got)
		}
		if tc.problem != "" && !strings.Contains(got, tc.problem) {
			t.Errorf("%s: problems = %q, want %q", tc.name, got, tc.problem)
		}
	}
}

// removedSurfaceProblems walks rows against root's command tree: every
// replacement resolves to a runnable leaf that accepts the row's declared
// trailing arguments, when it has some, (a flag of the leaf, for a flag row), every removed path
// or flag resolves to nothing, by name or by alias (one that still exists is a
// table bug: the hint would shadow a live command or flag, or never fire), and
// the group or leaf a row hangs off still exists (the root included), or the
// lookup could never run.
func removedSurfaceProblems(root *cobra.Command, rows []kernel.Removal) []string {
	var problems []string
	fail := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }
	// resolves reports whether path names a command of the tree by its
	// canonical name, consuming every word (cobra's Find stops at the first
	// unknown word and returns the deepest command it reached).
	resolves := func(path string) (*cobra.Command, bool) {
		c, _, err := root.Find(strings.Fields(path))
		if err != nil || c.CommandPath() != strings.TrimSpace(root.Name()+" "+path) {
			return nil, false
		}
		return c, true
	}
	// reaches reports whether cobra would run some command for path, aliases
	// included: Find consumed every word.
	reaches := func(path string) bool {
		_, rest, err := root.Find(strings.Fields(path))
		return err == nil && len(rest) == 0
	}
	seen := map[string]bool{}
	for _, r := range rows {
		key := r.Kind + "|" + r.Path + "|" + r.Token
		if seen[key] {
			fail("duplicate row %q", key)
		}
		seen[key] = true
		if r.RemovedIn == "" {
			fail("row %q: empty RemovedIn", key)
		}
		switch r.Kind {
		case kernel.RemovedCommand:
			if g, ok := resolves(r.Path); !ok || !g.HasSubCommands() {
				fail("row %q: %q is not a live group", key, r.Path)
			}
			if reaches(r.Path + " " + r.Token) {
				fail("row %q: removed path still resolves", key)
			}
			u, ok := resolves(r.Use)
			if !ok || u.HasSubCommands() || !u.Runnable() {
				fail("row %q: replacement %q is not a live leaf", key, r.Use)
				continue
			}
			// Declared arguments alone must fit the leaf: the user's own
			// arguments after the removed verb (an id, a file) follow them.
			if r.UseArgs == "" {
				continue
			}
			if err := u.ValidateArgs(strings.Fields(r.UseArgs)); err != nil {
				fail("row %q: replacement %q does not accept arguments %q: %v", key, r.Use, r.UseArgs, err)
			}
		case kernel.RemovedFlag:
			// A flag row hangs off the leaf that lost the flag: Use must be a
			// flag that leaf takes (its own or inherited), Token must not be,
			// or the hint would shadow a live flag.
			leaf, ok := resolves(r.Path)
			if !ok || leaf.HasSubCommands() || !leaf.Runnable() {
				fail("row %q: %q is not a live leaf", key, r.Path)
				continue
			}
			if r.UseArgs != "" {
				fail("row %q: a flag row takes no trailing arguments", key)
			}
			flagName := func(token string) (string, bool) {
				name, ok := strings.CutPrefix(token, "--")
				return name, ok && name != ""
			}
			if name, ok := flagName(r.Token); !ok {
				fail("row %q: removed %q is not a --long flag", key, r.Token)
			} else if leaf.Flag(name) != nil {
				fail("row %q: removed flag still exists on %q", key, r.Path)
			}
			if name, ok := flagName(r.Use); !ok || leaf.Flag(name) == nil {
				fail("row %q: replacement %q is not a flag of %q", key, r.Use, r.Path)
			}
		default:
			fail("row %q: kind %q has no integrity check", key, r.Kind)
		}
	}
	return problems
}

// TestRemovedFlag_namesReplacement is #689: a flag 2.0.0 removed from a leaf
// fails as before (exit 2, USAGE_ERROR, `unknown flag: --to`), then names the
// flag that replaced it on that leaf, in text and in the JSON envelope. Both
// spellings cobra accepts for a valued flag reach the same hint.
func TestRemovedFlag_namesReplacement(t *testing.T) {
	t.Setenv(output.EnvDefaultOutput, "")
	for _, argv := range [][]string{
		{"releases", "rollout", "--track", "production", "--to", "0.1"},
		{"releases", "rollout", "--track", "production", "--to=0.1"},
	} {
		stdout, code, err := runLikeMain(t, argv...)
		if code != 2 {
			t.Fatalf("%v: exit code = %d, want 2; err=%v", argv, code, err)
		}
		want := "unknown flag: --to: removed in 2.0.0, use `gplay releases rollout --staged`"
		if err.Error() != want {
			t.Errorf("%v: message = %q\nwant      %q", argv, err.Error(), want)
		}
		d := envelopeOf(t, stdout)
		if d.Code != string(exit.CodeUsageError) || d.ExitCode != 2 {
			t.Errorf("%v: envelope code/exitCode = %s/%d, want USAGE_ERROR/2", argv, d.Code, d.ExitCode)
		}
		if d.Replacement == nil {
			t.Fatalf("%v: envelope has no replacement:\n%s", argv, stdout)
		}
		wantR := output.Replacement{Kind: "flag", Removed: "--to", Use: "--staged", RemovedIn: "2.0.0"}
		if *d.Replacement != wantR {
			t.Errorf("%v: replacement = %+v, want %+v", argv, *d.Replacement, wantR)
		}
	}
}

// TestRemovedFlag_scopedToItsLeaf: a flag row applies to the leaf that lost the
// flag only. `--to` is still a live flag of `releases promote`, and on a leaf
// that never had it, or for a flag gplay never shipped, the message stays
// pflag's own, with no replacement in the envelope.
func TestRemovedFlag_scopedToItsLeaf(t *testing.T) {
	t.Setenv(output.EnvDefaultOutput, "")

	_, _, err := runLikeMain(t, "releases", "promote", "--from", "internal", "--to", "production")
	if err != nil && (strings.Contains(err.Error(), "unknown flag") || strings.Contains(err.Error(), "removed in")) {
		t.Errorf("releases promote --to: err = %q, want --to accepted", err.Error())
	}

	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"releases", "rollout", "--bogus"}, "unknown flag: --bogus"},
		{[]string{"releases", "halt", "--to", "0.1"}, "unknown flag: --to"},
		{[]string{"vitals", "crashes", "-x"}, "unknown shorthand flag: 'x' in -x"},
	} {
		stdout, code, err := runLikeMain(t, tc.argv...)
		if code != 2 {
			t.Fatalf("%v: exit code = %d, want 2; err=%v", tc.argv, code, err)
		}
		if err.Error() != tc.want {
			t.Errorf("%v: message = %q, want %q unchanged", tc.argv, err.Error(), tc.want)
		}
		if strings.Contains(stdout, "replacement") {
			t.Errorf("%v: envelope carries a replacement key:\n%s", tc.argv, stdout)
		}
	}
}

// TestRemovedFlag_perLeafRename: the vitals `--version` flag became
// `--version-code` on each of the eight vitals leaves, nested ones included.
func TestRemovedFlag_perLeafRename(t *testing.T) {
	t.Setenv(output.EnvDefaultOutput, "")
	for _, path := range []string{"vitals crashes", "vitals errors counts"} {
		argv := append(strings.Fields(path), "--version", "3")
		stdout, code, err := runLikeMain(t, argv...)
		if code != 2 {
			t.Fatalf("%v: exit code = %d, want 2; err=%v", argv, code, err)
		}
		want := "unknown flag: --version: removed in 2.0.0, use `gplay " + path + " --version-code`"
		if err.Error() != want {
			t.Errorf("%v: message = %q\nwant      %q", argv, err.Error(), want)
		}
		wantR := output.Replacement{Kind: "flag", Removed: "--version", Use: "--version-code", RemovedIn: "2.0.0"}
		if d := envelopeOf(t, stdout); d.Replacement == nil || *d.Replacement != wantR {
			t.Errorf("%v: envelope replacement = %+v, want %+v", argv, d.Replacement, wantR)
		}
	}
}
