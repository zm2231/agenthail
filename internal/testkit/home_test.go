package testkit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(RunInOwnHome(m.Run))
}

func TestTheTestsRunWithoutThePersonsHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(home), "agenthail-test-home-") || os.Getenv("CFFIXED_USER_HOME") != home || os.Getenv("CODEX_HOME") != "" {
		t.Fatalf("HOME=%s CFFIXED_USER_HOME=%s CODEX_HOME=%s", home, os.Getenv("CFFIXED_USER_HOME"), os.Getenv("CODEX_HOME"))
	}
}

func TestEveryTestPackageRunsInItsOwnHome(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	var missing []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() && path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "node_modules" || name == "runtime") {
			return filepath.SkipDir
		}
		if !entry.IsDir() {
			return nil
		}
		tests, _ := filepath.Glob(filepath.Join(path, "*_test.go"))
		if len(tests) > 0 && !slices.ContainsFunc(tests, runsInOwnHome) {
			relative, _ := filepath.Rel(root, path)
			missing = append(missing, relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) > 0 {
		t.Fatalf("these test packages have no TestMain that calls testkit.RunInOwnHome, so their tests can reach the person's own HOME and daemon: %v", missing)
	}
}

func runsInOwnHome(path string) bool {
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return false
	}
	found := false
	for _, decl := range parsed.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if !ok || function.Name.Name != "TestMain" || function.Recv != nil {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.SelectorExpr:
				found = found || fun.Sel.Name == "RunInOwnHome"
			case *ast.Ident:
				found = found || fun.Name == "RunInOwnHome"
			}
			return true
		})
	}
	return found
}
