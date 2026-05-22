package main

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

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
