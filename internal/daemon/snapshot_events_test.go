package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestEventsThatChangeTheSnapshotDropItsCache(t *testing.T) {
	d, _, _, _, _, _ := baseFixture(t)
	for _, event := range snapshotEvents {
		name := event.Event
		if event.Family {
			name += "example"
		}
		before := d.dashboard.Load().stateVersion.Load()
		d.publishEvent(name, "", nil)
		if dropped := d.dashboard.Load().stateVersion.Load() != before; dropped != event.Cache {
			t.Fatalf("%s dropped the cached snapshot: %v, classified %v", name, dropped, event.Cache)
		}
	}
}

func TestEveryPublishedEventIsClassifiedForTheSnapshot(t *testing.T) {
	emitted := publishedEventNames(t)
	if len(emitted) == 0 {
		t.Fatal("found no published events; the scan is broken")
	}
	used := map[string]bool{}
	for _, name := range emitted {
		event, found := classifySnapshotEvent(strings.TrimSuffix(name, "*"))
		if !found || strings.HasSuffix(name, "*") && !event.Family {
			t.Errorf("%s is published but not classified in snapshot_events.json", name)
			continue
		}
		used[event.Event] = true
	}
	for _, event := range snapshotEvents {
		if !used[event.Event] {
			t.Errorf("%s is classified but nothing publishes it", event.Event)
		}
		if strings.TrimSpace(event.Why) == "" {
			t.Errorf("%s has no reason", event.Event)
		}
	}
}

func publishedEventNames(t *testing.T) []string {
	t.Helper()
	constants := packageConstants(t, ".")
	files := parseDir(t, ".")
	var names []string
	resolve := func(file *ast.File, expr ast.Expr) {
		switch value := expr.(type) {
		case *ast.BasicLit:
			names = append(names, unquote(t, value))
		case *ast.Ident:
			name, found := constants[value.Name]
			if !found {
				t.Fatalf("event name %s is not a package constant", value.Name)
			}
			names = append(names, name)
		case *ast.SelectorExpr:
			names = append(names, importedConstant(t, file, value))
		case *ast.BinaryExpr:
			prefix, ok := value.X.(*ast.BasicLit)
			if !ok || value.Op != token.ADD {
				t.Fatalf("cannot read the event name built at %v", value.Pos())
			}
			names = append(names, unquote(t, prefix)+"*")
		default:
			t.Fatalf("cannot read the event name %T", expr)
		}
	}
	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.CallExpr:
				if selector, ok := value.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "publishEvent" && len(value.Args) > 0 {
					resolve(file, value.Args[0])
				}
			case *ast.CompositeLit:
				if kind, ok := value.Type.(*ast.Ident); ok && kind.Name == "apiEvent" {
					for _, element := range value.Elts {
						if field, ok := element.(*ast.KeyValueExpr); ok && isIdent(field.Key, "Type") {
							if literal, ok := field.Value.(*ast.BasicLit); ok {
								resolve(file, literal)
							}
						}
					}
				}
			}
			return true
		})
	}
	return names
}

func parseDir(t *testing.T, dir string) []*ast.File {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	return files
}

func packageConstants(t *testing.T, dir string) map[string]string {
	t.Helper()
	constants := map[string]string{}
	for _, file := range parseDir(t, dir) {
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.CONST {
				continue
			}
			for _, spec := range general.Specs {
				values := spec.(*ast.ValueSpec)
				for index, name := range values.Names {
					if index < len(values.Values) {
						if literal, ok := values.Values[index].(*ast.BasicLit); ok && literal.Kind == token.STRING {
							constants[name.Name] = unquote(t, literal)
						}
					}
				}
			}
		}
	}
	return constants
}

func importedConstant(t *testing.T, file *ast.File, selector *ast.SelectorExpr) string {
	t.Helper()
	pkg, ok := selector.X.(*ast.Ident)
	if !ok {
		t.Fatalf("cannot read the event name at %v", selector.Pos())
	}
	for _, spec := range file.Imports {
		path, _ := strconv.Unquote(spec.Path.Value)
		if filepath.Base(path) != pkg.Name || !strings.HasPrefix(path, "github.com/zm2231/agenthail/") {
			continue
		}
		dir := filepath.Join("..", "..", strings.TrimPrefix(path, "github.com/zm2231/agenthail/"))
		if _, err := os.Stat(dir); err != nil {
			t.Fatal(err)
		}
		if name, found := packageConstants(t, dir)[selector.Sel.Name]; found {
			return name
		}
	}
	t.Fatalf("event name %s.%s is not a constant this test can read", pkg.Name, selector.Sel.Name)
	return ""
}

func isIdent(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name
}

func unquote(t *testing.T, literal *ast.BasicLit) string {
	t.Helper()
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
