package datasafety_test

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/PollyGlot/google-play-cli/internal/compliance/datasafety"
)

// FuzzDataSafetyValidate fuzzes the offline check `compliance data-safety
// validate` and `set` run on a user-supplied CSV. Whatever the bytes, Validate
// returns exactly one of a Report or a non-empty list of Problems, each with a
// message, and never panics. On success the Payload `set` would POST is the
// input minus at most one leading UTF-8 BOM (nothing else is rewritten), it is
// valid UTF-8, and the tally matches the header it reports.
func FuzzDataSafetyValidate(f *testing.F) {
	header := strings.Join(datasafety.ReferenceHeader(), ",")
	for _, seed := range []string{
		header + "\n" + strings.Repeat("x,", len(datasafety.ReferenceHeader())-1) + "x\n",
		"a,b\n1,2\n",
		"\xef\xbb\xbfa,b\n1,2\n",
		"\xef\xbb\xbf\xef\xbb\xbfa\n",
		"a,b\n1\n",
		"\"unterminated\n",
		"a,\"b\"c\n",
		"a,b\n\xff\n",
		"",
		" \n\t",
	} {
		f.Add([]byte(seed))
	}
	bom := []byte("\xef\xbb\xbf")
	f.Fuzz(func(t *testing.T, raw []byte) {
		rep, probs := datasafety.Validate(raw)
		if (rep == nil) == (len(probs) == 0) {
			t.Fatalf("Validate(%q) = report %v with %d problems, want exactly one of them", raw, rep != nil, len(probs))
		}
		for _, p := range probs {
			if p.Message == "" {
				t.Fatalf("Validate(%q) returned a problem with no message", raw)
			}
		}
		if rep == nil {
			return
		}
		if !bytes.Equal(rep.Payload, bytes.TrimPrefix(raw, bom)) {
			t.Fatalf("Payload = %q, want the input minus one leading BOM: %q", rep.Payload, raw)
		}
		if !utf8.Valid(rep.Payload) {
			t.Fatalf("accepted a Payload that is not valid UTF-8: %q", rep.Payload)
		}
		if rep.Columns != len(rep.Header) || rep.Rows < 0 {
			t.Fatalf("tally Columns=%d Rows=%d does not match header %q", rep.Columns, rep.Rows, rep.Header)
		}
	})
}
