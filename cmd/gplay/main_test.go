package main

import (
	"bytes"
	"io"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// TestVitalsLeavesAreReportingScoped is the end-to-end least-privilege guard for
// PRD #49: EVERY leaf command under `gplay vitals` (query, the seven presets,
// errors counts/issues/reports, anomalies) must request the playdeveloperreporting
// scope via kernel.WithScope. A dropped wrapper anywhere in the registration
// (main.go or errorscmd.NewCommand) makes that leaf silently fall back to the
// androidpublisher scope — this walks the real command tree and catches it.
func TestVitalsLeavesAreReportingScoped(t *testing.T) {
	root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})

	var vitals *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "vitals" {
			vitals = c
			break
		}
	}
	if vitals == nil {
		t.Fatal("no `vitals` command registered")
	}

	leaves := 0
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		kids := c.Commands()
		if len(kids) == 0 {
			leaves++
			if got := kernel.ScopeFor(c); got != token.ReportingScope {
				t.Errorf("leaf %q scope = %q, want the reporting scope", c.CommandPath(), got)
			}
			return
		}
		for _, k := range kids {
			walk(k)
		}
	}
	walk(vitals)

	// query + 7 presets + errors{counts,issues,reports} + anomalies = 12.
	if leaves < 12 {
		t.Errorf("walked %d vitals leaves, want >= 12 (did the tree shrink?)", leaves)
	}
}

// TestVerbVocabulary_canonicalNames asserts the ADR-0019 verb renames at
// the user-facing command-tree level: the canonical names resolve to a
// command, and the pre-rename names are gone (a hard rename leaves no
// alias, so the old verb is not a registered subcommand). This is the
// contract guard for the verb audit (#98) — one e2e case per rename,
// exercised through the real cobra tree via Command.Find, which resolves
// commands without executing them or touching auth/network.
//
// Pre-rename verbs appear here only as SPLIT path args, never as a
// contiguous phrase, so the repo-wide verb gate (#168) stays green on this
// file. The subtest name is the path joined at runtime, not source text.
func TestVerbVocabulary_canonicalNames(t *testing.T) {
	cases := []struct {
		path     []string
		wantGone bool // true: the old name must NOT resolve (hard-renamed away)
	}{
		{[]string{"apps", "view"}, false}, // #163 (replaced apps + "info")
		{[]string{"apps", "info"}, true},
		{[]string{"tracks", "view"}, false}, // #164 (replaced tracks + "status")
		{[]string{"tracks", "status"}, true},
		{[]string{"team", "grants", "remove"}, false}, // #165 (replaced grants + "revoke")
		{[]string{"team", "grants", "revoke"}, true},
		{[]string{"apps", "details", "view"}, false},        // #166 (read now carries a verb)
		{[]string{"apps", "details", "set"}, false},         // write unchanged
		{[]string{"apps", "details"}, false},                // group still resolves (bare → help)
		{[]string{"tracks", "availability", "view"}, false}, // #167 (read now carries a verb)
		{[]string{"tracks", "availability"}, false},         // group still resolves (bare → help)
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.path, " "), func(t *testing.T) {
			root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})
			cmd, rest, err := root.Find(tc.path)
			if err != nil {
				t.Fatalf("Find(%v): unexpected error: %v", tc.path, err)
			}
			if tc.wantGone {
				// The old verb must stay unconsumed (Find stops at the parent
				// group and leaves the unknown name in rest). If rest is empty
				// the old name still resolved — the hard rename is incomplete.
				if len(rest) == 0 {
					t.Fatalf("%v: expected the old name to be unresolved (a hard rename leaves no alias), but it resolved to %q", tc.path, cmd.CommandPath())
				}
				return
			}
			// The canonical name must resolve fully: nothing left unconsumed,
			// and the resolved leaf carries the final path element as its name.
			if len(rest) != 0 {
				t.Fatalf("%v: expected to resolve fully, but %v was left unconsumed (resolved %q)", tc.path, rest, cmd.CommandPath())
			}
			if want := tc.path[len(tc.path)-1]; cmd.Name() != want {
				t.Fatalf("%v: resolved to leaf %q, want %q", tc.path, cmd.Name(), want)
			}
		})
	}
}

