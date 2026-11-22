// Command go-test-select prints the packages whose tests a change can reach, one import path per line, and each named suite as suite:<name>.
//
//	go run ./scripts/go-test-select -base <rev>
//	go run ./scripts/go-test-select -all
//
// A changed file inside a package directory changes that package; inputs.tsv names everything else. With -all, or when a changed file is an
// all input or is named nowhere, it prints every package with tests and every suite. It runs from the module root.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const inputsFile = "scripts/go-test-select/inputs.tsv"

func main() {
	base := flag.String("base", "", "the revision to diff HEAD against, from their merge base")
	all := flag.Bool("all", false, "select every package and suite")
	flag.Parse()
	if err := run(os.Stdout, os.Stderr, *base, *all); err != nil {
		fmt.Fprintf(os.Stderr, "go-test-select: %v\n", err)
		os.Exit(1)
	}
}

func run(stdout, stderr io.Writer, base string, all bool) error {
	if base == "" && !all {
		return errors.New("give -base <rev> or -all")
	}
	g, err := loadGraph(".")
	if err != nil {
		return err
	}
	file, err := os.Open(inputsFile)
	if err != nil {
		return err
	}
	defer file.Close()
	inputs, err := readInputs(file)
	if err != nil {
		return err
	}
	var s selection
	if all {
		s = everything(g, inputs, "-all")
	} else {
		changed, err := changedFiles(base)
		if err != nil {
			return err
		}
		if s, err = selectTests(g, inputs, changed); err != nil {
			return err
		}
	}
	if s.all {
		fmt.Fprintf(stderr, "every package: %s\n", s.reason)
	}
	for _, pkg := range s.packages {
		fmt.Fprintln(stdout, pkg)
	}
	for _, suite := range s.suites {
		fmt.Fprintf(stdout, "suite:%s\n", suite)
	}
	return nil
}

func changedFiles(base string) ([]string, error) {
	mergeBase, err := output("git", "merge-base", base, "HEAD")
	if err != nil {
		return nil, err
	}
	names, err := output("git", "diff", "--name-only", "--no-renames", strings.TrimSpace(string(mergeBase)), "HEAD")
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(names)), nil
}

type listed struct {
	ImportPath string
	Dir        string
	Deps       []string
	ForTest    string
	Module     *struct{ Path string }
}

func loadGraph(root string) (graph, error) {
	module, err := output("go", "-C", root, "list", "-m")
	if err != nil {
		return graph{}, err
	}
	out, err := output("go", "-C", root, "list", "-deps", "-test", "-json=ImportPath,Dir,Deps,ForTest,Module", "./...")
	if err != nil {
		return graph{}, err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return graph{}, err
	}
	g := graph{module: strings.TrimSpace(string(module)), packages: map[string]string{}, deps: map[string]map[string]bool{}, tests: map[string]map[string]bool{}}
	for decoder := json.NewDecoder(bytes.NewReader(out)); decoder.More(); {
		var p listed
		if err := decoder.Decode(&p); err != nil {
			return graph{}, err
		}
		if p.Module == nil || p.Module.Path != g.module {
			continue
		}
		if pkg, ok := strings.CutSuffix(p.ImportPath, ".test"); ok {
			g.tests[pkg] = withSelf(pkg, p.Deps)
			continue
		}
		if p.ForTest != "" {
			continue
		}
		rel, err := filepath.Rel(absRoot, p.Dir)
		if err != nil {
			return graph{}, err
		}
		g.packages[filepath.ToSlash(rel)] = p.ImportPath
		g.deps[p.ImportPath] = withSelf(p.ImportPath, p.Deps)
	}
	return g, nil
}

func withSelf(self string, deps []string) map[string]bool {
	set := map[string]bool{self: true}
	for _, dep := range deps {
		name, _, _ := strings.Cut(dep, " ")
		set[name] = true
	}
	return set
}

func output(name string, args ...string) ([]byte, error) {
	var stderr bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
