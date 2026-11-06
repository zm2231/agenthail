package testkit

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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

func walkModule(t *testing.T, visit func(root, path string, entry fs.DirEntry)) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() && path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "node_modules" || name == "runtime") {
			return filepath.SkipDir
		}
		visit(root, path, entry)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEveryTestPackageRunsInItsOwnHome(t *testing.T) {
	var missing []string
	walkModule(t, func(root, path string, entry fs.DirEntry) {
		if !entry.IsDir() {
			return
		}
		tests, _ := filepath.Glob(filepath.Join(path, "*_test.go"))
		tests = slices.DeleteFunc(tests, func(test string) bool {
			built, err := build.Default.MatchFile(path, filepath.Base(test))
			return err != nil || !built
		})
		if len(tests) > 0 && !slices.ContainsFunc(tests, runsInOwnHome) {
			relative, _ := filepath.Rel(root, path)
			missing = append(missing, relative)
		}
	})
	if len(missing) > 0 {
		t.Fatalf("these test packages have no TestMain that calls testkit.RunInOwnHome, so their tests can reach the person's own HOME and daemon: %v", missing)
	}
}

var firstImport = regexp.MustCompile(`(?m)^import\s+(?:[^'"]*?\s+from\s+)?['"]([^'"]+)['"]`)

func TestEveryNodeTestRunsInItsOwnHome(t *testing.T) {
	var missing []string
	entries := 0
	walkModule(t, func(root, path string, entry fs.DirEntry) {
		name := entry.Name()
		inTests := filepath.Base(filepath.Dir(path)) == "test" && (strings.HasSuffix(name, ".test.mjs") || strings.HasSuffix(name, ".test.js"))
		inScripts := filepath.Dir(path) == filepath.Join(root, "scripts") && strings.HasPrefix(name, "test-") && strings.HasSuffix(name, ".mjs") && name != "test-home.mjs"
		if entry.IsDir() || (!inTests && !inScripts) {
			return
		}
		entries++
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		relative, _ := filepath.Rel(root, path)
		match := firstImport.FindSubmatch(source)
		if match == nil || filepath.Clean(filepath.Join(filepath.Dir(path), string(match[1]))) != filepath.Join(root, "scripts", "test-home.mjs") {
			missing = append(missing, relative)
		}
	})
	if entries == 0 {
		t.Fatal("found no Node test files")
	}
	if len(missing) > 0 {
		t.Fatalf("these Node tests do not import scripts/test-home.mjs first, so they can reach the person's own HOME and daemon: %v", missing)
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
