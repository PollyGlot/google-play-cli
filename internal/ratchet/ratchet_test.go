package ratchet

import (
	"bufio"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var moduleRoot = filepath.Join("..", "..")

var scanRoots = []string{"cmd", "commands", "internal"}

var report = flag.Bool("ratchet.report", false, "print the allowlist size of every rule and exit (make ratchets)")

func TestMain(m *testing.M) {
	flag.Parse()
	if *report {
		if err := printReport(); err != nil {
			fmt.Fprintln(os.Stderr, "ratchets:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type srcFile struct {
	path    string
	fset    *token.FileSet
	ast     *ast.File
	isTest  bool
	imports map[string]string
}

type hit struct {
	key string
	pos token.Position
}

type rule struct {
	id           string
	hasAllowlist bool
	what, fix    string
	exempt       []string
	testCode     bool
	detect       func(f *srcFile) []hit
}

var rules = []rule{
	{
		id:   "request-helper",
		what: "direct (*http.Client).Do",
		fix:  "send the request through the executor, api.Do / api.DoJSON in internal/play/api",
		exempt: []string{
			"internal/play/api/",
			"internal/transport/",
			"internal/play/gcs/",
			"internal/discovery/",
		},
		detect: detectDirectDo,
	},
	{
		id:       "test-roundtripper",
		what:     "RoundTrip method on a test type",
		fix:      "use internal/testkit (testkit.NewFake, or a testkit.RoundTripFunc built on testkit.TokenResponse and testkit.Response) and extend the kit when it cannot express the case",
		exempt:   []string{"internal/testkit/"},
		testCode: true,
		detect:   detectRoundTrip,
	},
	{
		id:       "test-rsa-keygen",
		what:     "rsa key generation in a test",
		fix:      "use internal/testkit (testkit.RSAKey, testkit.PrivateKeyPEM or testkit.ServiceAccountJSON), one key per test binary",
		exempt:   []string{"internal/testkit/"},
		testCode: true,
		detect:   detectRSAKeygen,
	},
	{
		id:   "http-client",
		what: "http.Client constructed",
		fix:  "take the client the RunContext builds (rc.AuthedClient / rc.UploadClient), wrap transports in internal/transport, and build any new client with transport.NewClient",
		exempt: []string{
			"internal/transport/",
			"internal/discovery/",
			"internal/testkit/",
		},
		detect: detectHTTPClient,
	},
	{
		id:           "stdout-bypass",
		hasAllowlist: true,
		what:         "stdout written outside output.Render",
		fix:          "return an output.Renderable and let the kernel render it: output.Render runs every table and markdown view through output.SanitizeText, so an API string cannot carry a terminal escape sequence to the screen (output.WriteJSON for a JSON-only echo)",
		exempt: []string{
			"internal/output/",
			"internal/kernel/",
			"cmd/gplay/",
			"internal/discovery/",
			"commands/releases/generated/download/",
			"commands/releases/system-apks/download/",
		},
		detect: detectStdoutBypass,
	},
	{
		id:     "usage-error-type",
		what:   "local usage-error type",
		fix:    "return exit.UsageError (exit.Usagef) from internal/exit",
		exempt: []string{"internal/exit/"},
		detect: detectUsageErrorType,
	},
}

func TestRatchets(t *testing.T) {
	files := parseTree(t)
	for _, r := range rules {
		t.Run(r.id, func(t *testing.T) {
			allowed, err := loadAllowlist(r)
			if err != nil {
				t.Fatal(err)
			}
			for _, msg := range violations(r, scan(r, files), allowed) {
				t.Error(msg)
			}
		})
	}
}

func scan(r rule, files []*srcFile) map[string]hit {
	found := map[string]hit{}
	for _, f := range files {
		if f.isTest != r.testCode || exempted(r, f.path) {
			continue
		}
		for _, h := range r.detect(f) {
			if _, seen := found[h.key]; !seen {
				found[h.key] = h
			}
		}
	}
	return found
}

func exempted(r rule, path string) bool {
	for _, p := range r.exempt {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func violations(r rule, found map[string]hit, allowed map[string]bool) []string {
	var out []string
	for key, h := range found {
		if allowed[key] {
			continue
		}
		msg := fmt.Sprintf("%s:%d: [%s] %s in %s: %s.", h.pos.Filename, h.pos.Line, r.id, r.what, symbolOf(key), r.fix)
		if r.hasAllowlist {
			msg += fmt.Sprintf(" testdata/%s.allow lists today's offenders and only shrinks: do not add this one.", r.id)
		} else {
			msg += " This rule is at zero and has no allowlist."
		}
		out = append(out, msg)
	}
	for key := range allowed {
		if _, ok := found[key]; !ok {
			out = append(out, fmt.Sprintf("testdata/%s.allow: %q is no longer an offender: delete the line so the ratchet keeps its gain.", r.id, key))
		}
	}
	sort.Strings(out)
	return out
}

func symbolOf(key string) string {
	_, sym, _ := strings.Cut(key, " ")
	return sym
}

func loadAllowlist(r rule) (map[string]bool, error) {
	name := filepath.Join("testdata", r.id+".allow")
	allowed := map[string]bool{}
	fh, err := os.Open(name)
	if !r.hasAllowlist {
		if err == nil {
			_ = fh.Close()
			return nil, fmt.Errorf("%s exists but rule %s is at zero: delete the file", name, r.id)
		}
		return allowed, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = fh.Close() }()
	sc := bufio.NewScanner(fh)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(strings.Fields(line)) != 2 {
			return nil, fmt.Errorf("%s:%d: want `path symbol`, got %q", name, n, line)
		}
		if allowed[line] {
			return nil, fmt.Errorf("%s:%d: duplicate line %q", name, n, line)
		}
		allowed[line] = true
	}
	return allowed, sc.Err()
}

func printReport() error {
	for _, r := range rules {
		allowed, err := loadAllowlist(r)
		if err != nil {
			return err
		}
		state := "allowlisted"
		if !r.hasAllowlist {
			state = "forbidden, no allowlist"
		}
		fmt.Printf("%-18s %4d  (%s)\n", r.id, len(allowed), state)
	}
	return nil
}

func parseTree(t *testing.T) []*srcFile {
	t.Helper()
	var files []*srcFile
	for _, root := range scanRoots {
		err := filepath.WalkDir(filepath.Join(moduleRoot, root), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if name := d.Name(); name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			rel, err := filepath.Rel(moduleRoot, path)
			if err != nil {
				return err
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			f, err := parseFile(filepath.ToSlash(rel), src)
			if err != nil {
				return err
			}
			files = append(files, f)
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
	return files
}

func parseFile(path string, src []byte) (*srcFile, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	f := &srcFile{
		path:    path,
		fset:    fset,
		ast:     file,
		isTest:  strings.HasSuffix(path, "_test.go") || strings.HasSuffix(file.Name.Name, "test"),
		imports: map[string]string{},
	}
	for _, spec := range file.Imports {
		p, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := p[strings.LastIndex(p, "/")+1:]
		if spec.Name != nil {
			name = spec.Name.Name
		}
		f.imports[p] = name
	}
	return f, nil
}

func (f *srcFile) hit(symbol string, pos token.Pos) hit {
	return hit{key: f.path + " " + symbol, pos: f.fset.Position(pos)}
}

func (f *srcFile) isImportName(id string) bool {
	for _, name := range f.imports {
		if name == id {
			return true
		}
	}
	return false
}

func (f *srcFile) isPkgSel(e ast.Expr, importPath, sel string) bool {
	s, ok := e.(*ast.SelectorExpr)
	if !ok || s.Sel.Name != sel {
		return false
	}
	x, ok := s.X.(*ast.Ident)
	name, imported := f.imports[importPath]
	return ok && imported && x.Name == name
}

func eachDecl(f *srcFile, visit func(symbol string, n ast.Node)) {
	for _, decl := range f.ast.Decls {
		symbol := "(package)"
		if fd, ok := decl.(*ast.FuncDecl); ok {
			symbol = funcSymbol(fd)
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			if n != nil {
				visit(symbol, n)
			}
			return true
		})
	}
}

func funcSymbol(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	return recvType(fd) + "." + fd.Name.Name
}

func recvType(fd *ast.FuncDecl) string {
	e := fd.Recv.List[0].Type
	for {
		switch x := e.(type) {
		case *ast.StarExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.IndexListExpr:
			e = x.X
		case *ast.Ident:
			return x.Name
		default:
			return "?"
		}
	}
}

func detectDirectDo(f *srcFile) []hit {
	var hits []hit
	eachDecl(f, func(symbol string, n ast.Node) {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Do" {
			return
		}
		if x, ok := sel.X.(*ast.Ident); ok && f.isImportName(x.Name) {
			return
		}
		if _, ok := call.Args[0].(*ast.FuncLit); ok {
			return
		}
		hits = append(hits, f.hit(symbol, call.Pos()))
	})
	return hits
}

func detectRoundTrip(f *srcFile) []hit {
	var hits []hit
	for _, decl := range f.ast.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if ok && fd.Recv != nil && len(fd.Recv.List) > 0 && fd.Name.Name == "RoundTrip" {
			hits = append(hits, f.hit(recvType(fd), fd.Pos()))
		}
	}
	return hits
}

func detectRSAKeygen(f *srcFile) []hit {
	var hits []hit
	eachDecl(f, func(symbol string, n ast.Node) {
		call, ok := n.(*ast.CallExpr)
		if ok && (f.isPkgSel(call.Fun, "crypto/rsa", "GenerateKey") || f.isPkgSel(call.Fun, "crypto/rsa", "GenerateMultiPrimeKey")) {
			hits = append(hits, f.hit(symbol, call.Pos()))
		}
	})
	return hits
}

func detectHTTPClient(f *srcFile) []hit {
	var hits []hit
	eachDecl(f, func(symbol string, n ast.Node) {
		switch x := n.(type) {
		case *ast.CompositeLit:
			if f.isPkgSel(x.Type, "net/http", "Client") {
				hits = append(hits, f.hit(symbol, x.Pos()))
			}
		case *ast.CallExpr:
			if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "new" && len(x.Args) == 1 && f.isPkgSel(x.Args[0], "net/http", "Client") {
				hits = append(hits, f.hit(symbol, x.Pos()))
			}
		}
	})
	return hits
}

const outputPkg = "github.com/PollyGlot/google-play-cli/internal/output"

func detectStdoutBypass(f *srcFile) []hit {
	var hits []hit
	for _, decl := range f.ast.Decls {
		symbol := "(package)"
		if fd, ok := decl.(*ast.FuncDecl); ok {
			symbol = funcSymbol(fd)
		}
		sanctioned := map[ast.Expr]bool{}
		ast.Inspect(decl, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.AssignStmt:
				for i, lhs := range x.Lhs {
					if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "Stdout" {
						sanctioned[lhs] = true
						if len(x.Rhs) == len(x.Lhs) {
							sanctioned[x.Rhs[i]] = true
						}
					}
				}
			case *ast.KeyValueExpr:
				if k, ok := x.Key.(*ast.Ident); ok && k.Name == "Stdout" {
					sanctioned[x.Value] = true
				}
			case *ast.CallExpr:
				if (f.isPkgSel(x.Fun, outputPkg, "Render") || f.isPkgSel(x.Fun, outputPkg, "WriteJSON")) && len(x.Args) > 0 {
					sanctioned[x.Args[0]] = true
				}
				for _, p := range []string{"Print", "Printf", "Println"} {
					if f.isPkgSel(x.Fun, "fmt", p) {
						hits = append(hits, f.hit(symbol, x.Pos()))
					}
				}
			}
			if e, ok := n.(ast.Expr); ok && f.isStdout(e) {
				if !sanctioned[e] {
					hits = append(hits, f.hit(symbol, e.Pos()))
				}
				return false
			}
			return true
		})
	}
	return hits
}

func (f *srcFile) isStdout(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.SelectorExpr:
		if x.Sel.Name != "Stdout" {
			return false
		}
		if id, ok := x.X.(*ast.Ident); ok && f.isImportName(id.Name) {
			return f.isPkgSel(x, "os", "Stdout")
		}
		return true
	case *ast.CallExpr:
		sel, ok := x.Fun.(*ast.SelectorExpr)
		return ok && sel.Sel.Name == "OutOrStdout" && len(x.Args) == 0
	}
	return false
}

var usageErrorName = regexp.MustCompile(`(?i)usage\w*err`)

func detectUsageErrorType(f *srcFile) []hit {
	var hits []hit
	for _, decl := range f.ast.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts := spec.(*ast.TypeSpec)
			if usageErrorName.MatchString(ts.Name.Name) {
				hits = append(hits, f.hit(ts.Name.Name, ts.Pos()))
			}
		}
	}
	return hits
}

