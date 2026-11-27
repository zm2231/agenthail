package testkit

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
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

var (
	nodeTestModule = regexp.MustCompile(`['"]node:test['"]`)
	leadingComment = regexp.MustCompile(`^(?:\s+|//[^\n]*|/\*(?s:.*?)\*/)`)
	homeStatement  = regexp.MustCompile(`^(?:import\s*|require\(\s*)(['"])([^'"\n]+)['"]`)
)

func isNodeTest(root, path string, source []byte) bool {
	name := filepath.Base(path)
	named := strings.Contains(name, ".test.") || (filepath.Dir(path) == filepath.Join(root, "scripts") && strings.HasPrefix(name, "test-"))
	return named || nodeTestModule.Match(source)
}

func importsHomeFirst(path string, source []byte, helper string) bool {
	text := string(source)
	if strings.HasPrefix(text, "#!") {
		_, text, _ = strings.Cut(text, "\n")
	}
	for {
		skip := leadingComment.FindString(text)
		if skip == "" {
			break
		}
		text = text[len(skip):]
	}
	match := homeStatement.FindStringSubmatch(text)
	return match != nil && filepath.Clean(filepath.Join(filepath.Dir(path), match[2])) == helper
}

func TestOnlyAFirstStatementImportingTheHelperCounts(t *testing.T) {
	helper := filepath.Join("/repo", "scripts", "test-home.mjs")
	for source, want := range map[string]bool{
		"// Usage: node x\nimport './test-home.mjs';\nimport x from 'y';":            true,
		"#!/usr/bin/env node\n/* a\nb */\nimport \"./test-home.mjs\"":                true,
		"require('./test-home.mjs');\nconst test = require('node:test');":            true,
		"import assert from 'node:assert';\nimport './test-home.mjs';":               false,
		"import './other.mjs';\nimport './test-home.mjs';":                           false,
		"import home from './test-home.mjs';":                                        false,
		"/* import './test-home.mjs' */\nimport assert from 'node:assert';":          false,
		"// import './test-home.mjs'\nimport assert from 'node:assert';":             false,
		"const s = `\nimport './test-home.mjs'`;\nimport assert from 'node:assert';": false,
		"import '../test-home.mjs';":                                                 false,
	} {
		if got := importsHomeFirst(filepath.Join("/repo", "scripts", "test-x.mjs"), []byte(source), helper); got != want {
			t.Errorf("importsHomeFirst(%q) = %v, want %v", source, got, want)
		}
	}
	if !importsHomeFirst(filepath.Join("/repo", "claude-host", "test", "a.test.mjs"), []byte("import '../../scripts/test-home.mjs';"), helper) {
		t.Error("a nested test's relative import of the helper did not count")
	}
}

func TestEveryNodeTestRunsInItsOwnHome(t *testing.T) {
	var missing []string
	entries := 0
	walkModule(t, func(root, path string, entry fs.DirEntry) {
		if entry.IsDir() || !slices.Contains([]string{".mjs", ".cjs", ".js"}, filepath.Ext(path)) || entry.Name() == "test-home.mjs" {
			return
		}
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !isNodeTest(root, path, source) {
			return
		}
		entries++
		if !importsHomeFirst(path, source, filepath.Join(root, "scripts", "test-home.mjs")) {
			relative, _ := filepath.Rel(root, path)
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

func TestHoldHomeUntilKilled(t *testing.T) {
	if os.Getenv("AGENTHAIL_TESTKIT_HOLD_HOME") == "" {
		t.Skip("run by TestAKilledTestBinarysHomeIsRemovedByTheNextOne")
	}
	fmt.Println(os.Getenv("HOME"))
	fmt.Println(t.TempDir())
	time.Sleep(time.Minute)
}

func TestAKilledTestBinarysHomeIsRemovedByTheNextOne(t *testing.T) {
	tmp := t.TempDir()
	env := append(os.Environ(), "TMPDIR="+tmp)
	killedHome := func() (string, string, int) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestHoldHomeUntilKilled$")
		cmd.Env = append(env, "AGENTHAIL_TESTKIT_HOLD_HOME=1")
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		lines := bufio.NewReader(out)
		home, err := lines.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		temp, err := lines.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		cmd.Process.Kill()
		cmd.Wait()
		return strings.TrimSpace(home), strings.TrimSpace(temp), cmd.Process.Pid
	}
	backdate := func(path string) {
		old := time.Now().Add(-time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	owned := func(name string, pid int) string {
		home := filepath.Join(tmp, name)
		if err := os.Mkdir(home, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, homeOwner), []byte(strconv.Itoa(pid)), 0o600); err != nil {
			t.Fatal(err)
		}
		return home
	}

	dead, deadTemp, pid := killedHome()
	if !strings.HasPrefix(deadTemp, dead+string(filepath.Separator)) {
		t.Fatalf("the child's t.TempDir %s is not inside its home %s", deadTemp, dead)
	}
	if filepath.Dir(dead) != tmp {
		t.Fatalf("the child's home %s is not in %s", dead, tmp)
	}
	if _, err := os.Stat(dead); err != nil {
		t.Fatalf("the killed child's home is already gone: %v", err)
	}
	backdate(filepath.Join(dead, homeOwner))
	justDied := owned(homePrefix+"justdied", pid)
	alive := owned(homePrefix+"alive", os.Getpid())
	backdate(filepath.Join(alive, homeOwner))
	unmarked := filepath.Join(tmp, homePrefix+"unmarked")
	if err := os.Mkdir(unmarked, 0o700); err != nil {
		t.Fatal(err)
	}
	backdate(unmarked)

	next := exec.Command(os.Args[0], "-test.run=^$")
	next.Env = env
	if out, err := next.CombinedOutput(); err != nil {
		t.Fatalf("next test binary: %v\n%s", err, out)
	}
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Errorf("the killed child's home %s was not removed: %v", dead, err)
	}
	for _, kept := range []string{justDied, alive, unmarked} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s was removed: %v", kept, err)
		}
	}
	entries, _ := os.ReadDir(tmp)
	if len(entries) != 3 {
		t.Errorf("the next test binary left %d folders in %s, want the 3 kept ones", len(entries), tmp)
	}
}
