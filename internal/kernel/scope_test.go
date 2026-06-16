package kernel_test

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// TestWithScope_roundTrips asserts the per-command OAuth scope annotation —
// the reporting-scope analogue of MarkMutating — round-trips: WithScope marks
// a command, ScopeFor reads it back, and an unmarked command reports "" (the
// default-scope sentinel the kernel resolves to AndroidPublisherScope).
func TestWithScope_roundTrips(t *testing.T) {
	marked := kernel.WithScope(&cobra.Command{Use: "query"}, token.ReportingScope)
	if got := kernel.ScopeFor(marked); got != token.ReportingScope {
		t.Errorf("ScopeFor(marked) = %q, want %q", got, token.ReportingScope)
	}

	plain := &cobra.Command{Use: "list"}
	if got := kernel.ScopeFor(plain); got != "" {
		t.Errorf("ScopeFor(unmarked) = %q, want \"\"", got)
	}
}

// TestFromCobra_capturesScope asserts FromCobra lifts the command's scope
// annotation into Inputs.Scope, so a scoped command's RunContext requests the
// right token scope without each business function re-deriving it.
func TestFromCobra_capturesScope(t *testing.T) {
	cmd := kernel.WithScope(&cobra.Command{Use: "query"}, token.ReportingScope)
	// FromCobra reads persistent flags; give the command the flags it expects.
	cmd.Flags().Bool("verbose", false, "")
	cmd.Flags().String("service-account", "", "")
	cmd.Flags().String("account", "", "")
	cmd.Flags().Duration("timeout", 0, "")
	cmd.Flags().Int("retry", 0, "")

	in := kernel.FromCobra(cmd, "")
	if in.Scope != token.ReportingScope {
		t.Errorf("Inputs.Scope = %q, want %q", in.Scope, token.ReportingScope)
	}
}
