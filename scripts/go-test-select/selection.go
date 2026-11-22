package main

import (
	"fmt"
	"path"
	"sort"
)

type graph struct {
	module   string
	packages map[string]string
	deps     map[string]map[string]bool
	tests    map[string]map[string]bool
}

type selection struct {
	all      bool
	reason   string
	packages []string
	suites   []string
}

func (g graph) importPath(dir string) string {
	if dir == "." || dir == "" {
		return g.module
	}
	return g.module + "/" + dir
}

func (g graph) packageOf(file string) (string, bool) {
	for dir := path.Dir(file); ; dir = path.Dir(dir) {
		if pkg, ok := g.packages[dir]; ok {
			return pkg, true
		}
		if dir == "." || dir == "/" {
			return "", false
		}
	}
}

func everything(g graph, inputs []input, reason string) selection {
	s := selection{all: true, reason: reason}
	for pkg := range g.tests {
		s.packages = append(s.packages, pkg)
	}
	suites := map[string]bool{}
	for _, in := range inputs {
		if in.scope == scopeSuite && !suites[in.target] {
			suites[in.target] = true
			s.suites = append(s.suites, in.target)
		}
	}
	sort.Strings(s.packages)
	sort.Strings(s.suites)
	return s
}

func selectTests(g graph, inputs []input, changed []string) (selection, error) {
	changedPackages := map[string]bool{}
	testsOnly := map[string]bool{}
	suites := map[string]bool{}
	for _, file := range changed {
		pkg, inPackage := g.packageOf(file)
		if inPackage {
			changedPackages[pkg] = true
		}
		matched := false
		for _, in := range inputs {
			if !match(in.pattern, file) {
				continue
			}
			switch in.scope {
			case scopeAll:
				return everything(g, inputs, fmt.Sprintf("%s changed (inputs line %d)", file, in.line)), nil
			case scopeNone:
				matched = true
			case scopePackage:
				matched = true
				changedPackages[g.importPath(in.target)] = true
			case scopeTests:
				matched = true
				testsOnly[g.importPath(in.target)] = true
			case scopeSuite:
				matched = true
				suites[in.target] = true
			}
		}
		if !inPackage && !matched {
			return everything(g, inputs, file+" is in no package and no inputs line names it"), nil
		}
	}
	for _, in := range inputs {
		if in.scope != scopeBuild {
			continue
		}
		built := g.importPath(in.pattern)
		if _, ok := g.deps[built]; !ok {
			return selection{}, fmt.Errorf("inputs line %d: %s is not a package", in.line, in.pattern)
		}
		for dep := range g.deps[built] {
			if changedPackages[dep] {
				testsOnly[g.importPath(in.target)] = true
			}
		}
	}
	var s selection
	for pkg, deps := range g.tests {
		hit := testsOnly[pkg]
		for dep := range deps {
			hit = hit || changedPackages[dep]
		}
		if hit {
			s.packages = append(s.packages, pkg)
		}
	}
	for suite := range suites {
		s.suites = append(s.suites, suite)
	}
	sort.Strings(s.packages)
	sort.Strings(s.suites)
	return s, nil
}
