package surfaces

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestOnlyHostOpenOpensAClaudeHostConversation(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	checked := 0
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.Contains(path, filepath.Join("internal", "claudehost")) {
				return err
			}
			if filepath.Base(path) == "claude_host_open.go" {
				return nil
			}
			checked++
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			surfacesPackage := strings.Contains(path, filepath.Join("surface", "surfaces"))
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "Open" {
					return true
				}
				if receiver, ok := selector.X.(*ast.SelectorExpr); ok && receiver.Sel.Name == "client" && surfacesPackage {
					t.Errorf("%s opens through the host client instead of hostOpen", path)
				}
				for _, arg := range call.Args {
					if literal, ok := arg.(*ast.CompositeLit); ok {
						if typed, ok := literal.Type.(*ast.SelectorExpr); ok && typed.Sel.Name == "OpenRequest" {
							t.Errorf("%s opens a Claude host conversation without hostOpen", path)
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
