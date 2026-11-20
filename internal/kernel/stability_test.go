package kernel_test

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

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

func TestExperimental_synthesisesLongFromShort(t *testing.T) {
	cmd := kernel.Experimental(&cobra.Command{Use: "create", Short: "Create a recovery action"})

	if !strings.HasSuffix(cmd.Long, "Create a recovery action") {
		t.Errorf("Long = %q, want the Short as its body", cmd.Long)
	}
	if strings.Count(cmd.Long, "[experimental]") != 1 {
		t.Errorf("Long = %q, want exactly one [experimental] marker", cmd.Long)
	}
}

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

func TestIsExperimental_inheritsFromAncestor(t *testing.T) {
	group := kernel.Experimental(&cobra.Command{Use: "iap", Short: "Manage one-time products"})
	late := &cobra.Command{Use: "apply", Short: "Apply the catalog"}
	group.AddCommand(late)

	if !kernel.IsExperimental(late) {
		t.Error("IsExperimental(late child) = false, want true: the namespace is experimental")
	}
}

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

func TestIsExperimental_nilIsStable(t *testing.T) {
	if kernel.IsExperimental(nil) {
		t.Error("IsExperimental(nil) = true, want false")
	}
}

func TestExperimentalFlag_labelsOneFlag(t *testing.T) {
	cmd := &cobra.Command{Use: "upload"}
	cmd.Flags().String("young", "", "a new sub-feature")
	cmd.Flags().String("track", "", "target track")
	kernel.ExperimentalFlag(kernel.ExperimentalFlag(cmd, "young"), "young")

	young, track := cmd.Flags().Lookup("young"), cmd.Flags().Lookup("track")
	if !kernel.IsExperimentalFlag(cmd, young) {
		t.Error("IsExperimentalFlag(young) = false, want true")
	}
	if kernel.IsExperimentalFlag(cmd, track) {
		t.Error("IsExperimentalFlag(track) = true, want false: an unlabelled flag of a frozen command is frozen")
	}
	if kernel.IsExperimental(cmd) {
		t.Error("IsExperimental(cmd) = true, want false: a flag label must not unfreeze its command")
	}
	if young.Usage != "[experimental] a new sub-feature" {
		t.Errorf("usage = %q, want one [experimental] prefix", young.Usage)
	}
}

func TestExperimentalFlag_inheritsFromCommand(t *testing.T) {
	cmd := &cobra.Command{Use: "pull", Short: "Pull"}
	cmd.Flags().String("dir", "", "target dir")
	kernel.Experimental(cmd)
	if !kernel.IsExperimentalFlag(cmd, cmd.Flags().Lookup("dir")) {
		t.Error("IsExperimentalFlag(dir) = false on an experimental command, want true")
	}
}

func TestExperimentalFlag_unknownFlagPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("ExperimentalFlag on an unknown flag did not panic")
		}
	}()
	kernel.ExperimentalFlag(&cobra.Command{Use: "upload"}, "nope")
}
