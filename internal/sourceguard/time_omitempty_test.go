package sourceguard

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func timeFieldsWithOmitempty(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".worktrees", "node_modules", "runtime", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		timePackage := importName(file, "time")
		if timePackage == "" {
			return nil
		}
		ast.Inspect(file, func(node ast.Node) bool {
			field, ok := node.(*ast.Field)
			if !ok || field.Tag == nil || !isTimeType(field.Type, timePackage) {
				return true
			}
			tag, err := strconv.Unquote(field.Tag.Value)
			if err != nil {
				return true
			}
			name, options, _ := strings.Cut(reflect.StructTag(tag).Get("json"), ",")
			if name != "-" && slices.Contains(strings.Split(options, ","), "omitempty") {
				found = append(found, fmt.Sprintf("%s:%d", path, fset.Position(field.Pos()).Line))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func importName(file *ast.File, path string) string {
	for _, spec := range file.Imports {
		if spec.Path.Value != strconv.Quote(path) {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return path
	}
	return ""
}

func isTimeType(expr ast.Expr, timePackage string) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Time" {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == timePackage
}

func TestTimeFieldsDropZeroWithOmitzero(t *testing.T) {
	for _, field := range timeFieldsWithOmitempty(t, filepath.Join("..", "..")) {
		t.Errorf("%s tags a time.Time omitempty, which still sends a zero time as 0001-01-01; use omitzero", field)
	}
}

func TestTheOmitemptyTimeGuardFindsAliasedAndPointerFields(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "internal", "thing")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := "package thing\n\nimport clock \"time\"\n\ntype row struct {\n\tA clock.Time `json:\"a,omitempty\"`\n\tB *clock.Time `json:\"b,omitempty\"`\n\tC clock.Time `json:\"c,omitzero\"`\n\tD string `json:\"d,omitempty\"`\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "row.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	got := timeFieldsWithOmitempty(t, root)
	want := []string{filepath.Join(dir, "row.go") + ":6", filepath.Join(dir, "row.go") + ":7"}
	if !slices.Equal(got, want) {
		t.Fatalf("found=%v want %v", got, want)
	}
}