// TestVerbVocabulary_oldLeafNamesFailLoudly asserts the runtime half of the
// hard rename: executing a removed leaf verb under its group does not quietly
// print group help with exit 0 (cobra's default for an unknown subcommand of
// a child group). The group rejects it as CLI misuse — exit 2, message naming
// the unknown command — so a CI step still calling the old name breaks.
func TestVerbVocabulary_oldLeafNamesFailLoudly(t *testing.T) {
	// Old names kept as SPLIT args (never a contiguous phrase) for the #168 gate.
	for _, args := range [][]string{
		{"apps", "info"},
		{"tracks", "status"},
		{"team", "grants", "revoke"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})
			root.SetArgs(args)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			err := root.Execute()
			if err == nil {
				t.Fatalf("%v: expected a misuse error, got nil (the old name silently succeeded)", args)
			}
			if code := exit.For(err); code != 2 {
				t.Errorf("%v: exit code = %d, want 2 (CLI misuse); err=%v", args, code, err)
			}
			if !strings.Contains(err.Error(), "unknown command") {
				t.Errorf("%v: error = %q, want it to name the unknown command", args, err)
			}
		})
	}
}

// groupPaths is the full set of grouping nouns in the command tree — pure
// nouns that carry no business logic, whose only job is to hold subcommands
// (and, when bare, print help). The empty path is the root. Every entry must
// behave identically: bare → help (exit 0), unknown subcommand → loud CLI
// misuse (exit 2). Kept as SPLIT path elements (never a contiguous phrase) so
// the #168 verb gate stays green on this file.
var groupPaths = [][]string{
	{}, // root (gplay)
	{"auth"},
	{"apps"},
	{"apps", "details"},
	{"releases"},
	{"tracks"},
	{"tracks", "availability"},
	{"testers"},
	{"team"},
	{"team", "users"},
	{"team", "grants"},
	{"reviews"},
	{"metadata"},
	{"metadata", "images"},
	{"compliance"},
	{"compliance", "datasafety"},
	{"games"},
	{"games", "achievements"},
	{"games", "leaderboards"},
}

// TestGroupCommands_unknownSubcommandFailsLoudly asserts the UX contract for
// EVERY grouping noun (and the root): a mistyped or unknown subcommand is
// rejected as CLI misuse — exit 2, message naming the unknown command — never
// the cobra default of silently printing group help with exit 0. This is the
// generalisation of TestVerbVocabulary_oldLeafNamesFailLoudly (which guards
// the specific ADR-0019 renames) to the whole tree, so a typo against any
// group breaks loudly instead of "succeeding".
func TestGroupCommands_unknownSubcommandFailsLoudly(t *testing.T) {
	// An invented token that is not a subcommand of any group (and not a
	// pre-rename verb, so the #168 gate is untouched).
	const bogus = "nonesuch"
	for _, path := range groupPaths {
		args := append(append([]string{}, path...), bogus)
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})
			root.SetArgs(args)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			err := root.Execute()
			if err == nil {
				t.Fatalf("%v: expected a misuse error, got nil (the group silently printed help)", args)
			}
			if code := exit.For(err); code != 2 {
				t.Errorf("%v: exit code = %d, want 2 (CLI misuse); err=%v", args, code, err)
			}
			if !strings.Contains(err.Error(), "unknown command") {
				t.Errorf("%v: error = %q, want it to name the unknown command", args, err)
			}
		})
	}
}

// TestGroupCommands_bareInvocationPrintsHelp asserts the other half of the
// grouping-noun contract: a bare group (no subcommand) prints its help and
// exits cleanly (nil error → exit 0). Giving every group a RunE for the
// unknown-subcommand case must not regress the bare case into an error.
func TestGroupCommands_bareInvocationPrintsHelp(t *testing.T) {
	for _, path := range groupPaths {
		name := strings.Join(path, " ")
		if name == "" {
			name = "(root)"
		}
		t.Run(name, func(t *testing.T) {
			root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})
			root.SetArgs(path)
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(io.Discard)
			if err := root.Execute(); err != nil {
				t.Fatalf("%v: bare group should print help and succeed, got err=%v", path, err)
			}
			// Help goes to stdout; a non-empty body confirms we printed help
			// rather than silently doing nothing.
			if out.Len() == 0 {
				t.Errorf("%v: bare group produced no help output", path)
			}
		})
	}
}

