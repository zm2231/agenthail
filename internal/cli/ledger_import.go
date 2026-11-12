package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

const ledgerImportUsage = `usage: agenthail ledger import td [<path to .todos/issues.db>] [--all] [--dry-run] [--json]`

const importBatchBytes = 96 << 10

func (a *App) ledgerImport(store *ledger.Store, args []string) error {
	w, err := parseLedgerWrite(args)
	if err != nil {
		return err
	}
	if len(w.positional) < 2 || len(w.positional) > 3 || w.positional[1] != "td" {
		return fmt.Errorf("%s", ledgerImportUsage)
	}
	path := ""
	if len(w.positional) == 3 {
		path = w.positional[2]
	} else if path, err = findTD(); err != nil {
		return err
	}
	db, err := openTD(path)
	if err != nil {
		return err
	}
	export, err := readTD(db, w.bools["--all"], time.Now())
	db.Close()
	if err != nil {
		return fmt.Errorf("read td database %s: %w", path, err)
	}
	if w.bools["--dry-run"] {
		board, err := a.workBoard(store, w)
		if err != nil {
			return err
		}
		plan := planTDImport(board, export)
		if w.bools["--json"] {
			return emit(map[string]any{"items": plan.items, "deps": plan.deps, "present": plan.present, "notCarriedOver": export.leftOver})
		}
		fmt.Printf("would import %d items and %d dependencies from %s (%d already imported)\n", len(plan.items), len(plan.deps), path, len(plan.present))
		printLeftOver(export.leftOver)
		return nil
	}
	ctx := context.Background()
	key, session, err := a.ledgerWriteContext(ctx, w)
	if err != nil {
		return err
	}
	var total ledger.WorkImportResult
	send := func(req ledger.WorkImportRequest) error {
		req.ProjectKey, req.SessionID = key, session
		var result ledger.WorkImportResult
		if err := a.ledgerPost(ctx, "/api/v1/ledger/import", req, &result); err != nil {
			return err
		}
		total.Imported = append(total.Imported, result.Imported...)
		total.Present = append(total.Present, result.Present...)
		total.Skipped = append(total.Skipped, result.Skipped...)
		total.Deps += result.Deps
		return nil
	}
	var items importBatches[ledger.WorkImport]
	for _, item := range export.items {
		data, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if len(data) > importBatchBytes {
			total.Skipped = append(total.Skipped, ledger.WorkImportSkip{ImportedFrom: item.ImportedFrom, Reason: fmt.Sprintf("its history is %d bytes, more than one import request carries", len(data))})
			continue
		}
		items.add(item, len(data))
	}
	var deps importBatches[ledger.WorkImportDep]
	for _, dep := range export.deps {
		data, err := json.Marshal(dep)
		if err != nil {
			return err
		}
		deps.add(dep, len(data))
	}
	for _, batch := range items.batches {
		if err := send(ledger.WorkImportRequest{Items: batch}); err != nil {
			return importStopped(total, err)
		}
	}
	for _, batch := range deps.batches {
		if err := send(ledger.WorkImportRequest{Deps: batch}); err != nil {
			return importStopped(total, err)
		}
	}
	if w.bools["--json"] {
		return emit(map[string]any{"result": total, "notCarriedOver": export.leftOver})
	}
	fmt.Printf("imported %d items and %d dependencies from %s into %s as found work; %d were already imported\n", len(total.Imported), total.Deps, path, key, len(total.Present))
	for _, s := range total.Skipped {
		fmt.Printf("  skipped %s: %s\n", s.ImportedFrom, s.Reason)
	}
	printLeftOver(export.leftOver)
	if len(total.Imported) > 0 {
		fmt.Println("each item is found work until it is linked to a clause: agenthail ledger work link <item> <clause>")
	}
	return nil
}

type importBatches[T any] struct {
	batches [][]T
	last    int
}

func (b *importBatches[T]) add(v T, size int) {
	if n := len(b.batches); n > 0 && b.last+size <= importBatchBytes {
		b.batches[n-1], b.last = append(b.batches[n-1], v), b.last+size
		return
	}
	b.batches, b.last = append(b.batches, []T{v}), size
}

func importStopped(done ledger.WorkImportResult, err error) error {
	return fmt.Errorf("import stopped after %d items; run it again to bring over the rest, items already imported are kept: %w", len(done.Imported)+len(done.Present), err)
}

func printLeftOver(leftOver []string) {
	if len(leftOver) == 0 {
		return
	}
	fmt.Println("not carried over:")
	for _, l := range leftOver {
		fmt.Println("  " + l)
	}
}

func findTD() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		path := filepath.Join(dir, ".todos", "issues.db")
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no .todos/issues.db here or in a parent directory; give the path\n%s", ledgerImportUsage)
		}
		dir = parent
	}
}

type tdImportPlan struct {
	items   []ledger.WorkImport
	deps    []ledger.WorkImportDep
	present []ledger.WorkImported
}

func planTDImport(board *ledger.Board, export tdExport) tdImportPlan {
	imported := map[string]ledger.WorkItem{}
	for _, item := range board.WorkItems() {
		if item.ImportedFrom != "" {
			imported[item.ImportedFrom] = item
		}
	}
	var plan tdImportPlan
	for _, item := range export.items {
		if have, ok := imported[item.ImportedFrom]; ok {
			plan.present = append(plan.present, ledger.WorkImported{ImportedFrom: item.ImportedFrom, ItemID: have.ID})
			continue
		}
		plan.items = append(plan.items, item)
	}
	for _, dep := range export.deps {
		item, itemOK := imported[dep.Item]
		on, onOK := imported[dep.On]
		if itemOK && onOK && slices.Contains(item.DependsOn, on.ID) {
			continue
		}
		plan.deps = append(plan.deps, dep)
	}
	return plan
}
