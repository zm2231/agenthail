package main

import (
	"bufio"
	"fmt"
	"io"
	"path"
	"strings"
)

type scope string

const (
	scopeAll     scope = "all"
	scopeNone    scope = "none"
	scopePackage scope = "package"
	scopeTests   scope = "tests"
	scopeBuild   scope = "build"
	scopeSuite   scope = "suite"
	scopeImports scope = "imports"
)

type input struct {
	pattern string
	scope   scope
	target  string
	line    int
}

func readInputs(r io.Reader) ([]input, error) {
	var inputs []input
	scanner := bufio.NewScanner(r)
	for n := 1; scanner.Scan(); n++ {
		text := scanner.Text()
		if strings.TrimSpace(text) == "" {
			continue
		}
		fields := strings.Split(text, "\t")
		if len(fields) != 3 {
			return nil, fmt.Errorf("inputs line %d: want pattern, scope and target separated by tabs, got %q", n, text)
		}
		in := input{pattern: fields[0], scope: scope(fields[1]), target: fields[2], line: n}
		switch in.scope {
		case scopeAll, scopeNone:
			if in.target != "-" {
				return nil, fmt.Errorf("inputs line %d: scope %s takes target -", n, in.scope)
			}
		case scopePackage, scopeTests, scopeBuild, scopeSuite, scopeImports:
			if in.target == "-" || in.target == "" {
				return nil, fmt.Errorf("inputs line %d: scope %s needs a target", n, in.scope)
			}
		default:
			return nil, fmt.Errorf("inputs line %d: unknown scope %q", n, in.scope)
		}
		if _, err := path.Match(strings.TrimPrefix(strings.TrimSuffix(in.pattern, "/**"), "**/"), ""); err != nil {
			return nil, fmt.Errorf("inputs line %d: bad pattern %q: %v", n, in.pattern, err)
		}
		inputs = append(inputs, in)
	}
	return inputs, scanner.Err()
}

// match reports whether a slash path matches a pattern: path.Match, plus dir/** for everything under dir and **/name for name in any directory.
func match(pattern, name string) bool {
	switch {
	case pattern == "**":
		return true
	case strings.HasSuffix(pattern, "/**"):
		dir := strings.TrimSuffix(pattern, "/**")
		return name == dir || strings.HasPrefix(name, dir+"/")
	case strings.HasPrefix(pattern, "**/"):
		ok, _ := path.Match(strings.TrimPrefix(pattern, "**/"), path.Base(name))
		return ok
	}
	ok, _ := path.Match(pattern, name)
	return ok
}

// covers reports whether a pattern declares a read of name; a directory read is declared by any pattern inside it.
func covers(pattern, name string, dir bool) bool {
	if match(pattern, name) {
		return true
	}
	if !dir {
		return false
	}
	return name == "" || pattern == "**" || strings.HasPrefix(pattern, "**/") || strings.HasPrefix(pattern, name+"/")
}
