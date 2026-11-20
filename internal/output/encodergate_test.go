package output_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoJSONEncoderBypassesWriteJSON(t *testing.T) {
	repo := filepath.Join("..", "..")
	roots := []string{"commands", "cmd", "internal"}
	for _, root := range roots {
		commandTree := root != "internal"
		err := filepath.WalkDir(filepath.Join(repo, root), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				encoders, marshals, unescaped := scanEncoders(fn.Body)
				if marshals > 0 && commandTree {
					t.Errorf("%s: %s calls json.Marshal/MarshalIndent; use output.Marshal (or output.WriteJSON) instead", path, fn.Name.Name)
				}
				switch {
				case encoders > 0 && commandTree:
					t.Errorf("%s: %s builds a json.NewEncoder; render through output.WriteJSON instead", path, fn.Name.Name)
				case encoders > 0 && !unescaped:
					t.Errorf("%s: %s builds a json.NewEncoder without SetEscapeHTML(false)", path, fn.Name.Name)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
}

func scanEncoders(body *ast.BlockStmt) (encoders, marshals int, unescaped bool) {
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "json" {
			switch sel.Sel.Name {
			case "NewEncoder":
				encoders++
			case "Marshal", "MarshalIndent":
				marshals++
			}
		}
		if sel.Sel.Name == "SetEscapeHTML" && len(call.Args) == 1 {
			if arg, ok := call.Args[0].(*ast.Ident); ok && arg.Name == "false" {
				unescaped = true
			}
		}
		return true
	})
	return encoders, marshals, unescaped
}
