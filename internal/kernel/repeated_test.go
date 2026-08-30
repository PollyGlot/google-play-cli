package kernel_test

import (
	"bytes"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// flagZoo builds a two-level tree carrying one flag per pflag value KIND gplay
// can plausibly register, each declared twice: once with the type's zero default
// and once with a real default. The zero-default half is the interesting one:
// pflag hides a zero default and prints a real one, and that decision is made by
// a type switch over pflag's own concrete value types, which is exactly what a
// wrapper hides.
func flagZoo() *cobra.Command {
	root := &cobra.Command{Use: "root", Short: "root"}
	pf := root.PersistentFlags()
	pf.Duration("timeout", 0, "per-request API timeout (default: 60s for control-plane calls)")
	pf.String("account", "", "credential to use")
	pf.Bool("verbose", false, "log to stderr")

	child := &cobra.Command{Use: "child", Short: "child", Run: func(*cobra.Command, []string) {}}
	f := child.Flags()
	f.Duration("grace", 90*time.Second, "grace period")
	f.String("track", "production", "track to ship to")
	f.Bool("complete", true, "ship at 100%")
	f.Int("limit", 0, "cap the result count")
	f.Int64("version-code", 42, "build to promote")
	f.Float64("user-fraction", 0, "staged rollout share")
	f.Count("v", "verbosity")
	f.IP("bind", net.IPv4(127, 0, 0, 1), "address to bind")
	f.StringSlice("locale", nil, "locales to touch")
	f.StringArray("file", nil, "files to upload")
	f.StringToString("label", nil, "labels to set")
	root.AddCommand(child)
	return root
}

// helpOf renders a command's own --help the way a user sees it.
func helpOf(t *testing.T, cmd *cobra.Command) string {
	t.Helper()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Help(); err != nil {
		t.Fatalf("Help(): %v", err)
	}
	return out.String()
}

// TestRejectRepeatedFlags_leavesHelpByteIdentical is the regression guard for
// the wrapper's one visible side effect. pflag decides whether to append
// ` (default X)` from a type switch over its OWN concrete value types, so any
// wrapper falls through to a generic branch that does not know "0s" is a zero
// duration: wrapping the tree started advertising `--timeout ... (default 0s)`
// on all 149 help screens, contradicting the flag's own usage text and leaking
// into the generated website reference (built from --help).
//
// The assertion is byte equality against the SAME tree unwrapped, at both
// levels (the root's persistent flags and a leaf's local ones), so it covers
// every flag kind in flagZoo rather than the one that happened to regress.
func TestRejectRepeatedFlags_leavesHelpByteIdentical(t *testing.T) {
	for _, path := range []string{"", "child"} {
		name := "root"
		if path != "" {
			name = path
		}
		t.Run(name, func(t *testing.T) {
			pick := func(root *cobra.Command) *cobra.Command {
				if path == "" {
					return root
				}
				sub, _, err := root.Find([]string{path})
				if err != nil {
					t.Fatalf("find %q: %v", path, err)
				}
				return sub
			}
			want := helpOf(t, pick(flagZoo()))
			got := helpOf(t, pick(kernel.RejectRepeatedFlags(flagZoo())))
			if got != want {
				t.Errorf("help after RejectRepeatedFlags differs from the declared tree\n--- declared ---\n%s\n--- wrapped ---\n%s", want, got)
			}
			if strings.Contains(got, "(default 0s)") {
				t.Errorf("help advertises `(default 0s)`: pflag's zero-default rule was lost")
			}
		})
	}
}

// TestRejectRepeatedFlags_stillRejectsAfterHelp pins the other half of the
// help hook: taking the wrappers off to RENDER must put them back, so a caller
// that prints help and keeps going (a library, a test) still gets the
// repeated-flag rejection afterwards.
func TestRejectRepeatedFlags_stillRejectsAfterHelp(t *testing.T) {
	root := kernel.RejectRepeatedFlags(flagZoo())
	_ = helpOf(t, root)
	f := root.PersistentFlags().Lookup("account")
	if err := f.Value.Set("a"); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	if err := f.Value.Set("b"); err == nil {
		t.Error("second Set after rendering help = nil, want a repeated-flag rejection")
	}
}

// TestRejectRepeatedFlags_mapFlagsStayRepeatable is the latent half of the
// exemption. pflag publishes an interface for its slice values (SliceValue) and
// none for its map values, so a structural check that only asks for SliceValue
// would wrap `--label k=v` and reject the second pair as misuse, even though a
// map flag is repeatable BY DESIGN (one occurrence per entry). No gplay command
// registers one yet: this test is the guard that the first one will not have to
// discover the hole.
func TestRejectRepeatedFlags_mapFlagsStayRepeatable(t *testing.T) {
	root := kernel.RejectRepeatedFlags(flagZoo())
	child, _, err := root.Find([]string{"child"})
	if err != nil {
		t.Fatalf("find child: %v", err)
	}
	if err := child.Flags().Parse([]string{"--label", "a=1", "--label", "b=2"}); err != nil {
		t.Fatalf("parsing a repeated map flag: %v, want nil", err)
	}
	got, err := child.Flags().GetStringToString("label")
	if err != nil {
		t.Fatalf("GetStringToString(label): %v", err)
	}
	if len(got) != 2 || got["a"] != "1" || got["b"] != "2" {
		t.Errorf("--label = %v, want map[a:1 b:2]", got)
	}
}
