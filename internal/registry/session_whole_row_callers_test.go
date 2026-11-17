package registry

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

var wholeRowRegistrations = map[string]bool{"RecordObservedSession": true, "RecordObservedSessionAndDeletePending": true, "TakeMovedSession": true}

// wholeRowRegistrars are the only functions that write a whole session row.
// Each writes a session it creates, or values it has just observed from the
// session's provider or process; a write of values read from the registry is
// an UpdateSession instead.
var wholeRowRegistrars = map[string]string{
	"internal/registry/delivery_intents.go recordNamedSender":             "creates a sender row that does not exist yet",
	"internal/registry/delivery_intents.go EnsureSender":                  "creates the person or CLI sender row only when it does not exist",
	"internal/registry/session_starting.go RecordStartingSession":         "creates the row of a session its start just minted",
	"internal/registry/session_starting.go RecordLearnedSession":          "records a session no action read, or one under a new id",
	"internal/delivery/delivery.go RegisterReached":                       "records the process a send reached under a different id, as its provider reports it",
	"internal/daemon/voice.go registerVoiceOperator":                      "creates the voice operator's row when it has none",
	"internal/daemon/session_move.go apiMoveTransferHandler":              "takes a session this host resumed in a move, inside the move's handover",
	"internal/daemon/session_operations.go doSessionOperation":            "creates the row of a fork the provider just minted",
	"internal/cli/thread_operations.go cmdThreadOperation":                "creates the row of a fork the provider just minted",
	"internal/daemon/dashboard_spawn.go createNotionThread":               "creates the row of a Notion thread just started",
	"internal/cli/send.go cmdSend":                                        "creates the row of a Notion thread a send just started",
	"internal/daemon/ledger_checker.go launchChecker":                     "creates the row of a checker session just started",
	"internal/daemon/catalog_scripted.go keepHiddenSession":               "records a scripted run as discovery lists it",
	"internal/daemon/catalog_stream.go correlatePendingLaunches":          "records a launched session as discovery lists it",
	"internal/daemon/catalog_stream.go correlateObservedTerminalLaunches": "records a launched terminal session as discovery lists it",
	"internal/daemon/observer.go refreshAndPruneInactiveClaudeRoutes":     "records Claude sessions as discovery lists them",
	"internal/daemon/dashboard_search.go dashboardSearchHandler":          "records a session as provider search returns it",
	"internal/cli/search.go cmdSearch":                                    "records a session as provider search returns it",
	"internal/cli/session_target.go resolveLocalTarget":                   "records a session as its provider resolves it",
}

func TestOnlyListedFunctionsRegisterAWholeSessionRow(t *testing.T) {
	root := filepath.Join("..", "..")
	found := map[string]bool{}
	checked := 0
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			checked++
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			for _, decl := range file.Decls {
				function, ok := decl.(*ast.FuncDecl)
				if !ok || function.Body == nil {
					continue
				}
				ast.Inspect(function.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					selector, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || !wholeRowRegistrations[selector.Sel.Name] {
						return true
					}
					key := filepath.ToSlash(rel) + " " + function.Name.Name
					found[key] = true
					if wholeRowRegistrars[key] == "" {
						t.Errorf("%s writes a whole session row with %s; write what it read back with UpdateSession, or list it with the reason its values are new", key, selector.Sel.Name)
					}
					return true
				})
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if checked < 100 {
		t.Fatalf("checked only %d files", checked)
	}
	for key := range wholeRowRegistrars {
		if !found[key] {
			t.Errorf("%s is listed but no longer registers a whole session row", key)
		}
	}
}
