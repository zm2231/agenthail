package daemon

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAPersonDeliveryToACodexSessionQueuesItsReadbackOnce(t *testing.T) {
	d, r, _ := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-readback", "readback-key")
	if err := p.store.Enable(p.key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	codex := surface.Session{ID: "codex-thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Cwd: p.dir, Status: surface.StatusIdle}
	if err := r.RegisterSession(codex); err != nil {
		t.Fatal(err)
	}
	claude := p.session(t, r, "claude-session")

	intent := uuid.NewString()
	for range 2 {
		d.personCapture(delivery.PersonCaptureContext{Target: codex, IntentUUID: intent, Message: "build the importer"})
	}
	d.personCapture(delivery.PersonCaptureContext{Target: claude, IntentUUID: uuid.NewString(), Message: "and the exporter"})

	rows, err := r.ReadDB().Query(`SELECT session_id,message FROM message_queue WHERE delivery_key LIKE 'ledger:readback:%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var queued [][2]string
	for rows.Next() {
		var session, message string
		if err := rows.Scan(&session, &message); err != nil {
			t.Fatal(err)
		}
		queued = append(queued, [2]string{session, message})
	}
	if len(queued) != 1 || queued[0][0] != codex.ID {
		t.Fatalf("want one readback for the Codex session only, got %+v", queued)
	}
	ref := ledger.DeliveryRef(intent)
	for _, want := range []string{"utterance " + ref + " in " + p.key, "agenthail ledger attach " + ref + " --as", "Tell the person each readback line"} {
		if !strings.Contains(queued[0][1], want) {
			t.Fatalf("readback %q lacks %q", queued[0][1], want)
		}
	}
}