func TestRules_detectTheirPattern(t *testing.T) {
	cases := []struct {
		rule, path, src string
		want            []string
	}{
		{"request-helper", "commands/x/x.go", `package x
import ("context"; "net/http"; "sync"; "github.com/PollyGlot/google-play-cli/internal/play/api")
var once sync.Once
func do(hc *http.Client, req *http.Request) { hc.Do(req) }
func (c *client) get(req *http.Request) { c.hc.Do(req) }
func ok(ctx context.Context, hc *http.Client) { api.Do(ctx, hc, api.Call{}); once.Do(func() {}) }`,
			[]string{"commands/x/x.go do", "commands/x/x.go client.get"}},
		{"request-helper", "internal/play/api/x.go", `package api
func do(hc *http.Client, req *http.Request) { hc.Do(req) }`, nil},
		{"test-roundtripper", "commands/x/x_test.go", `package x
type rt func(*http.Request) (*http.Response, error)
func (f rt) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
type fake struct{}
func (*fake) RoundTrip(r *http.Request) (*http.Response, error) { return nil, nil }`,
			[]string{"commands/x/x_test.go rt", "commands/x/x_test.go fake"}},
		{"test-roundtripper", "internal/xtest/x.go", `package xtest
type rt struct{}
func (rt) RoundTrip(r *http.Request) (*http.Response, error) { return nil, nil }`,
			[]string{"internal/xtest/x.go rt"}},
		{"test-roundtripper", "internal/transport/retry.go", `package transport
type rt struct{}
func (rt) RoundTrip(r *http.Request) (*http.Response, error) { return nil, nil }`, nil},
		{"test-rsa-keygen", "commands/x/x_test.go", `package x
import (cr "crypto/rand"; r2 "crypto/rsa")
func key() { r2.GenerateKey(cr.Reader, 2048) }`,
			[]string{"commands/x/x_test.go key"}},
		{"http-client", "commands/x/x.go", `package x
import "net/http"
var c = http.Client{}
func mk() { _ = &http.Client{Timeout: 1}; _ = new(http.Client) }`,
			[]string{"commands/x/x.go (package)", "commands/x/x.go mk"}},
		{"http-client", "commands/x/x_test.go", `package x
import "net/http"
func mk() { _ = &http.Client{} }`, nil},
		{"stdout-bypass", "commands/x/x.go", `package x
import ("fmt"; "os"; "github.com/PollyGlot/google-play-cli/internal/output")
func view(rc *kernel.RunContext, name string) { fmt.Fprintf(rc.Stdout, "NAME\t%s\n", name) }
func line(cmd *cobra.Command) { fmt.Fprintln(cmd.OutOrStdout(), "x") }
func raw(b []byte) { os.Stdout.Write(b) }
func say() { fmt.Println("x") }
func ok(rc *kernel.RunContext, cmd *cobra.Command, b *kernel.Boot, r output.Renderers) {
	_ = output.Render(rc.Stdout, rc.Format, r)
	_ = output.WriteJSON(cmd.OutOrStdout(), r)
	b.Stdout = cmd.OutOrStdout()
	b.Stdout, b.Stderr = os.Stdout, os.Stderr
	_ = kernel.Boot{Stdout: os.Stdout}
	fmt.Fprintln(rc.Stderr, "log")
}`,
			[]string{"commands/x/x.go view", "commands/x/x.go line", "commands/x/x.go raw", "commands/x/x.go say"}},
		{"stdout-bypass", "commands/releases/generated/download/x.go", `package download
func dl(rc *kernel.RunContext) { copyTo(rc.Stdout) }`, nil},
		{"usage-error-type", "commands/x/x.go", `package x
type usageError struct{ msg string }
type flagUsageErr = error
type usage struct{}`,
			[]string{"commands/x/x.go usageError", "commands/x/x.go flagUsageErr"}},
	}
	for _, c := range cases {
		t.Run(c.rule+" "+c.path, func(t *testing.T) {
			f, err := parseFile(c.path, []byte(c.src))
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for key := range scan(ruleByID(t, c.rule), []*srcFile{f}) {
				got = append(got, key)
			}
			sort.Strings(got)
			want := append([]string(nil), c.want...)
			sort.Strings(want)
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("keys = %q, want %q", got, want)
			}
		})
	}
}

func TestViolations_bothDirections(t *testing.T) {
	r := ruleByID(t, "request-helper")
	found := map[string]hit{
		"a.go do":    {key: "a.go do", pos: token.Position{Filename: "a.go", Line: 3}},
		"b.go fetch": {key: "b.go fetch", pos: token.Position{Filename: "b.go", Line: 9}},
	}
	allowed := map[string]bool{"a.go do": true, "c.go gone": true}

	got := violations(r, found, allowed)
	if len(got) != 2 {
		t.Fatalf("violations = %q, want one new offender and one stale line", got)
	}
	if !strings.Contains(got[0], "b.go:9:") || !strings.Contains(got[0], "api.Do") {
		t.Errorf("new offender message %q must point at b.go:9 and name api.Do", got[0])
	}
	if !strings.Contains(got[1], `"c.go gone" is no longer an offender`) {
		t.Errorf("stale line message = %q", got[1])
	}
}

func ruleByID(t *testing.T, id string) rule {
	t.Helper()
	for _, r := range rules {
		if r.id == id {
			return r
		}
	}
	t.Fatalf("no rule %q", id)
	return rule{}
}
