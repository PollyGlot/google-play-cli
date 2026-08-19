package kernel_test

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// TestExperimental_roundTrips asserts the label round-trips and that an
// unlabelled command reports false: the "frozen by default" side of ADR-0010,
// where absence of a label means the Public contract applies.
func TestExperimental_roundTrips(t *testing.T) {
	marked := kernel.Experimental(&cobra.Command{Use: "pull", Short: "Pull the catalog"})
	if !kernel.IsExperimental(marked) {
		t.Error("IsExperimental(marked) = false, want true")
	}

	plain := &cobra.Command{Use: "list", Short: "List apps"}
	if kernel.IsExperimental(plain) {
		t.Error("IsExperimental(unmarked) = true, want false: unlabelled commands are in the frozen contract")
	}
}

// TestExperimental_tagsHelpText asserts the label is visible in both places a
// user reads: the Short (rendered in the parent's subcommand list) and the Long
// (the command's own --help), with the Long carrying the consequence rather
// than the bare marker.
func TestExperimental_tagsHelpText(t *testing.T) {
	cmd := kernel.Experimental(&cobra.Command{
		Use:   "apply",
		Short: "Apply the catalog",
		Long:  "Apply the local catalog file to Google Play.",
	})

	if !strings.HasPrefix(cmd.Short, "[experimental] ") {
		t.Errorf("Short = %q, want the [experimental] prefix", cmd.Short)
	}
	if !strings.HasPrefix(cmd.Long, "[experimental] ") {
		t.Errorf("Long = %q, want the notice first", cmd.Long)
	}
	if !strings.Contains(cmd.Long, "without a major version bump") {
		t.Errorf("Long = %q, want the notice to state the contract consequence", cmd.Long)
	}
	if !strings.HasSuffix(cmd.Long, "Apply the local catalog file to Google Play.") {
		t.Errorf("Long = %q, want the original description preserved after the notice", cmd.Long)
	}
}

// TestExperimental_synthesisesLongFromShort asserts a command written with only
// a Short still shows the full notice in its own --help, and that the
// synthesised body does not repeat the marker inside the notice.
func TestExperimental_synthesisesLongFromShort(t *testing.T) {
	cmd := kernel.Experimental(&cobra.Command{Use: "create", Short: "Create a recovery action"})

	if !strings.HasSuffix(cmd.Long, "Create a recovery action") {
		t.Errorf("Long = %q, want the Short as its body", cmd.Long)
	}
	if strings.Count(cmd.Long, "[experimental]") != 1 {
		t.Errorf("Long = %q, want exactly one [experimental] marker", cmd.Long)
	}
}

// TestExperimental_labelsRegisteredSubtree asserts one call on a namespace
// covers the children already registered under it: the registration shape
// cmd/gplay/main.go uses, so each child carries the tag in its own help
// instead of inheriting it invisibly.
func TestExperimental_labelsRegisteredSubtree(t *testing.T) {
	group := &cobra.Command{Use: "subscriptions", Short: "Manage subscriptions"}
	child := &cobra.Command{Use: "pull", Short: "Pull the catalog"}
	grandchild := &cobra.Command{Use: "convert", Short: "Convert prices"}
	nested := &cobra.Command{Use: "prices", Short: "Manage prices"}
	nested.AddCommand(grandchild)
	group.AddCommand(child)
	group.AddCommand(nested)

	kernel.Experimental(group)

	for _, cmd := range []*cobra.Command{group, child, nested, grandchild} {
		if !kernel.IsExperimental(cmd) {
			t.Errorf("IsExperimental(%q) = false, want true", cmd.Name())
		}
		if !strings.HasPrefix(cmd.Short, "[experimental] ") {
			t.Errorf("%q Short = %q, want the tag", cmd.Name(), cmd.Short)
		}
	}
}

// TestIsExperimental_inheritsFromAncestor asserts a subcommand registered AFTER
// its namespace was labelled is still outside the contract. The parent walk is
// the safety net that keeps a late-added child from silently claiming more
// stability than the namespace containing it.
func TestIsExperimental_inheritsFromAncestor(t *testing.T) {
	group := kernel.Experimental(&cobra.Command{Use: "iap", Short: "Manage one-time products"})
	late := &cobra.Command{Use: "apply", Short: "Apply the catalog"}
	group.AddCommand(late)

	if !kernel.IsExperimental(late) {
		t.Error("IsExperimental(late child) = false, want true: the namespace is experimental")
	}
}

// TestExperimental_idempotent asserts labelling a child and then its parent:
// the order a namespace-wide call produces when a child was marked on its own:
// does not stack two markers in the help text.
func TestExperimental_idempotent(t *testing.T) {
	group := &cobra.Command{Use: "games", Short: "Manage Play Games config"}
	child := kernel.Experimental(&cobra.Command{Use: "achievements", Short: "Manage achievements"})
	group.AddCommand(child)

	kernel.Experimental(group)

	if got := strings.Count(child.Short, "[experimental]"); got != 1 {
		t.Errorf("child Short = %q, want exactly one marker, got %d", child.Short, got)
	}
	if got := strings.Count(child.Long, "[experimental]"); got != 1 {
		t.Errorf("child Long = %q, want exactly one marker, got %d", child.Long, got)
	}
}

// TestIsExperimental_nilIsStable guards the accessor against a nil command the
// way ScopeFor/IsMutating are guarded, so a caller walking a partially built
// tree cannot panic.
func TestIsExperimental_nilIsStable(t *testing.T) {
	if kernel.IsExperimental(nil) {
		t.Error("IsExperimental(nil) = true, want false")
	}
}
