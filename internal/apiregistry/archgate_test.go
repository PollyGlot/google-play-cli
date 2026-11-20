package apiregistry_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var scanRoots = []string{
	filepath.Join("..", "play"),
	filepath.Join("..", "..", "commands"),
}

var allowedPathPackages = map[string]bool{
	filepath.Join("..", "play", "gcs"): true,
}

var forbiddenSubstrings = []string{
	".googleapis.com/",
	"/androidpublisher/",
	"/v1beta1/",
	"/games/",
	"/playcustomapp/",
	"/upload/",
}

func TestNoLiteralAPIPathsOutsideTheRegistry(t *testing.T) {
	for _, root := range scanRoots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if allowedPathPackages[path] {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			scanFile(t, path)
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
}

func scanFile(t *testing.T, path string) {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	if strings.HasSuffix(file.Name.Name, "test") {
		return
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.ImportSpec:
			return false
		case *ast.BasicLit:
			if node.Kind != token.STRING {
				return false
			}
			value, err := strconv.Unquote(node.Value)
			if err != nil {
				return false
			}
			for _, bad := range forbiddenSubstrings {
				if strings.Contains(value, bad) {
					pos := fset.Position(node.Pos())
					t.Errorf("%s:%d: literal API path %q (contains %q): resolve it through apiregistry.Resolve instead of writing the URL by hand", pos.Filename, pos.Line, value, bad)
					return false
				}
			}
			return false
		}
		return true
	})
}
