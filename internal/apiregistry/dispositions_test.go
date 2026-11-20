package apiregistry_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
)

func TestDispositionsAreWellFormed(t *testing.T) {
	for _, err := range apiregistry.LintDispositions(methodIDsFromPaths(t)) {
		t.Error(err)
	}
}

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
