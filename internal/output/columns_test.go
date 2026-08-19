package output_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// row is a tiny stand-in for a command's row type, exercising Column[T]
// over a struct just like reviews.Review / TrackRow / tracks.Release do.
type row struct {
	a, b, c string
}

// cols is the shared fixture: a three-column set whose declaration order
// (a, b, c) is both the registry and the default render order.
func cols() output.ColumnSet[row] {
	return output.NewColumnSet(
		output.Column[row]{Key: "a", Header: "A", Value: func(r row) string { return r.a }},
		output.Column[row]{Key: "b", Header: "B", Value: func(r row) string { return r.b }},
		output.Column[row]{Key: "c", Header: "C", Value: func(r row) string { return r.c }},
	)
}

func keysOf(cs []output.Column[row]) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Key
	}
	return out
}

// TestColumnSet_Resolve locks the resolution contract every list command
// now leans on instead of carrying its own copy: empty spec → all columns
// in declaration order; an explicit spec selects and REORDERS; blanks are
// skipped; and an unknown or empty selection is a CLI misuse (exit 2). The
// default↔registry invariant that used to be re-tested per command is here
// structural (DefaultKeys IS the declaration order) and asserted once.
func TestColumnSet_Resolve(t *testing.T) {
	cs := cols()

	if got := cs.DefaultKeys(); strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("DefaultKeys() = %v, want [a b c]", got)
	}

	t.Run("empty spec yields all in declaration order", func(t *testing.T) {
		got, err := cs.Resolve("")
		if err != nil {
			t.Fatalf("Resolve(\"\"): %v", err)
		}
		if k := keysOf(got); strings.Join(k, ",") != "a,b,c" {
			t.Errorf("default columns = %v, want [a b c]", k)
		}
	})

	t.Run("explicit spec selects and reorders", func(t *testing.T) {
		got, err := cs.Resolve("c,a")
		if err != nil {
			t.Fatalf("Resolve(\"c,a\"): %v", err)
		}
		if k := keysOf(got); strings.Join(k, ",") != "c,a" {
			t.Errorf("selected columns = %v, want [c a]", k)
		}
	})

	t.Run("blank entries are skipped", func(t *testing.T) {
		got, err := cs.Resolve("a, ,b")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if k := keysOf(got); strings.Join(k, ",") != "a,b" {
			t.Errorf("columns = %v, want [a b]", k)
		}
	})

	t.Run("unknown key is exit-2 usage error", func(t *testing.T) {
		_, err := cs.Resolve("a,bogus")
		var ue *exit.UsageError
		if !errors.As(err, &ue) {
			t.Fatalf("Resolve(unknown) error = %v (%T), want *exit.UsageError", err, err)
		}
		if exit.For(err) != 2 {
			t.Errorf("exit.For = %d, want 2", exit.For(err))
		}
		if !strings.Contains(err.Error(), "a, b, c") {
			t.Errorf("error should list the valid set; got %q", err.Error())
		}
	})

	t.Run("all-blank spec is exit-2 usage error", func(t *testing.T) {
		_, err := cs.Resolve(" , ")
		if exit.For(err) != 2 {
			t.Errorf("exit.For = %d, want 2 (err=%v)", exit.For(err), err)
		}
	})
}

func TestRenderTable(t *testing.T) {
	cs := cols()
	sel, _ := cs.Resolve("a,c")
	var buf bytes.Buffer
	if err := output.RenderTable(&buf, sel, []row{{a: "1", b: "2", c: "3"}}); err != nil {
		t.Fatalf("RenderTable: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "A") || !strings.Contains(out, "C") {
		t.Errorf("table missing selected headers:\n%s", out)
	}
	if strings.Contains(out, "B") {
		t.Errorf("table should drop the unselected B column:\n%s", out)
	}
	if !strings.Contains(out, "1") || !strings.Contains(out, "3") {
		t.Errorf("table missing cell values:\n%s", out)
	}
}

func TestRenderMarkdown(t *testing.T) {
	cs := cols()
	sel, _ := cs.Resolve("")
	var buf bytes.Buffer
	if err := output.RenderMarkdown(&buf, sel, []row{{a: "x", b: "y", c: "z"}}); err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "| A | B | C |") {
		t.Errorf("markdown header row missing:\n%s", out)
	}
	if !strings.Contains(out, "| --- | --- | --- |") {
		t.Errorf("markdown separator row missing:\n%s", out)
	}
	if !strings.Contains(out, "| x | y | z |") {
		t.Errorf("markdown data row missing:\n%s", out)
	}
}
