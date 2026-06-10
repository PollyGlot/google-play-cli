package batch

import (
	"strings"
	"testing"
)

// FuzzParse fuzzes the reply-batch TSV parser, which consumes user-authored
// files or stdin — fully untrusted bytes. The parser is contractually
// crash-free (every problem is reported on a Line's Err, never as a panic or a
// returned error), so the fuzz invariants are: it never panics, and every
// successfully-parsed Line (Err == nil) carries a non-empty review-id AND a
// non-empty reply — a malformed record must surface as an Err, never as a
// half-populated Record. Seeded from the unit-test fixtures plus edge cases.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"r1\thello\nr2\tworld\n",
		"# comment\n\nr1\treply\n",
		"r1\t\"say \"\"hi\"\"\"\n",
		"\tjust a reply\n",
		"r1\t\n",
		"  r1  \tthanks\n",
		"r1\tHe said \"hi\" please\n",
		"only-one-field\n",
		"a\tb\tc\n",
		"\"unterminated quote\n",
		"r1\tmulti\nline\n",
		"é\t日本 🎉\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		lines := Parse(strings.NewReader(in))
		for _, ln := range lines {
			if ln.Err != nil {
				continue
			}
			if ln.Record.ReviewID == "" {
				t.Errorf("Parse(%q) produced an OK line with an empty review-id: %+v", in, ln)
			}
			if ln.Record.Reply == "" {
				t.Errorf("Parse(%q) produced an OK line with an empty reply: %+v", in, ln)
			}
		}
	})
}
