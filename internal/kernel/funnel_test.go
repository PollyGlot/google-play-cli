package kernel_test

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// TestFunnel_prefixes pins the one marker per level, so the prefixes a human
// or a log grep sees are decided here and nowhere else.
func TestFunnel_prefixes(t *testing.T) {
	var buf bytes.Buffer
	rc := kernel.NewForTest(context.Background(), kernel.Boot{Stderr: &buf}, kernel.Inputs{})
	rc.Confirmf("done %d", 1)
	rc.Warnf("careful %s", "x")
	rc.Notef("more with --page-token %s", "t")
	rc.Logf("OK %s", "id")
	rc.Failf("ERR %s", "id")
	want := "✓ done 1\nwarning: careful x\nNOTE: more with --page-token t\nOK id\nERR id\n"
	if buf.String() != want {
		t.Errorf("stderr =\n%q\nwant\n%q", buf.String(), want)
	}

	// A RunContext built by hand may carry no Stderr: the funnel is a no-op,
	// never a panic.
	bare := &kernel.RunContext{}
	bare.Notef("x")
	bare.Logf("x")
	bare.Failf("x")
}

// TestLoggerFor_writesToCommandStderr covers the commands that never build a
// RunContext (`gplay init`, `install-skills`): same prefixes, cobra's writer.
func TestLoggerFor_writesToCommandStderr(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{Use: "x"}
	cmd.SetErr(&buf)
	l := kernel.LoggerFor(cmd)
	l.Confirmf("pinned %q", "com.example")
	l.Warnf("w")
	l.Logf("  hint")
	if want := "✓ pinned \"com.example\"\nwarning: w\n  hint\n"; buf.String() != want {
		t.Errorf("stderr = %q, want %q", buf.String(), want)
	}
}

// TestNoDirectStderrWritesInCommands is the gate behind the funnel: a stderr
// line written by hand under commands/ is a line a future --quiet cannot
// reach and a prefix nobody chose (the audit counted four warning spellings
// before the funnel). It parses every shipped Go file under commands/ and
// fails on a write whose target is a stderr: `fmt.Fprint*`, `io.WriteString`
// or a `.Write` on `X.Stderr`, `os.Stderr` or `cmd.ErrOrStderr()`.
//
// Handing the writer to a helper is not a write and passes (artifact.Verify
// takes rc.Stderr; `b.Stderr = cmd.ErrOrStderr()` wires a sub-run's Boot).
// Test files are skipped: they read stderr back, they do not write it.
func TestNoDirectStderrWritesInCommands(t *testing.T) {
	root := filepath.Join("..", "..", "commands")
	fset := token.NewFileSet()
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if target := stderrWriteTarget(call); target != "" {
				offenders = append(offenders, fset.Position(call.Pos()).String()+": writes to "+target)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("direct stderr writes under commands/: route them through the kernel funnel "+
			"(rc.Confirmf/Warnf/Notef/Logf/Failf, or kernel.LoggerFor(cmd) without a RunContext):\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// stderrWriteTarget returns the stderr expression call writes to, or "".
func stderrWriteTarget(call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	// w.Write(...) / w.WriteString(...) on a stderr expression.
	if sel.Sel.Name == "Write" || sel.Sel.Name == "WriteString" {
		if isStderr(sel.X) {
			return exprString(sel.X)
		}
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || len(call.Args) == 0 {
		return ""
	}
	switch {
	case pkg.Name == "fmt" && strings.HasPrefix(sel.Sel.Name, "Fprint"),
		pkg.Name == "io" && sel.Sel.Name == "WriteString":
		if isStderr(call.Args[0]) {
			return exprString(call.Args[0])
		}
	}
	return ""
}

// isStderr reports whether e is X.Stderr (rc.Stderr, os.Stderr, boot.Stderr)
// or X.ErrOrStderr().
func isStderr(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.SelectorExpr:
		return v.Sel.Name == "Stderr"
	case *ast.CallExpr:
		if s, ok := v.Fun.(*ast.SelectorExpr); ok {
			return s.Sel.Name == "ErrOrStderr"
		}
	}
	return false
}

func exprString(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.SelectorExpr:
		return exprString(v.X) + "." + v.Sel.Name
	case *ast.CallExpr:
		return exprString(v.Fun) + "()"
	case *ast.Ident:
		return v.Name
	}
	return "stderr"
}