// TestFlagErrors_areCliMisuse asserts the docs/DESIGN.md §9 contract for the
// flag-error class: an unknown flag, a bad flag value, or a missing required
// flag is CLI misuse — exit 2, named in one "gplay: ..." line — not the
// generic exit 1 cobra hands back for a plain flag-parse error. The root's
// FlagErrorFunc routes parse errors (unknown flag, bad value) through
// exit.Usagef, and is inherited down the whole cobra tree so a leaf's parse
// error maps the same way; `apps init` validates its required --package
// in-band onto the same exit-2 path. Exercised through the real cobra tree via
// Execute, which surfaces the error main.go would hand to exit.For.
//
// Canonical verbs only, and any multi-token path is kept as SPLIT args (never
// a contiguous phrase), so the repo-wide verb gate (#168) stays green here.
func TestFlagErrors_areCliMisuse(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantMsg string // substring the one-line error must contain
	}{
		{"unknown-flag-at-root", []string{"--nope"}, "unknown flag"},
		{"unknown-flag-on-leaf", []string{"apps", "list", "--bogus"}, "unknown flag"},
		// pflag-level bad value: caught by the root FlagErrorFunc.
		{"bad-value-at-root", []string{"--verbose=notabool"}, "invalid argument"},
		// Downstream-validated bad value (--output is resolved in the kernel,
		// not pflag.Set): typed as a UsageError at its source, so it lands on
		// the same exit-2 path without the FlagErrorFunc seeing it.
		{"bad-output-value", []string{"apps", "list", "--output", "xyz"}, "unsupported --output"},
		{"missing-required-flag", []string{"apps", "init"}, "--package is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})
			root.SetArgs(tc.args)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			err := root.Execute()
			if err == nil {
				t.Fatalf("%v: expected a CLI-misuse error, got nil", tc.args)
			}
			if code := exit.For(err); code != 2 {
				t.Errorf("%v: exit code = %d, want 2 (CLI misuse); err=%v", tc.args, code, err)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("%v: error = %q, want it to contain %q", tc.args, err, tc.wantMsg)
			}
		})
	}
}

