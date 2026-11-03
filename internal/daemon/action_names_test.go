package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var actionRequestTypes = map[string]bool{"dashboardActionRequest": true, "projectRequest": true, "coordinatorRequest": true}

func dispatchedActionNames(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, name, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !dispatchesActions(fn) {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.BinaryExpr:
					if node.Op == token.EQL || node.Op == token.NEQ {
						if isActionExpr(node.X) {
							addLiteral(names, node.Y)
						} else if isActionExpr(node.Y) {
							addLiteral(names, node.X)
						}
					}
				case *ast.SwitchStmt:
					if node.Tag != nil && isActionExpr(node.Tag) {
						for _, stmt := range node.Body.List {
							for _, value := range stmt.(*ast.CaseClause).List {
								addLiteral(names, value)
							}
						}
					}
				}
				return true
			})
		}
	}
	list := make([]string, 0, len(names))
	for name := range names {
		list = append(list, name)
	}
	sort.Strings(list)
	return list
}

func dispatchesActions(fn *ast.FuncDecl) bool {
	for _, field := range fn.Type.Params.List {
		if ident, ok := field.Type.(*ast.Ident); ok {
			if actionRequestTypes[ident.Name] {
				return true
			}
			if ident.Name == "string" {
				for _, name := range field.Names {
					if name.Name == "action" {
						return true
					}
				}
			}
		}
	}
	return false
}

func isActionExpr(expr ast.Expr) bool {
	switch expr := expr.(type) {
	case *ast.SelectorExpr:
		receiver, ok := expr.X.(*ast.Ident)
		return ok && receiver.Name == "request" && expr.Sel.Name == "Action"
	case *ast.Ident:
		return expr.Name == "action"
	}
	return false
}

func addLiteral(names map[string]bool, expr ast.Expr) {
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return
	}
	if value, err := strconv.Unquote(literal.Value); err == nil && value != "" {
		names[value] = true
	}
}

func TestDispatchedActionNamesAreFound(t *testing.T) {
	names := dispatchedActionNames(t)
	t.Logf("%d actions: %s", len(names), strings.Join(names, " "))
	for _, want := range []string{"send", "steer", "respond", "session-create", "session-fork", "coordinator-send", "project-add", "channel-send", "relay-add", "goal-set"} {
		if sort.SearchStrings(names, want) == len(names) || names[sort.SearchStrings(names, want)] != want {
			t.Fatalf("the scan missed %q; it found %v", want, names)
		}
	}
}
