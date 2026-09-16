package apiregistry

import (
	"strings"
	"testing"
)

// Fabricated universe: two called methods, one excluded, and ids the lists may
// or may not claim. Each case below breaks exactly one invariant so the
// message named is the one a maintainer would read.
var (
	lintKnown = map[string]bool{
		"svc.res.patch": true, "svc.res.list": true, "svc.res.update": true,
		"svc.res.get": true, "svc.runtime.get": true, "svc.other.get": true,
	}
	lintEntries    = []Entry{{MethodID: "svc.res.patch", Commands: []string{"res set"}}, {MethodID: "svc.res.list", Commands: []string{"res list"}}}
	lintExclusions = []Exclusion{{MethodID: "svc.runtime.get", Reason: "runtime"}}
)

func TestLintDispositionsRejectsBrokenInvariants(t *testing.T) {
	cases := []struct {
		name  string
		red   []Redundant
		park  []Parked
		wants string
	}{
		{
			name:  "unknown canonical",
			red:   []Redundant{{MethodID: "svc.res.update", CanonicalID: "svc.res.nope", Reason: "shape"}},
			wants: "names canonical \"svc.res.nope\", which no shipped command calls",
		},
		{
			name:  "uncalled canonical",
			red:   []Redundant{{MethodID: "svc.res.update", CanonicalID: "svc.res.get", Reason: "shape"}},
			wants: "names canonical \"svc.res.get\", which no shipped command calls",
		},
		{
			name:  "redundant id unknown to paths.txt",
			red:   []Redundant{{MethodID: "svc.res.ghost", CanonicalID: "svc.res.patch", Reason: "shape"}},
			wants: "absent from paths.txt",
		},
		{
			name:  "redundant without reason",
			red:   []Redundant{{MethodID: "svc.res.update", CanonicalID: "svc.res.patch", Reason: " "}},
			wants: "has no reason",
		},
		{
			name:  "redundant and registered",
			red:   []Redundant{{MethodID: "svc.res.list", CanonicalID: "svc.res.patch", Reason: "shape"}},
			wants: "both registered and redundant",
		},
		{
			name:  "redundant and excluded",
			red:   []Redundant{{MethodID: "svc.runtime.get", CanonicalID: "svc.res.patch", Reason: "shape"}},
			wants: "both excluded and redundant",
		},
		{
			name:  "parked without issue",
			park:  []Parked{{MethodID: "svc.other.get", Issue: 0, Reason: "later"}},
			wants: "names no issue",
		},
		{
			name:  "parked id unknown to paths.txt",
			park:  []Parked{{MethodID: "svc.other.ghost", Issue: 1, Reason: "later"}},
			wants: "absent from paths.txt",
		},
		{
			name:  "parked and redundant",
			red:   []Redundant{{MethodID: "svc.other.get", CanonicalID: "svc.res.patch", Reason: "shape"}},
			park:  []Parked{{MethodID: "svc.other.get", Issue: 1, Reason: "later"}},
			wants: "both redundant and parked",
		},
		{
			name:  "parked and registered",
			park:  []Parked{{MethodID: "svc.res.patch", Issue: 1, Reason: "later"}},
			wants: "both registered and parked",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := lintDispositions(lintKnown, lintEntries, lintExclusions, tc.red, tc.park)
			for _, err := range errs {
				if strings.Contains(err.Error(), tc.wants) {
					return
				}
			}
			t.Errorf("want an error containing %q, got %v", tc.wants, errs)
		})
	}
}

func TestLintDispositionsAcceptsWellFormedLists(t *testing.T) {
	red := []Redundant{{MethodID: "svc.res.update", CanonicalID: "svc.res.patch", Reason: "full-body shape of patch"}}
	park := []Parked{{MethodID: "svc.other.get", Issue: 42, Reason: "deferred"}}
	if errs := lintDispositions(lintKnown, lintEntries, lintExclusions, red, park); len(errs) != 0 {
		t.Errorf("well-formed lists rejected: %v", errs)
	}
}
