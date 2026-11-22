package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type read struct {
	at       string
	owner    string
	test     bool
	resolved string
	dir      bool
}

// reads finds the paths a .go file reaches through "../" literals or filepath.Join("..", ...), taken from its package directory.
func reads(root string) ([]read, error) {
	var found []read
	err := filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if file != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(file, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		owner := path.Dir(filepath.ToSlash(rel))
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			return err
		}
		for _, relative := range relativePaths(parsed) {
			resolved := path.Clean(path.Join(owner, relative.path))
			if i := strings.IndexAny(resolved, "*?["); i >= 0 {
				resolved = path.Dir(resolved[:i])
			}
			if resolved == ".." || strings.HasPrefix(resolved, "../") || resolved == owner || strings.HasPrefix(resolved, owner+"/") {
				continue
			}
			if resolved == "." {
				resolved = ""
			}
			info, err := os.Stat(filepath.Join(root, filepath.FromSlash(resolved)))
			if err != nil {
				continue
			}
			found = append(found, read{
				at:       fmt.Sprintf("%s:%d", filepath.ToSlash(rel), fset.Position(relative.pos).Line),
				owner:    owner,
				test:     strings.HasSuffix(file, "_test.go"),
				resolved: resolved,
				dir:      info.IsDir(),
			})
		}
		return nil
	})
	return found, err
}

type relativePath struct {
	path string
	pos  token.Pos
}

func relativePaths(file *ast.File) []relativePath {
	var paths []relativePath
	joined := map[*ast.BasicLit]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CallExpr:
			selector, ok := n.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Join" {
				return true
			}
			if x, ok := selector.X.(*ast.Ident); !ok || (x.Name != "filepath" && x.Name != "path") {
				return true
			}
			var parts []string
			var pos token.Pos
			for _, arg := range n.Args {
				value, lit := stringLiteral(arg)
				if lit != nil {
					joined[lit] = true
				}
				if len(parts) == 0 && value != ".." {
					continue
				}
				if lit == nil {
					break
				}
				if len(parts) == 0 {
					pos = lit.Pos()
				}
				parts = append(parts, value)
			}
			if len(parts) > 0 {
				paths = append(paths, relativePath{path.Join(parts...), pos})
			}
		case *ast.BasicLit:
			if value, lit := stringLiteral(n); lit != nil && !joined[lit] && strings.HasPrefix(value, "../") && value != "../" {
				paths = append(paths, relativePath{value, lit.Pos()})
			}
		}
		return true
	})
	return paths
}

func stringLiteral(expr ast.Expr) (string, *ast.BasicLit) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", nil
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", nil
	}
	return value, lit
}

func uncovered(root string, g graph, inputs []input) ([]string, int, error) {
	found, err := reads(root)
	if err != nil {
		return nil, 0, err
	}
	var missing []string
	for _, r := range found {
		pkg := g.importPath(r.owner)
		deps := g.deps[pkg]
		if r.test {
			deps = g.tests[pkg]
		}
		probe := r.resolved
		if r.dir {
			probe = path.Join(r.resolved, "x")
		}
		if reached, ok := g.packageOf(probe); ok && deps[reached] {
			continue
		}
		if !declared(inputs, r) {
			missing = append(missing, fmt.Sprintf("%s reads %s, and no inputs line gives it to %s", r.at, displayed(r.resolved), r.owner))
		}
	}
	return missing, len(found), nil
}

func declared(inputs []input, r read) bool {
	for _, in := range inputs {
		if in.target != r.owner {
			continue
		}
		switch in.scope {
		case scopePackage, scopeBuild, scopeImports:
		case scopeTests:
			if !r.test {
				continue
			}
		default:
			continue
		}
		if covers(in.pattern, r.resolved, r.dir) {
			return true
		}
	}
	return false
}

func displayed(resolved string) string {
	if resolved == "" {
		return "the repository root"
	}
	return resolved
}

func loadInputs(t *testing.T, file string) []input {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	inputs, err := readInputs(f)
	if err != nil {
		t.Fatal(err)
	}
	return inputs
}

func TestEveryPathAGoFileReachesOutsideItsPackageIsAnInput(t *testing.T) {
	root := filepath.Join("..", "..")
	g, err := loadGraph(root)
	if err != nil {
		t.Fatal(err)
	}
	missing, checked, err := uncovered(root, g, loadInputs(t, filepath.Join(root, inputsFile)))
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("found no reads outside a package; the scan is broken")
	}
	for _, m := range missing {
		t.Error(m + "; add it to " + inputsFile)
	}
}
