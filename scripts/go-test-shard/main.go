// Command go-test-shard prints a go test -run pattern that selects one shard
// of a package's top-level tests. Tests are read from the package's source, as
// go list selects its test files, so nothing in the package runs.
//
//	go run ./scripts/go-test-shard [-names] [-seconds <file>] <shard> <count> <package> [build flags...]
//	go run ./scripts/go-test-shard -record <package import path> < go-test.json > <file>
//
// With -seconds, tests are dealt longest first to the shard with the least
// time so far, using the seconds the file records for each test; a test the
// file lacks counts as the file's median. Without it, a test's shard is the
// FNV-1a hash of its name modulo the count. Either way every test lands in
// exactly one shard without a hand-kept list. With -names it prints the
// shard's test names, one per line, instead. -record reads go test -json
// output and writes the seconds file for one package.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"hash/fnv"
	"io"
	"math"
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
	if len(args) == 2 && args[0] == "-record" {
		if err := record(os.Stdin, os.Stdout, args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "go-test-shard: %v\n", err)
			os.Exit(1)
		}
		return
	}
	names := len(args) > 0 && args[0] == "-names"
	if names {
		args = args[1:]
	}
	var seconds map[string]float64
	if len(args) > 1 && args[0] == "-seconds" {
		var err error
		if seconds, err = readSeconds(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "go-test-shard: %v\n", err)
			os.Exit(1)
		}
		args = args[2:]
	}
	selected, summary, err := shard(args, seconds)
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

func shard(args []string, seconds map[string]float64) ([]string, string, error) {
	if len(args) < 3 {
		return nil, "", errors.New("usage: go-test-shard [-names] [-seconds <file>] <shard> <count> <package> [build flags...]")
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
	var selected []string
	summary := fmt.Sprintf("tests in shard %d of %d", index, count)
	if seconds == nil {
		selected = shardOf(names, index, count)
	} else {
		var total float64
		selected, total = balancedShardOf(names, seconds, index, count)
		summary += fmt.Sprintf(", about %.0fs by the recorded seconds", total)
	}
	if len(selected) == 0 {
		return nil, "", fmt.Errorf("shard %d of %d selects no tests", index, count)
	}
	return selected, fmt.Sprintf("%d of %d %s", len(selected), len(names), summary), nil
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

// Ties go by name, then to the lowest-numbered shard, so every shard computes the same deal.
func balancedShardOf(names []string, seconds map[string]float64, shard, count int) ([]string, float64) {
	fallback := median(seconds)
	weight := func(name string) float64 {
		if s, ok := seconds[name]; ok {
			return s
		}
		return fallback
	}
	order := append([]string(nil), names...)
	sort.Slice(order, func(i, j int) bool {
		wi, wj := weight(order[i]), weight(order[j])
		if wi != wj {
			return wi > wj
		}
		return order[i] < order[j]
	})
	totals := make([]float64, count)
	var selected []string
	for _, name := range order {
		least := 0
		for i := range totals {
			if totals[i] < totals[least] {
				least = i
			}
		}
		totals[least] += weight(name)
		if least == shard {
			selected = append(selected, name)
		}
	}
	sort.Strings(selected)
	return selected, totals[shard]
}

func median(seconds map[string]float64) float64 {
	if len(seconds) == 0 {
		return 1
	}
	values := make([]float64, 0, len(seconds))
	for _, s := range seconds {
		values = append(values, s)
	}
	sort.Float64s(values)
	return values[len(values)/2]
}

func readSeconds(path string) (map[string]float64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	seconds := map[string]float64{}
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		value, name, ok := strings.Cut(scanner.Text(), "\t")
		s, err := strconv.ParseFloat(value, 64)
		if !ok || err != nil || name == "" || strings.ContainsRune(name, '\t') || math.IsNaN(s) || math.IsInf(s, 0) || s < 0 {
			return nil, fmt.Errorf("%s:%d: want <seconds>\\t<test name>", path, line)
		}
		seconds[name] = s
	}
	return seconds, scanner.Err()
}

func record(in io.Reader, out io.Writer, pkg string) error {
	type event struct {
		Action  string
		Package string
		Test    string
		Elapsed float64
	}
	seconds := map[string]float64{}
	decoder := json.NewDecoder(in)
	for {
		var e event
		if err := decoder.Decode(&e); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		if e.Package == pkg && e.Test != "" && !strings.Contains(e.Test, "/") && (e.Action == "pass" || e.Action == "fail") {
			seconds[e.Test] = e.Elapsed
		}
	}
	if len(seconds) == 0 {
		return fmt.Errorf("no test of %s passed or failed in the input", pkg)
	}
	names := make([]string, 0, len(seconds))
	for name := range seconds {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if seconds[names[i]] != seconds[names[j]] {
			return seconds[names[i]] > seconds[names[j]]
		}
		return names[i] < names[j]
	})
	w := bufio.NewWriter(out)
	for _, name := range names {
		fmt.Fprintf(w, "%.2f\t%s\n", seconds[name], name)
	}
	return w.Flush()
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
