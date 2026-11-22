package sourceguard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var messageQueueInsert = regexp.MustCompile(`(?is)\binsert\s+(or\s+\w+\s+)?into\s+message_queue\b`)

var messageQueueWritersAllowed = map[string]bool{"enqueueMessageTx": true, "QueueGoal": true, "queueCompactTx": true, "queueHostDeliveryTx": true}

func messageQueueWriters(t *testing.T, root string) []string {
	t.Helper()
	var writers []string
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
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			name := "package scope"
			if fn, ok := decl.(*ast.FuncDecl); ok {
				name = fn.Name.Name
			}
			ast.Inspect(decl, func(node ast.Node) bool {
				if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING && messageQueueInsert.MatchString(literal.Value) && !messageQueueWritersAllowed[name] {
					writers = append(writers, path+": "+name)
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return writers
}

func TestOnlyTheQueueChokepointWritesMessageRows(t *testing.T) {
	for _, writer := range messageQueueWriters(t, filepath.Join("..", "..")) {
		t.Errorf("%s inserts into message_queue; queue messages through enqueueMessageTx so they reach the inbox", writer)
	}
}

func TestTheChokepointGuardFindsAWriterInAnyPackage(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "cmd", "tool")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := "package main\n\nfunc queueDirectly() string {\n\treturn `INSERT OR IGNORE\n\t\tINTO message_queue(session_id,message) VALUES(?,?)`\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := messageQueueWriters(t, root); len(got) != 1 || !strings.HasSuffix(got[0], ": queueDirectly") {
		t.Fatalf("writers=%v", got)
	}
}
