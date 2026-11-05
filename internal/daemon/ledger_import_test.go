package daemon

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestAnImportOnAHostThatDoesNotKeepTheLedgerLandsOnTheOwnerOnce(t *testing.T) {
	mac, owner, _ := ledgerOnTwoHosts(t)
	mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"}, nil)
	batch := ledger.WorkImportRequest{ProjectKey: widgetKey, SessionID: "local-a", Items: []ledger.WorkImport{
		{ImportedFrom: "td:td-1", Text: "the index leaks", CreatedAt: time.Now().Add(-time.Hour), Fields: ledger.WorkFields{Type: ledger.WorkBug}},
		{ImportedFrom: "td:td-2", Text: "write the schema", CreatedAt: time.Now().Add(-time.Hour)},
	}, Deps: []ledger.WorkImportDep{{Item: "td:td-1", On: "td:td-2"}}}

	var first, second ledger.WorkImportResult
	mustLedger(t, mac, "/api/v1/ledger/import", batch, &first)
	if len(first.Imported) != 2 || first.Deps != 1 {
		t.Fatalf("first import = %+v", first)
	}
	owner.gate.ledgerDrops.Store(true)
	owner.gate.drops.Store(1)
	mustLedger(t, mac, "/api/v1/ledger/import", batch, &second)
	if owner.gate.drops.Load() != 0 {
		t.Fatalf("the owner's answer was not dropped: %d left", owner.gate.drops.Load())
	}
	if len(second.Imported) != 0 || len(second.Present) != 2 || second.Deps != 0 {
		t.Fatalf("second import = %+v", second)
	}
	if n := countEvents(t, owner.reg, ledger.EventWorkItem, ""); n != 2 {
		t.Fatalf("the owner holds %d work items after two imports", n)
	}
	sameLedgerOnBothHosts(t, widgetKey, mac, owner)
	board, err := ledger.NewStore(mac.reg.WriteDB(), mac.reg.ReadDB()).Board(widgetKey)
	if err != nil {
		t.Fatal(err)
	}
	if item, _ := board.WorkItem("widget-W1"); item.ImportedFrom != "td:td-1" || item.ClauseID != "" || len(item.DependsOn) != 1 {
		t.Fatalf("the mac sees the import as %+v", item)
	}
}
