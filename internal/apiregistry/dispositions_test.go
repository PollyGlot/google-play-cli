package apiregistry_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
)

// TestDispositionsAreWellFormed runs the shared lint over the committed lists:
// every redundant or parked id exists in paths.txt, carries exactly one
// disposition, says why, and (the invariant that keeps "redundant" honest) a
// redundant entry's canonical is a method a shipped command calls. The
// negative cases live in dispositions_internal_test.go, on fabricated lists.
func TestDispositionsAreWellFormed(t *testing.T) {
	for _, err := range apiregistry.LintDispositions(methodIDsFromPaths(t)) {
		t.Error(err)
	}
}

// TestDispositionsAreSortedByMethodID keeps both lists in paths.txt order, like
// entries and exclusions, so a diff on the file reads like a diff on the index.
func TestDispositionsAreSortedByMethodID(t *testing.T) {
	var prev string
	for _, r := range apiregistry.Redundancies() {
		if r.MethodID < prev {
			t.Errorf("redundancies out of order: %q after %q", r.MethodID, prev)
		}
		prev = r.MethodID
	}
	prev = ""
	for _, p := range apiregistry.Parkings() {
		if p.MethodID < prev {
			t.Errorf("parkings out of order: %q after %q", p.MethodID, prev)
		}
		prev = p.MethodID
	}
}
