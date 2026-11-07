// Command go-test-shard prints a go test -run pattern that selects one shard
// of a package's top-level tests. A test's shard is the FNV-1a hash of its
// name modulo the shard count, so a new test always lands in exactly one
// shard without a hand-kept list. Tests are read from the package's source,
// as go list selects its test files, so nothing in the package runs.
//
//	go run ./scripts/go-test-shard [-names] <shard> <count> <package> [build flags...]
//
// With -names it prints the shard's test names, one per line, instead.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

func main() {
	args := os.Args[1:]
	names := len(args) > 0 && args[0] == "-names"
	if names {
		args = args[1:]
	}
	selected, summary, err := shard(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-test-shard: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, summary)
	if names {
		fmt.Println(strings.Join(selected, "\n"))
		return
	}
	fmt.Println(pattern(selected))
}

func pattern(selected []string) string {
	quoted := make([]string, len(selected))
	for i, name := range selected {
		quoted[i] = regexp.QuoteMeta(name)
	}
	return "^(" + strings.Join(quoted, "|") + ")$"
}

func shard(args []string) ([]string, string, error) {
	if len(args) < 3 {
		return nil, "", errors.New("usage: go-test-shard [-names] <shard> <count> <package> [build flags...]")
	}
	index, err1 := strconv.Atoi(args[0])
	count, err2 := strconv.Atoi(args[1])
	if err1 != nil || err2 != nil || count < 1 || index < 0 || index >= count {
		return nil, "", errors.New("shard and count must be numbers with 0 <= shard < count")
	}
	names, err := packageTests(args[2], args[3:]...)
	if err != nil {
		return nil, "", err
	}
	if len(names) == 0 {
		return nil, "", fmt.Errorf("%s has no tests", args[2])
	}
	selected := shardOf(names, index, count)
	if len(selected) == 0 {
		return nil, "", fmt.Errorf("shard %d of %d selects no tests", index, count)
	}
	return selected, fmt.Sprintf("%d of %d tests in shard %d of %d", len(selected), len(names), index, count), nil
}

func shardOf(names []string, shard, count int) []string {
	var selected []string
	for _, name := range names {
		h := fnv.New32a()
		h.Write([]byte(name))
		if int(h.Sum32()%uint32(count)) == shard {
			selected = append(selected, name)
		}
	}
	return selected
}

// packageTests lists the top-level tests, examples and fuzz targets go test
// would run for the package, from the test files go list selects for this
// platform and the build flags go test is given, such as -race.
func packageTests(pkg string, buildFlags ...string) ([]string, error) {
	args := append(append([]string{"list", "-json"}, buildFlags...), pkg)
	out, err := exec.Command("go", args...).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, fmt.Errorf("go list %s: %s", pkg, strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, err
	}
	var listed struct {
		Dir          string
		TestGoFiles  []string
		XTestGoFiles []string
	}
	if err := json.Unmarshal(out, &listed); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var names []string
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, file := range append(listed.TestGoFiles, listed.XTestGoFiles...) {
		parsed, err := parser.ParseFile(fset, filepath.Join(listed.Dir, file), nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		files = append(files, parsed)
		for _, decl := range parsed.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && isTestFunc(fn) {
				add(fn.Name.Name)
			}
		}
	}
	// go test runs only the examples that have an output comment.
	for _, example := range doc.Examples(files...) {
		if example.Output != "" || example.EmptyOutput {
			add("Example" + example.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// isTestFunc follows go test's rules for TestXxx(*testing.T) and
// FuzzXxx(*testing.F), where Xxx does not start with a lower-case letter.
func isTestFunc(fn *ast.FuncDecl) bool {
	name := fn.Name.Name
	params := fn.Type.Params.List
	switch {
	case hasPrefix(name, "Test"):
		return len(params) == 1 && isPointerTo(params[0].Type, "T")
	case hasPrefix(name, "Fuzz"):
		return len(params) == 1 && isPointerTo(params[0].Type, "F")
	}
	return false
}

func hasPrefix(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	if len(name) == len(prefix) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(name[len(prefix):])
	return !unicode.IsLower(r)
}

func isPointerTo(expr ast.Expr, typ string) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	switch x := star.X.(type) {
	case *ast.SelectorExpr:
		return x.Sel.Name == typ
	case *ast.Ident:
		// testing imported with a dot.
		return x.Name == typ
	}
	return false
}
