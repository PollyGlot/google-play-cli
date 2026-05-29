package filter

import "testing"

// TestParse_rejectsInvalid covers the out-of-band specs the grammar must
// refuse: a rating below 1, above 5, a reversed range, and non-numeric
// junk. Each must surface a non-nil error (the command maps it to exit 2);
// the parser itself stays exit-code-agnostic.
func TestParse_rejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		spec string
	}{
		{"below range", "0"},
		{"above range", "6"},
		{"reverse range", "5-1"},
		{"non numeric", "abc"},
		{"non numeric in set", "1,x,3"},
		{"range hi out of band", "1-6"},
		{"empty member", "1,,3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.spec); err == nil {
				t.Errorf("Parse(%q) = nil error, want a rejection", tc.spec)
			}
		})
	}
}

// TestParse_emptySpecMatchesAll documents that an absent --stars (empty
// spec) yields a selector admitting every rating — the command calls
// Matches unconditionally rather than branching on whether --stars was set.
func TestParse_emptySpecMatchesAll(t *testing.T) {
	sel, err := Parse("")
	if err != nil {
		t.Fatalf("Parse(\"\"): unexpected error %v", err)
	}
	for s := 1; s <= 5; s++ {
		if !sel.Matches(s) {
			t.Errorf("empty spec: Matches(%d) = false, want true", s)
		}
	}
}

// TestParse_acceptsAndMatches drives the --stars grammar: a single star,
// an inclusive range, and a comma set. The behavior verified is "which
// ratings does the parsed selector admit", through the public Parse +
// Matches surface only — never the internal set representation.
func TestParse_acceptsAndMatches(t *testing.T) {
	cases := []struct {
		name    string
		spec    string
		match   []int // ratings the selector must admit
		noMatch []int // ratings the selector must reject
	}{
		{"single", "1", []int{1}, []int{2, 3, 4, 5}},
		{"range", "1-2", []int{1, 2}, []int{3, 4, 5}},
		{"set", "1,3,5", []int{1, 3, 5}, []int{2, 4}},
		{"full range", "1-5", []int{1, 2, 3, 4, 5}, nil},
		{"unordered set", "5,1,3", []int{1, 3, 5}, []int{2, 4}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sel, err := Parse(tc.spec)
			if err != nil {
				t.Fatalf("Parse(%q): unexpected error %v", tc.spec, err)
			}
			for _, s := range tc.match {
				if !sel.Matches(s) {
					t.Errorf("Matches(%d) = false, want true for spec %q", s, tc.spec)
				}
			}
			for _, s := range tc.noMatch {
				if sel.Matches(s) {
					t.Errorf("Matches(%d) = true, want false for spec %q", s, tc.spec)
				}
			}
		})
	}
}
