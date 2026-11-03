package surfaces

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

var threadMethods = map[string]string{"thread/start": "codexThreadStart", "thread/resume": "codexThreadResume", "thread/fork": "codexThreadFork"}

func TestOnlyCodexThreadRequestSendsThreadStartResumeOrFork(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	checked := 0
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.Contains(path, filepath.Join("internal", "codextest")) {
				return err
			}
			checked++
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			helper := filepath.Base(path) == "codex_thread_request.go" && strings.Contains(path, filepath.Join("surface", "surfaces"))
			ast.Inspect(file, func(node ast.Node) bool {
				switch n := node.(type) {
				case *ast.BasicLit:
					if value, err := strconv.Unquote(n.Value); err == nil && threadMethods[value] != "" && !helper {
						t.Errorf("%s sends %s without codexThreadRequest", path, value)
					}
				case *ast.CallExpr:
					selector, ok := n.Fun.(*ast.SelectorExpr)
					if !ok || selector.Sel.Name != "Request" || len(n.Args) < 2 || helper {
						return true
					}
					if ident, ok := n.Args[1].(*ast.Ident); ok {
						for _, constant := range threadMethods {
							if ident.Name == constant {
								t.Errorf("%s calls Request(%s) outside codexThreadRequest", path, constant)
							}
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if checked < 100 {
		t.Fatalf("checked only %d files", checked)
	}
}
