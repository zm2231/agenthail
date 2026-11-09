package daemon

import (
	"testing"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAPersonDeliveryIsProvisionalAndQueuesNothingToTheSession(t *testing.T) {
	d, r, _ := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-readback", "readback-key")
	if err := p.store.Enable(p.key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	codex := surface.Session{ID: "codex-thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Cwd: p.dir, Status: surface.StatusIdle}
	if err := r.RegisterSession(codex); err != nil {
		t.Fatal(err)
	}
	lead := surface.Session{ID: "codex-lead", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Cwd: p.dir, Status: surface.StatusIdle}
	if err := r.RegisterSession(lead); err != nil {
		t.Fatal(err)
	}
	makeCoordinator(t, r, p, lead.ID)
	intent := uuid.NewString()
	for range 2 {
		d.personCapture(delivery.PersonCaptureContext{Target: codex, IntentUUID: intent, Message: "build the importer"})
	}
	d.personCapture(delivery.PersonCaptureContext{Target: lead, IntentUUID: uuid.NewString(), Message: "plan the release"})
	for _, session := range []string{codex.ID, lead.ID} {
		if queued := r.QueueCount(session); queued != 0 {
			t.Fatalf("%s has %d messages queued for the person's words, want none", session, queued)
		}
		words, err := p.store.ProvisionalFor(p.key, session)
		if err != nil {
			t.Fatal(err)
		}
		if len(words) != 1 {
			t.Fatalf("%s has %d provisional messages, want its one delivery recorded once", session, len(words))
		}
	}
}