// TestMutatingRegistry_pinsWriteCommands is the completeness guard for the
// GPLAY_READONLY policy (#211 / ADR-0024): it pins exactly which leaf commands
// carry the mutating annotation (kernel.MarkMutating). A new write command that
// forgets MarkMutating, or a read command wrongly marked, fails here — so the
// policy's authority boundary cannot silently rot as the tree grows.
//
// Crucially it does NOT trust a hand-maintained allow-list to enumerate the
// tree: it WALKS the real cobra tree and asserts EVERY runnable leaf is present
// in `want` with the matching annotation. A leaf that nobody classified — the
// exact way a new surface slips past a static list by simply being absent — is a
// test failure, not a silent pass. The reverse guard also fails a `want` entry
// that resolves to no leaf, so a rename can't leave a stale line "covering"
// nothing. (No execution, no auth/network — Command.Name/Commands only.)
//
// Multi-token paths are kept as SPLIT args (never a contiguous phrase) so the
// repo-wide verb gate (#168) stays green on this file; the map key is joined at
// runtime and each leaf's own path comes from cmd.CommandPath(), never a literal.
func TestMutatingRegistry_pinsWriteCommands(t *testing.T) {
	// leaf classification: true = mutating Google Play state (MarkMutating;
	// GPLAY_READONLY refuses it, exit 4). false = a read, an offline validator,
	// or a local-only registry/credential op — must stay UNmarked so dashboards
	// and agents can still observe and plan with a production credential.
	type leaf struct {
		path     []string
		mutating bool
	}
	classified := []leaf{
		// top-level meta / local-only leaves
		{[]string{"init"}, false},
		{[]string{"version"}, false},
		{[]string{"schema"}, false},
		{[]string{"exit-codes"}, false},
		{[]string{"install-skills"}, false},

		// auth — local credential ops, none mutate Play state
		{[]string{"auth", "login"}, false},
		{[]string{"auth", "logout"}, false},
		{[]string{"auth", "status"}, false},
		{[]string{"auth", "list"}, false},
		{[]string{"auth", "doctor"}, false},

		// apps — local registry ops + reads
		{[]string{"apps", "init"}, false},
		{[]string{"apps", "add"}, false},
		{[]string{"apps", "list"}, false},
		{[]string{"apps", "accessible", "list"}, false}, // #347 — Reporting apps.search, read-only
		{[]string{"apps", "view"}, false},
		{[]string{"apps", "details", "view"}, false},
		{[]string{"apps", "details", "set"}, true},
		{[]string{"apps", "remove"}, false},

		// releases
		{[]string{"releases", "upload"}, true},
		{[]string{"releases", "promote"}, true},
		{[]string{"releases", "rollout"}, true},
		{[]string{"releases", "halt"}, true},
		{[]string{"releases", "resume"}, true},
		{[]string{"releases", "complete"}, true},
		{[]string{"releases", "list"}, false},
		{[]string{"releases", "mappings", "upload"}, true},
		{[]string{"releases", "sharing", "upload"}, true},
		{[]string{"releases", "expansion-files", "upload"}, true},
		{[]string{"releases", "expansion-files", "set"}, true},
		{[]string{"releases", "expansion-files", "view"}, false},
		{[]string{"releases", "generated", "list"}, false},
		{[]string{"releases", "generated", "download"}, false},

		// tracks
		{[]string{"tracks", "list"}, false},
		{[]string{"tracks", "view"}, false},
		{[]string{"tracks", "create"}, true},
		{[]string{"tracks", "availability", "view"}, false},

		// testers
		{[]string{"testers", "list"}, false},
		{[]string{"testers", "set"}, true},

		// device-tiers
		{[]string{"device-tiers", "create"}, true},
		{[]string{"device-tiers", "view"}, false},
		{[]string{"device-tiers", "list"}, false},

		// recovery — deploy/cancel/add-targeting are the production-impacting
		// lifecycle writes; a dropped MarkMutating on any would let a force-push
		// to users run under GPLAY_READONLY=1 instead of exit 4.
		{[]string{"recovery", "create"}, true},
		{[]string{"recovery", "list"}, false},
		{[]string{"recovery", "deploy"}, true},
		{[]string{"recovery", "cancel"}, true},
		{[]string{"recovery", "add-targeting"}, true},

		// team
		{[]string{"team", "permissions"}, false},
		{[]string{"team", "users", "list"}, false},
		{[]string{"team", "users", "view"}, false},
		{[]string{"team", "users", "add"}, true},
		{[]string{"team", "users", "set"}, true},
		{[]string{"team", "users", "remove"}, true},
		{[]string{"team", "grants", "list"}, false},
		{[]string{"team", "grants", "set"}, true},
		{[]string{"team", "grants", "remove"}, true},

		// customapps
		{[]string{"customapps", "create"}, true},

		// appstore — the alternative-app-store persona. Everything under
		// `catalog` is a read of Play's catalog export; nothing mutates.
		{[]string{"appstore", "catalog", "view"}, false},

		// edits — begin/commit/discard mutate Play state (insert/commit/delete);
		// status is a local-pin read.
		{[]string{"edits", "begin"}, true},
		{[]string{"edits", "commit"}, true},
		{[]string{"edits", "discard"}, true},
		{[]string{"edits", "status"}, false},

		// games
		{[]string{"games", "achievements", "list"}, false},
		{[]string{"games", "achievements", "view"}, false},
		{[]string{"games", "achievements", "create"}, true},
		{[]string{"games", "achievements", "update"}, true},
		{[]string{"games", "achievements", "delete"}, true},
		{[]string{"games", "leaderboards", "list"}, false},
		{[]string{"games", "leaderboards", "view"}, false},
		{[]string{"games", "leaderboards", "create"}, true},
		{[]string{"games", "leaderboards", "update"}, true},
		{[]string{"games", "leaderboards", "delete"}, true},

		// orders
		{[]string{"orders", "view"}, false},
		{[]string{"orders", "refund"}, true},

		// subscriptions — pull writes only local catalog files; apply
		// reconciles the live catalog (ADR-0041). prices convert is a pure
		// computation (derives regional prices, writes nothing).
		{[]string{"subscriptions", "pull"}, false},
		{[]string{"subscriptions", "apply"}, true},
		{[]string{"subscriptions", "prices", "convert"}, false},
		{[]string{"subscriptions", "prices", "migrate"}, true},

		// iap — pull writes only local catalog files; apply reconciles the
		// live v2 catalog (legacy is read-only, ADR-0041 §8).
		{[]string{"iap", "pull"}, false},
		{[]string{"iap", "apply"}, true},

		// reviews
		{[]string{"reviews", "list"}, false},
		{[]string{"reviews", "reply"}, true},
		{[]string{"reviews", "view"}, false},
		{[]string{"reviews", "history"}, false},

		// vitals — the whole namespace is read-only (Play Developer Reporting).
		{[]string{"vitals", "query"}, false},
		{[]string{"vitals", "crashes"}, false},
		{[]string{"vitals", "anr"}, false},
		{[]string{"vitals", "slowstart"}, false},
		{[]string{"vitals", "slowrendering"}, false},
		{[]string{"vitals", "excessivewakeup"}, false},
		{[]string{"vitals", "lmk"}, false},
		{[]string{"vitals", "stuckbgwakelock"}, false},
		{[]string{"vitals", "errors", "counts"}, false},
		{[]string{"vitals", "errors", "issues"}, false},
		{[]string{"vitals", "errors", "reports"}, false},
		{[]string{"vitals", "anomalies"}, false},

		// metadata — validate leaves are offline; only apply mutates.
		{[]string{"metadata", "list"}, false},
		{[]string{"metadata", "pull"}, false},
		{[]string{"metadata", "validate"}, false},
		{[]string{"metadata", "apply"}, true},
		{[]string{"metadata", "images", "list"}, false},
		{[]string{"metadata", "images", "pull"}, false},
		{[]string{"metadata", "images", "validate"}, false},
		{[]string{"metadata", "images", "apply"}, true},

		// compliance
		{[]string{"compliance", "datasafety", "validate"}, false},
		{[]string{"compliance", "datasafety", "set"}, true},
	}

	want := make(map[string]bool, len(classified))
	for _, e := range classified {
		key := strings.Join(e.path, " ")
		if _, dup := want[key]; dup {
			t.Fatalf("duplicate classification for %q — remove one", key)
		}
		want[key] = e.mutating
	}

	root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})

	// Walk EVERY runnable leaf and assert it is classified with the matching
	// annotation. Grouping nouns always have children, so a childless node is a
	// real leaf; cobra's injected help/completion helpers are skipped by name.
	seen := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if name := c.Name(); name == "help" || name == "completion" || strings.HasPrefix(name, "__") {
			return
		}
		if kids := c.Commands(); len(kids) > 0 {
			for _, k := range kids {
				walk(k)
			}
			return
		}
		// cmd.CommandPath() is "gplay <path>"; strip the root to get the key.
		key := strings.TrimPrefix(c.CommandPath(), "gplay ")
		seen[key] = true
		wantMut, ok := want[key]
		if !ok {
			t.Errorf("leaf %q is not classified in the mutating registry — every leaf must be pinned as mutating or read-only (a new write command MUST be kernel.MarkMutating so GPLAY_READONLY refuses it, exit 4)", c.CommandPath())
			return
		}
		if got := kernel.IsMutating(c); got != wantMut {
			if wantMut {
				t.Errorf("%q must be marked mutating (kernel.MarkMutating) — GPLAY_READONLY would not refuse it", key)
			} else {
				t.Errorf("%q is marked mutating but is a read/local command — GPLAY_READONLY would wrongly refuse it", key)
			}
		}
	}
	for _, c := range root.Commands() {
		walk(c)
	}

	// Reverse guard: a classified path that matches no real leaf is a stale
	// entry (the command was renamed or removed) — fail so the list can't drift
	// into "covering" commands that no longer exist.
	for key := range want {
		if !seen[key] {
			t.Errorf("classified path %q resolves to no leaf — stale entry (renamed or removed?)", key)
		}
	}
}

