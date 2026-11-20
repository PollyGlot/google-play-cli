package batch

import (
	"strings"
	"testing"
)

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
