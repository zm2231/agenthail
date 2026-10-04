package daemon

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

func TestReadJournalPagePreservesLiveSourceErrorAfterSuccessfulSeed(t *testing.T) {
	d, reg, fake, from, _ := daemonFixture(t)
	retention := registry.SessionJournalRetention{Count: 32, Bytes: 16 << 10}
	normal, err := json.Marshal(sessionJournalPayload{ItemID: "old", ProviderKey: "old", Kind: "text", Role: "assistant", Body: "old"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := reg.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: from.ID, Kind: "text", ProviderKey: "old", Payload: normal, ObservedAt: time.Now()}, retention); err != nil {
		t.Fatal(err)
	}
	source := &sessionSource{manager: newSessionSourceManager(reg), session: from, adapter: fake, epoch: "test", appendBodies: map[string]string{}}
	source.appendSourceError(errors.New("initial provider failure"))
	if err := reg.MarkSessionJournalSeed(from.ID, true); err != nil {
		t.Fatal(err)
	}
	page, err := d.readJournalPage(from.ID, 0, 10)
	if err != nil || page.UnavailableReason != "" {
		t.Fatalf("successful seed left baseline error: page=%+v err=%v", page, err)
	}
	source.appendSourceError(errors.New("live provider failure"))
	page, err = d.readJournalPage(from.ID, 0, 10)
	if err != nil || page.UnavailableReason != "live provider failure" {
		t.Fatalf("live source error hidden: page=%+v err=%v", page, err)
	}
}