// TestStabilityRegistry_pinsPublicContract is the v1.0 counterpart of the
// mutating registry: it pins, leaf by leaf, which commands are inside the frozen
// Public contract and which ship [experimental] (ADR-0010 / ADR-0042).
//
// It matters more than a normal registry test because the failure is silent and
// one-way. Forget kernel.Experimental on a young command and gplay promises,
// from the next release on, that its flags and semantics will not change without
// a major bump — a promise that cannot be walked back without breaking someone's
// CI. The walk below makes "unclassified" a build failure instead.
//
// Multi-token paths are kept as SPLIT args (never a contiguous phrase) so the
// repo-wide verb gate (#168) stays green on this file.
func TestStabilityRegistry_pinsPublicContract(t *testing.T) {
	// leaf classification: true = shipped [experimental] (outside the Public
	// contract, free to change in any release). false = frozen — its name,
	// flags, semantics and exit codes are the 1.0 promise.
	type leaf struct {
		path         []string
		experimental bool
	}
	classified := []leaf{
		// top-level meta / local-only leaves — the install and diagnostic
		// surface CI scripts wrap first, frozen. `schema` is the exception: its
		// --output json is a projection gplay invented, not a Google resource.
		{[]string{"init"}, false},
		{[]string{"version"}, false},
		{[]string{"schema"}, true},
		{[]string{"exit-codes"}, false},
		{[]string{"install-skills"}, false},

		// auth — the oldest surface in the CLI, frozen in full.
		{[]string{"auth", "login"}, false},
		{[]string{"auth", "logout"}, false},
		{[]string{"auth", "status"}, false},
		{[]string{"auth", "list"}, false},
		{[]string{"auth", "doctor"}, false},

		// apps — MVP surface, frozen (ADR-0010 names it explicitly).
		{[]string{"apps", "init"}, false},
		{[]string{"apps", "add"}, false},
		{[]string{"apps", "list"}, false},
		{[]string{"apps", "accessible", "list"}, false},
		{[]string{"apps", "view"}, false},
		{[]string{"apps", "details", "view"}, false},
		{[]string{"apps", "details", "set"}, false},
		{[]string{"apps", "remove"}, false},

		// releases — the core loop is frozen; the three side surfaces bolted on
		// later (sharing, expansion-files, generated) are not.
		{[]string{"releases", "upload"}, false},
		{[]string{"releases", "promote"}, false},
		{[]string{"releases", "rollout"}, false},
		{[]string{"releases", "halt"}, false},
		{[]string{"releases", "resume"}, false},
		{[]string{"releases", "complete"}, false},
		{[]string{"releases", "list"}, false},
		{[]string{"releases", "mappings", "upload"}, false},
		{[]string{"releases", "sharing", "upload"}, true},
		{[]string{"releases", "expansion-files", "upload"}, true},
		{[]string{"releases", "expansion-files", "set"}, true},
		{[]string{"releases", "expansion-files", "view"}, true},
		{[]string{"releases", "generated", "list"}, true},
		{[]string{"releases", "generated", "download"}, true},

		// tracks / testers — MVP surface, frozen.
		{[]string{"tracks", "list"}, false},
		{[]string{"tracks", "view"}, false},
		{[]string{"tracks", "create"}, false},
		{[]string{"tracks", "availability", "view"}, false},
		{[]string{"testers", "list"}, false},
		{[]string{"testers", "set"}, false},

		// device-tiers / recovery / customapps — the #243 long-tail and the
		// account-axis creation surface, all shipped for coverage rather than
		// demand.
		{[]string{"device-tiers", "create"}, true},
		{[]string{"device-tiers", "view"}, true},
		{[]string{"device-tiers", "list"}, true},
		{[]string{"recovery", "create"}, true},
		{[]string{"recovery", "list"}, true},
		{[]string{"recovery", "deploy"}, true},
		{[]string{"recovery", "cancel"}, true},
		{[]string{"recovery", "add-targeting"}, true},
		{[]string{"customapps", "create"}, true},

		// appstore — a brand-new namespace for a persona gplay has never served,
		// on an addressing axis (the app store package name) never exercised
		// against a real enrolled app store. Experimental until it has been.
		{[]string{"appstore", "catalog", "view"}, true},

		// team / edits — exercised on every real account and every write
		// respectively, frozen.
		{[]string{"team", "permissions"}, false},
		{[]string{"team", "users", "list"}, false},
		{[]string{"team", "users", "view"}, false},
		{[]string{"team", "users", "add"}, false},
		{[]string{"team", "users", "set"}, false},
		{[]string{"team", "users", "remove"}, false},
		{[]string{"team", "grants", "list"}, false},
		{[]string{"team", "grants", "set"}, false},
		{[]string{"team", "grants", "remove"}, false},
		{[]string{"edits", "begin"}, false},
		{[]string{"edits", "commit"}, false},
		{[]string{"edits", "discard"}, false},
		{[]string{"edits", "status"}, false},

		// games — a second Google service on its own ID space, draft-only writes.
		{[]string{"games", "achievements", "list"}, true},
		{[]string{"games", "achievements", "view"}, true},
		{[]string{"games", "achievements", "create"}, true},
		{[]string{"games", "achievements", "update"}, true},
		{[]string{"games", "achievements", "delete"}, true},
		{[]string{"games", "leaderboards", "list"}, true},
		{[]string{"games", "leaderboards", "view"}, true},
		{[]string{"games", "leaderboards", "create"}, true},
		{[]string{"games", "leaderboards", "update"}, true},
		{[]string{"games", "leaderboards", "delete"}, true},

		// orders / subscriptions / iap — the commerce continent. The declarative
		// catalog (ADR-0041) shipped in v0.18.0, days before the 1.0 cut, and
		// exposes a file schema and a reconciliation model as contract.
		{[]string{"orders", "view"}, true},
		{[]string{"orders", "refund"}, true},
		{[]string{"subscriptions", "pull"}, true},
		{[]string{"subscriptions", "apply"}, true},
		{[]string{"subscriptions", "prices", "convert"}, true},
		{[]string{"subscriptions", "prices", "migrate"}, true},
		{[]string{"iap", "pull"}, true},
		{[]string{"iap", "apply"}, true},

		// reviews — list/reply/view are MVP and frozen; history reads CSVs out
		// of a GCS bucket whose layout Google can change without an API version.
		{[]string{"reviews", "list"}, false},
		{[]string{"reviews", "reply"}, false},
		{[]string{"reviews", "view"}, false},
		{[]string{"reviews", "history"}, true},

		// vitals — read-only, shipped early and stable since.
		{[]string{"vitals", "query"}, false},
		{[]string{"vitals", "crashes"}, false},
		{[]string{"vitals", "anr"}, false},
		{[]string{"vitals", "slowstart"}, false},
		{[]string{"vitals", "slowrendering"}, false},
		{[]string{"vitals", "excessivewakeup"}, false},
		{[]string{"vitals", "lmk"}, false},
		{[]string{"vitals", "stuckbgwakelock"}, false},
		{[]string{"vitals", "errors", "counts"}, false},
		{[]string{"vitals", "errors", "issues"}, false},
		{[]string{"vitals", "errors", "reports"}, false},
		{[]string{"vitals", "anomalies"}, false},

		// metadata / compliance — the first-release readiness surfaces, driven
		// end-to-end since June, frozen.
		{[]string{"metadata", "list"}, false},
		{[]string{"metadata", "pull"}, false},
		{[]string{"metadata", "validate"}, false},
		{[]string{"metadata", "apply"}, false},
		{[]string{"metadata", "images", "list"}, false},
		{[]string{"metadata", "images", "pull"}, false},
		{[]string{"metadata", "images", "validate"}, false},
		{[]string{"metadata", "images", "apply"}, false},
		{[]string{"compliance", "datasafety", "validate"}, false},
		{[]string{"compliance", "datasafety", "set"}, false},
	}

	want := make(map[string]bool, len(classified))
	for _, e := range classified {
		key := strings.Join(e.path, " ")
		if _, dup := want[key]; dup {
			t.Fatalf("duplicate classification for %q — remove one", key)
		}
		want[key] = e.experimental
	}

	root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})

	seen := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if name := c.Name(); name == "help" || name == "completion" || strings.HasPrefix(name, "__") {
			return
		}
		if kids := c.Commands(); len(kids) > 0 {
			for _, k := range kids {
				walk(k)
			}
			return
		}
		key := strings.TrimPrefix(c.CommandPath(), "gplay ")
		seen[key] = true
		wantExp, ok := want[key]
		if !ok {
			t.Errorf("leaf %q is not classified in the stability registry — every leaf must be pinned as frozen or [experimental]; an unclassified NEW command would silently join the frozen v1.0 Public contract (ADR-0010)", c.CommandPath())
			return
		}
		if got := kernel.IsExperimental(c); got != wantExp {
			if wantExp {
				t.Errorf("%q must be labelled experimental (kernel.Experimental) — as it stands, gplay promises its surface will not change without a major bump", key)
			} else {
				t.Errorf("%q is labelled experimental but is classified as part of the frozen Public contract", key)
			}
		}
		// The annotation is the semantics; the visible tag is what a CI author
		// actually reads before wiring a command into a pipeline. An experimental
		// command with no marker in its help is a broken promise in the other
		// direction.
		if wantExp && !strings.Contains(c.Short, "[experimental]") {
			t.Errorf("%q is experimental but its Short %q carries no visible marker", key, c.Short)
		}
	}
	for _, c := range root.Commands() {
		walk(c)
	}

	for key := range want {
		if !seen[key] {
			t.Errorf("classified path %q resolves to no leaf — stale entry (renamed or removed?)", key)
		}
	}
}

