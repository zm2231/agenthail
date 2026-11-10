package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

func main() {
	patterns := os.Args[1:]
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	found, err := darwinPackages(patterns)
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-darwin-packages: %v\n", err)
		os.Exit(1)
	}
	if len(found) == 0 {
		fmt.Fprintln(os.Stderr, "go-darwin-packages: no package has a macOS code path")
		os.Exit(1)
	}
	for _, pkg := range found {
		fmt.Fprintf(os.Stderr, "%s: %s\n", pkg.ImportPath, strings.Join(pkg.Reasons, "; "))
		fmt.Println(pkg.ImportPath)
	}
}

type darwinPackage struct {
	ImportPath string
	Reasons    []string
}

type listedPackage struct {
	ImportPath   string
	Dir          string
	GoFiles      []string
	CgoFiles     []string
	TestGoFiles  []string
	XTestGoFiles []string
	Error        *struct{ Err string }
}

func (p listedPackage) files() []string {
	files := slices.Concat(p.GoFiles, p.CgoFiles, p.TestGoFiles, p.XTestGoFiles)
	sort.Strings(files)
	return files
}

func darwinPackages(patterns []string) ([]darwinPackage, error) {
	darwin, err := listPackages("darwin", patterns)
	if err != nil {
		return nil, err
	}
	linux, err := listPackages("linux", patterns)
	if err != nil {
		return nil, err
	}
	var found []darwinPackage
	for _, pkg := range darwin {
		var reasons []string
		files := pkg.files()
		if len(files) == 0 {
			continue
		}
		if only := difference(files, linux[pkg.ImportPath].files()); len(only) > 0 {
			reasons = append(reasons, "macOS-only files "+strings.Join(only, ", "))
		}
		readers, err := goosReaders(pkg.Dir, files)
		if err != nil {
			return nil, err
		}
		if len(readers) > 0 {
			reasons = append(reasons, "runtime.GOOS in "+strings.Join(readers, ", "))
		}
		if len(reasons) > 0 {
			found = append(found, darwinPackage{ImportPath: pkg.ImportPath, Reasons: reasons})
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].ImportPath < found[j].ImportPath })
	return found, nil
}

// cgo stays on for both systems so only the system decides which files build.
func listPackages(goos string, patterns []string) (map[string]listedPackage, error) {
	cmd := exec.Command("go", append([]string{"list", "-e", "-json=ImportPath,Dir,GoFiles,CgoFiles,TestGoFiles,XTestGoFiles,Error"}, patterns...)...)
	cmd.Env = append(os.Environ(), "GOOS="+goos, "CGO_ENABLED=1")
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, fmt.Errorf("go list for %s: %s", goos, strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, err
	}
	packages := map[string]listedPackage{}
	decoder := json.NewDecoder(strings.NewReader(string(out)))
	for {
		var pkg listedPackage
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if pkg.Error != nil && len(pkg.files()) > 0 {
			return nil, fmt.Errorf("go list %s for %s: %s", pkg.ImportPath, goos, pkg.Error.Err)
		}
		packages[pkg.ImportPath] = pkg
	}
	return packages, nil
}

func difference(files, other []string) []string {
	var only []string
	for _, file := range files {
		if !slices.Contains(other, file) {
			only = append(only, file)
		}
	}
	return only
}

func goosReaders(dir string, files []string) ([]string, error) {
	fset := token.NewFileSet()
	var readers []string
	for _, file := range files {
		parsed, err := parser.ParseFile(fset, filepath.Join(dir, file), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		if readsGOOS(parsed) {
			readers = append(readers, file)
		}
	}
	return readers, nil
}

func readsGOOS(file *ast.File) bool {
	names := map[string]bool{}
	for _, spec := range file.Imports {
		if path, _ := strconv.Unquote(spec.Path.Value); path != "runtime" {
			continue
		}
		switch {
		case spec.Name == nil:
			names["runtime"] = true
		case spec.Name.Name == ".":
			return dotReadsGOOS(file)
		default:
			names[spec.Name.Name] = true
		}
	}
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		if sel, ok := node.(*ast.SelectorExpr); ok && sel.Sel.Name == "GOOS" {
			if x, ok := sel.X.(*ast.Ident); ok && names[x.Name] {
				found = true
			}
		}
		return !found
	})
	return found
}

func dotReadsGOOS(file *ast.File) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		if id, ok := node.(*ast.Ident); ok && id.Name == "GOOS" {
			found = true
		}
		return !found
	})
	return found
}
