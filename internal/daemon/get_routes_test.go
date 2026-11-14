package daemon

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/atomicfile"
	"github.com/zm2231/agenthail/internal/extension"
)

// routePatterns reads every pattern the daemon registers, from its source, so
// a route added later is audited without anyone listing it here. A pattern it
// cannot read fails the test, except a helper's own parameter, whose callers
// are read instead.
var notOnABrowserOrigin = map[string]string{
	"debug_socket.go": "the runtime profiles are served only on the daemon's 0600 Unix socket",
}

func routePatterns(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	type registration struct {
		call   *ast.CallExpr
		params map[string]bool
		at     token.Position
	}
	consts := map[string]string{}
	var calls []registration
	for _, source := range sources {
		if strings.HasSuffix(source, "_test.go") || notOnABrowserOrigin[source] != "" {
			continue
		}
		file, err := parser.ParseFile(fset, source, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		var stack []ast.Node
		ast.Inspect(file, func(node ast.Node) bool {
			if node == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			stack = append(stack, node)
			switch node := node.(type) {
			case *ast.ValueSpec:
				for i, name := range node.Names {
					if i < len(node.Values) {
						if lit, ok := node.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							consts[name.Name], _ = strconv.Unquote(lit.Value)
						}
					}
				}
			case *ast.CallExpr:
				params := map[string]bool{}
				for _, enclosing := range stack {
					var fields *ast.FieldList
					switch fn := enclosing.(type) {
					case *ast.FuncLit:
						fields = fn.Type.Params
					case *ast.FuncDecl:
						fields = fn.Type.Params
					}
					if fields != nil {
						for _, field := range fields.List {
							for _, name := range field.Names {
								params[name.Name] = true
							}
						}
					}
				}
				calls = append(calls, registration{call: node, params: params, at: fset.Position(node.Pos())})
			}
			return true
		})
	}
	var patterns []string
	for _, found := range calls {
		call := found.call
		name := ""
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			name = fun.Sel.Name
		case *ast.Ident:
			name = fun.Name
		}
		if (name != "HandleFunc" && name != "Handle" && name != "route") || len(call.Args) != 2 {
			continue
		}
		switch arg := call.Args[0].(type) {
		case *ast.BasicLit:
			if pattern, err := strconv.Unquote(arg.Value); err == nil && arg.Kind == token.STRING {
				patterns = append(patterns, pattern)
				continue
			}
		case *ast.Ident:
			if pattern, ok := consts[arg.Name]; ok {
				patterns = append(patterns, pattern)
				continue
			}
			if found.params[arg.Name] {
				continue
			}
		}
		t.Fatalf("%s registers a route whose pattern this test cannot read; give it a literal or a string constant", found.at)
	}
	slices.Sort(patterns)
	return slices.Compact(patterns)
}

func getPaths(patterns []string, values map[string]string) []string {
	var paths []string
	for _, pattern := range patterns {
		method, path, found := strings.Cut(pattern, " ")
		if !found {
			method, path = "", pattern
		}
		if method != "" && method != http.MethodGet && method != http.MethodHead {
			continue
		}
		if !strings.HasPrefix(path, "/") {
			continue
		}
		for name, value := range values {
			path = strings.ReplaceAll(path, "{"+name+"}", value)
		}
		paths = append(paths, path)
	}
	return paths
}

type daemonState struct {
	events uint64
	rows   map[string]string
	files  map[string]string
}

func readDaemonState(t *testing.T, f *extensionFixture) daemonState {
	t.Helper()
	db, err := sql.Open("sqlite", f.d.Registry.Path()+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tables, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var name string
		_ = tables.Scan(&name)
		names = append(names, name)
	}
	tables.Close()
	sums := map[string]string{}
	for _, name := range names {
		sum := sha256.New()
		query := `SELECT * FROM "` + name + `"`
		if name == "paired_devices" {
			query = `SELECT id, name, scopes, host_id, created_at, revoked_at, browser FROM paired_devices`
		}
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		columns, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			_ = rows.Scan(pointers...)
			fmt.Fprintf(sum, "%v\n", values)
		}
		rows.Close()
		sums[name] = hex.EncodeToString(sum.Sum(nil))
	}
	files := map[string]string{}
	for _, root := range []string{os.Getenv("HOME"), f.home} {
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || atomicfile.IsTemp(path) || strings.HasSuffix(path, ".log") || strings.Contains(path, "-wal") || strings.Contains(path, "-shm") || strings.HasSuffix(path, ".sock") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err == nil {
				digest := sha256.Sum256(data)
				files[path] = hex.EncodeToString(digest[:])
			}
			return nil
		})
	}
	return daemonState{events: f.d.events.cursor(), rows: sums, files: files}
}

func (before daemonState) changes(after daemonState) []string {
	var changed []string
	if after.events != before.events {
		changed = append(changed, fmt.Sprintf("published %d events", after.events-before.events))
	}
	for name, sum := range after.rows {
		if before.rows[name] != sum {
			changed = append(changed, "changed table "+name)
		}
	}
	for path, digest := range after.files {
		if before.files[path] != digest {
			changed = append(changed, "wrote "+path)
		}
	}
	for path := range before.files {
		if _, ok := after.files[path]; !ok {
			changed = append(changed, "removed "+path)
		}
	}
	return changed
}

// Pages share a site with the dashboard and with each other, so a page can
// send a blind GET, cookie and all, to any of them. No GET may change
// anything but a device's last_seen_at, which records that its own
// credential was used.
func TestNoGetRouteOnTheDashboardOrAnExtensionPortChangesAnything(t *testing.T) {
	f := startExtensionFixture(t)
	writeExtension(t, extension.Workspace(f.home), "notes", `{"label": "Notes"}`)
	f.turn(t, "to", "turn-1")
	rev := f.changes(t)[0].Rev
	page := f.pageURL(t)
	cookie := signInWithLoginLink(t, f.server.Config.Handler)
	patterns := routePatterns(t)
	if len(patterns) < 50 || !slices.Contains(patterns, "GET /s") || !slices.Contains(patterns, "/api/v1/events") {
		t.Fatalf("read %d route patterns from the source: %v", len(patterns), patterns)
	}
	paths := getPaths(patterns, map[string]string{"id": "fixture", "rev": rev, "action": "state", "path...": "assets/app.css"})

	time.Sleep(time.Second)
	for _, base := range []string{f.server.URL, page} {
		for _, path := range paths {
			for _, auth := range []string{"cookie", "bearer"} {
				before := readDaemonState(t, f)
				ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
				request, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
				if auth == "cookie" {
					request.AddCookie(cookie)
				} else {
					request.Header.Set("Authorization", "Bearer secret")
				}
				if response, err := http.DefaultClient.Do(request); err == nil {
					_, _ = io.Copy(io.Discard, response.Body)
					response.Body.Close()
				}
				cancel()
				if changed := before.changes(readDaemonState(t, f)); len(changed) > 0 {
					t.Errorf("GET %s%s with the %s %s", base, path, auth, strings.Join(changed, ", "))
				}
			}
		}
	}
}