func TestRootCmd_persistentFlags_serviceAccountAccountAndVerbose(t *testing.T) {
	root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})

	for _, name := range []string{"service-account", "account", "verbose"} {
		f := root.PersistentFlags().Lookup(name)
		if f == nil {
			t.Errorf("root command missing persistent --%s flag", name)
		}
	}
	// -v is the documented shorthand for --verbose (docs/DESIGN.md §8).
	if f := root.PersistentFlags().ShorthandLookup("v"); f == nil || f.Name != "verbose" {
		t.Errorf("root command missing -v shorthand wired to --verbose; got %v", f)
	}
}

func TestResolveVersion(t *testing.T) {
	buildInfo := &debug.BuildInfo{
		Main: debug.Module{Version: "v0.1.0-alpha.1"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abc123"},
			{Key: "vcs.time", Value: "2026-05-22T12:00:00Z"},
			{Key: "GOOS", Value: "linux"},
		},
	}

	tests := []struct {
		name                                 string
		ldVersion, ldCommit, ldDate          string
		info                                 *debug.BuildInfo
		infoOK                               bool
		wantVersion, wantCommit, wantDateStr string
	}{
		{
			name:      "ldflags present — keep them, ignore BuildInfo",
			ldVersion: "v9.9.9", ldCommit: "deadbeef", ldDate: "2030-01-01",
			info: buildInfo, infoOK: true,
			wantVersion: "v9.9.9", wantCommit: "deadbeef", wantDateStr: "2030-01-01",
		},
		{
			name:      "ldflags default + BuildInfo — fall back to BuildInfo",
			ldVersion: "dev", ldCommit: "none", ldDate: "unknown",
			info: buildInfo, infoOK: true,
			wantVersion: "v0.1.0-alpha.1", wantCommit: "abc123", wantDateStr: "2026-05-22T12:00:00Z",
		},
		{
			name:      "ldflags default + no BuildInfo — keep defaults",
			ldVersion: "dev", ldCommit: "none", ldDate: "unknown",
			info: nil, infoOK: false,
			wantVersion: "dev", wantCommit: "none", wantDateStr: "unknown",
		},
		{
			name:      "ldflags default + BuildInfo with (devel) version — keep default version, take vcs.* fields",
			ldVersion: "dev", ldCommit: "none", ldDate: "unknown",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "(devel)"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "abc123"},
					{Key: "vcs.time", Value: "2026-05-22T12:00:00Z"},
				},
			},
			infoOK:      true,
			wantVersion: "dev", wantCommit: "abc123", wantDateStr: "2026-05-22T12:00:00Z",
		},
		{
			name:      "ldflags default + BuildInfo missing vcs.* settings — keep commit/date defaults",
			ldVersion: "dev", ldCommit: "none", ldDate: "unknown",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "v0.2.0"},
			},
			infoOK:      true,
			wantVersion: "v0.2.0", wantCommit: "none", wantDateStr: "unknown",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, c, d := resolveVersion(tc.ldVersion, tc.ldCommit, tc.ldDate, tc.info, tc.infoOK)
			if v != tc.wantVersion || c != tc.wantCommit || d != tc.wantDateStr {
				t.Errorf("resolveVersion = (%q, %q, %q); want (%q, %q, %q)",
					v, c, d, tc.wantVersion, tc.wantCommit, tc.wantDateStr)
			}
		})
	}
}
