package batch

import (
	"strings"
	"testing"
)

func TestParse_records(t *testing.T) {
	lines := Parse(strings.NewReader("r1\thello\nr2\tworld\n"))
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %+v", len(lines), lines)
	}
	want := []Record{{ReviewID: "r1", Reply: "hello"}, {ReviewID: "r2", Reply: "world"}}
	for i, w := range want {
		if lines[i].Err != nil {
			t.Errorf("line %d: unexpected Err %v", i, lines[i].Err)
		}
		if lines[i].Record != w {
			t.Errorf("line %d: Record = %+v, want %+v", i, lines[i].Record, w)
		}
	}
	// Num tracks the 1-based source line so the command can point at it.
	if lines[0].Num != 1 || lines[1].Num != 2 {
		t.Errorf("Num = %d,%d, want 1,2", lines[0].Num, lines[1].Num)
	}
}

func TestParse_skipsBlankAndComments(t *testing.T) {
	in := "# a leading comment\n" +
		"r1\thi\n" +
		"\n" + // blank
		"   \n" + // whitespace-only
		"# another comment\n" +
		"r2\tyo\n"
	lines := Parse(strings.NewReader(in))
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2 (comments + blanks skipped): %+v", len(lines), lines)
	}
	if lines[0].Record != (Record{"r1", "hi"}) || lines[1].Record != (Record{"r2", "yo"}) {
		t.Errorf("records = %+v, %+v; want {r1,hi},{r2,yo}", lines[0].Record, lines[1].Record)
	}
}

func TestParse_quoting(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Record
	}{
		{"embedded tab", "r1\t\"has\ttab\"\n", Record{"r1", "has\ttab"}},
		{"embedded newline", "r1\t\"line one\nline two\"\n", Record{"r1", "line one\nline two"}},
		{"escaped quote", "r1\t\"say \"\"hi\"\"\"\n", Record{"r1", `say "hi"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := Parse(strings.NewReader(tc.in))
			if len(lines) != 1 || lines[0].Err != nil {
				t.Fatalf("got %+v, want one clean record", lines)
			}
			if lines[0].Record != tc.want {
				t.Errorf("Record = %+v, want %+v", lines[0].Record, tc.want)
			}
		})
	}
}

func TestParse_malformedLineDoesNotAbort(t *testing.T) {
	// A missing-tab line (1 field) and an extra-tab line (3 fields) both sit
	// between valid records; the valid ones must still come through, in order.
	in := "r1\tok\n" +
		"noTabHere\n" +
		"r2\ta\tb\n" +
		"r3\tfine\n"
	lines := Parse(strings.NewReader(in))
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4 (2 ok, 2 malformed): %+v", len(lines), lines)
	}
	if lines[0].Err != nil || lines[0].Record != (Record{"r1", "ok"}) {
		t.Errorf("line 0 should be the first valid record, got %+v", lines[0])
	}
	if lines[1].Err == nil {
		t.Errorf("line 1 (no tab) should be malformed, got %+v", lines[1])
	}
	if lines[2].Err == nil {
		t.Errorf("line 2 (extra tab) should be malformed, got %+v", lines[2])
	}
	if lines[3].Err != nil || lines[3].Record != (Record{"r3", "fine"}) {
		t.Errorf("line 3 should be the trailing valid record, got %+v", lines[3])
	}
}

func TestParse_emptyReviewID_isMalformed(t *testing.T) {
	lines := Parse(strings.NewReader("\tjust a reply\n"))
	if len(lines) != 1 || lines[0].Err == nil {
		t.Fatalf("a line with an empty review-id must be malformed, got %+v", lines)
	}
}

func TestParse_emptyReply_isMalformed(t *testing.T) {
	// A trailing tab with no reply text parses to two fields, the second
	// empty. Single mode rejects an empty --reply (exit 2); batch must mirror
	// it rather than POST an empty developer response.
	lines := Parse(strings.NewReader("r1\t\n"))
	if len(lines) != 1 || lines[0].Err == nil {
		t.Fatalf("a line with an empty reply must be malformed, got %+v", lines)
	}
	if lines[0].Record != (Record{}) {
		t.Errorf("a malformed line must not carry a Record, got %+v", lines[0].Record)
	}
}

func TestParse_bareQuoteInReplyIsLiteral(t *testing.T) {
	// A reply with a bare (non-RFC-4180) double-quote is common prose and must
	// be taken literally, not rejected as a parse error (LazyQuotes).
	lines := Parse(strings.NewReader("r1\tHe said \"hi\" please\n"))
	if len(lines) != 1 || lines[0].Err != nil {
		t.Fatalf("a bare quote in prose must parse cleanly, got %+v", lines)
	}
	if lines[0].Record != (Record{"r1", `He said "hi" please`}) {
		t.Errorf("Record = %+v, want the quote kept literal", lines[0].Record)
	}
}

func TestParse_reviewIDIsTrimmed(t *testing.T) {
	// Surrounding whitespace on a hand-edited id would be URL-escaped and 404;
	// it is trimmed before the record is built.
	lines := Parse(strings.NewReader("  r1  \tthanks\n"))
	if len(lines) != 1 || lines[0].Err != nil {
		t.Fatalf("got %+v, want one clean record", lines)
	}
	if lines[0].Record.ReviewID != "r1" {
		t.Errorf("ReviewID = %q, want %q (surrounding whitespace trimmed)", lines[0].Record.ReviewID, "r1")
	}
}

func TestParse_leadingUnterminatedQuoteAbsorbsRest(t *testing.T) {
	// Documents the LazyQuotes footgun: a reply that opens with a double-quote
	// and never closes it absorbs the remaining lines into one record rather
	// than erroring. Following rows are therefore not parsed separately. This
	// pins the known tradeoff so a future reader does not expect r2/r3 here.
	lines := Parse(strings.NewReader("r1\t\"oops\nr2\tgood\nr3\tgood\n"))
	if len(lines) != 1 {
		t.Fatalf("a leading unterminated quote folds the rest into one record; got %d lines: %+v", len(lines), lines)
	}
	if lines[0].Err != nil || lines[0].Record.ReviewID != "r1" {
		t.Errorf("line 0 = %+v, want a single r1 record carrying the absorbed tail", lines[0])
	}
	if !strings.Contains(lines[0].Record.Reply, "r2") {
		t.Errorf("the absorbed reply should contain the following rows verbatim, got %q", lines[0].Record.Reply)
	}
}
