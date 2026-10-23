package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func registryWithAHostSession(t *testing.T) *Registry {
	t.Helper()
	r := openTestRegistry(t)
	hostID := "h0123456789ab"
	if _, err := r.AddHost(PairedHost{ID: hostID, Name: "server", Endpoint: "https://server.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_secret"}); err != nil {
		t.Fatal(err)
	}
	entity := hostID + "::remote-thread"
	row := HostCatalogRow{Session: surface.Session{ID: "remote-thread", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "remote work"}, EntityID: entity, Projection: json.RawMessage(`{"id":"` + entity + `"}`)}
	if _, _, err := r.ApplyHostCatalogRow(hostID, row); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestHoldingASendForAHostTouchesItsChangeSignal(t *testing.T) {
	r := registryWithAHostSession(t)
	path, err := r.ChangeSignal(ChangeHostDeliveries)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || len(data) != 0 {
		t.Fatalf("new signal=%q err=%v", data, err)
	}
	if _, err := r.QueueHostDelivery("remote-thread", "held", "key-held", []byte(`{"action":"send"}`), false); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || len(data) == 0 {
		t.Fatalf("signal after a held send=%q err=%v", data, err)
	}
}

func TestASignalThatCannotBeWrittenNeverFailsTheSendItSignals(t *testing.T) {
	r := registryWithAHostSession(t)
	if err := os.WriteFile(filepath.Join(filepath.Dir(r.Path()), "changes"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.QueueHostDelivery("remote-thread", "held", "key-held", []byte(`{"action":"send"}`), false); err != nil {
		t.Fatalf("held send failed with its signal unwritable: %v", err)
	}
	if held, err := r.HostDeliveries("h0123456789ab"); err != nil || len(held) != 1 {
		t.Fatalf("held=%+v err=%v", held, err)
	}
}
