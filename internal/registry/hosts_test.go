package registry

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestHostIdentityIsStableAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.HostIdentity()
	if err != nil || !ValidHostID(first.ID) || !ValidHostName(first.Name) {
		t.Fatalf("identity=%+v err=%v", first, err)
	}
	renamed, err := r.SetHostName("Studio")
	if err != nil || renamed.Name != "studio" || renamed.ID != first.ID {
		t.Fatalf("renamed=%+v err=%v", renamed, err)
	}
	if _, err := r.SetHostName("bad name"); err == nil {
		t.Fatal("invalid host name was accepted")
	}
	r.Close()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	again, err := reopened.HostIdentity()
	if err != nil || again.ID != first.ID || again.Name != "studio" {
		t.Fatalf("again=%+v first=%+v err=%v", again, first, err)
	}
}

func TestAddHostRefusesSelfDuplicatesAndNameCollisions(t *testing.T) {
	r := openTestRegistry(t)
	local, err := r.HostIdentity()
	if err != nil {
		t.Fatal(err)
	}
	remote := PairedHost{ID: "h0123456789ab", Name: "forge", Endpoint: "https://forge.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_secret"}
	if _, err := r.AddHost(PairedHost{ID: local.ID, Name: "other", Endpoint: remote.Endpoint, Address: remote.Address, Token: remote.Token}); err == nil {
		t.Fatal("pairing with self was accepted")
	}
	if _, err := r.AddHost(PairedHost{ID: "h0123456789ac", Name: local.Name, Endpoint: remote.Endpoint, Address: remote.Address, Token: remote.Token}); err == nil {
		t.Fatal("pairing under the local name was accepted")
	}
	if _, err := r.AddHost(remote); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddHost(remote); err == nil {
		t.Fatal("duplicate host id was accepted")
	}
	if _, err := r.AddHost(PairedHost{ID: "h0123456789ad", Name: "forge", Endpoint: remote.Endpoint, Address: remote.Address, Token: remote.Token}); err == nil {
		t.Fatal("duplicate host name was accepted")
	}
	if _, err := r.SetHostName("forge"); err == nil {
		t.Fatal("local rename onto a paired host name was accepted")
	}
	byName, err := r.Host("FORGE")
	if err != nil || byName.ID != remote.ID || byName.Token != remote.Token {
		t.Fatalf("byName=%+v err=%v", byName, err)
	}
	encoded, _ := json.Marshal(byName)
	if strings.Contains(string(encoded), remote.Token) {
		t.Fatalf("host JSON leaked the token: %s", encoded)
	}
	if _, err := r.Host("missing"); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("missing host err=%v", err)
	}
}

func TestHostCatalogRowsStayOwnedAndOutsideLocalResolution(t *testing.T) {
	r := openTestRegistry(t)
	hostID := "h0123456789ab"
	if _, err := r.AddHost(PairedHost{ID: hostID, Name: "forge", Endpoint: "https://forge.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_secret"}); err != nil {
		t.Fatal(err)
	}
	local := surface.Session{ID: "local-thread", Surface: surface.KindCodex, Status: surface.StatusIdle}
	if err := r.RegisterSession(local); err != nil {
		t.Fatal(err)
	}
	remote := surface.Session{ID: "remote-thread", Surface: surface.KindCodex, Name: "remote work", Status: surface.StatusBusy, PID: 42, Transcript: "/home/remote/t.jsonl"}
	row := HostCatalogRow{Session: remote, EntityID: hostID + "::remote-thread", Projection: json.RawMessage(`{"id":"h0123456789ab::remote-thread","surface":"codex","alias":"builder"}`)}
	event, created, err := r.ApplyHostCatalogRow(hostID, row)
	if err != nil || !created || event.Type != "session.upserted" || event.EntityID != row.EntityID {
		t.Fatalf("event=%+v created=%v err=%v", event, created, err)
	}
	if _, created, err := r.ApplyHostCatalogRow(hostID, row); err != nil || created {
		t.Fatalf("unchanged row created=%v err=%v", created, err)
	}
	if _, _, err := r.ApplyHostCatalogRow(hostID, HostCatalogRow{Session: local, EntityID: hostID + "::local-thread", Projection: json.RawMessage(`{"id":"x"}`)}); !errors.As(err, &SessionOwnedError{}) {
		t.Fatalf("taking over a local row err=%v", err)
	}
	stored, err := r.Session("remote-thread")
	if err != nil || stored.PID != 0 || stored.Transcript != "" {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	if owner, err := r.SessionHost("remote-thread"); err != nil || owner != hostID {
		t.Fatalf("owner=%q err=%v", owner, err)
	}
	if _, err := r.ResolveTarget("remote-thread"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("local resolution reached a remote row: %v", err)
	}
	if _, err := r.ResolveTarget("remote"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("local name resolution reached a remote row: %v", err)
	}
	if _, err := r.RecordCatalogDiscovery(surface.KindCodex, map[string]struct{}{}, time.Now(), true, 1); err != nil {
		t.Fatal(err)
	}
	rows, err := r.HostCatalogRows(hostID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("local discovery pruned host rows: rows=%+v err=%v", rows, err)
	}
	if _, err := r.MarkCatalogDiscoveryFailure(surface.KindCodex, "codex down", time.Now()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := r.CatalogSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range snapshot.Sessions {
		if state.Session.ID == "remote-thread" && (state.HostID != hostID || state.Freshness.Stale) {
			t.Fatalf("remote snapshot row=%+v", state)
		}
	}
	page, err := r.CatalogSnapshotPage(CatalogPageRequest{Query: "builder", Limit: 10, Now: time.Now(), CodexRecentHours: 24})
	if err != nil || len(page.Sessions) != 1 || page.Sessions[0].HostID != hostID {
		t.Fatalf("remote alias search page=%+v err=%v", page.Sessions, err)
	}
	watched, err := r.WatchedSessions()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.QueueMessage("remote-thread", "held for the host"); err != nil {
		t.Fatal(err)
	}
	if after, _ := r.WatchedSessions(); len(after) != len(watched) {
		t.Fatalf("local observer would watch a remote row: %+v", after)
	}
	if _, created, err := r.RemoveHostCatalogRow(hostID, "remote-thread", row.EntityID, "removed_by_host"); err != nil || !created {
		t.Fatalf("remove created=%v err=%v", created, err)
	}
	if rows, _ := r.HostCatalogRows(hostID); len(rows) != 0 {
		t.Fatalf("rows after remove=%+v", rows)
	}
}

func TestRemoveHostDeletesOwnedSessionsAndAnnouncesRemoval(t *testing.T) {
	r := openTestRegistry(t)
	hostID := "h0123456789ab"
	if _, err := r.AddHost(PairedHost{ID: hostID, Name: "forge", Endpoint: "https://forge.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_secret"}); err != nil {
		t.Fatal(err)
	}
	entity := hostID + "::remote-thread"
	if _, _, err := r.ApplyHostCatalogRow(hostID, HostCatalogRow{Session: surface.Session{ID: "remote-thread", Surface: surface.KindClaude}, EntityID: entity, Projection: json.RawMessage(`{"id":"` + entity + `"}`)}); err != nil {
		t.Fatal(err)
	}
	events, err := r.RemoveHost(hostID)
	if err != nil || len(events) != 1 || events[0].Type != "session.removed" || events[0].EntityID != entity {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	if _, err := r.Session("remote-thread"); err == nil {
		t.Fatal("owned session survived host removal")
	}
	if _, err := r.RemoveHost(hostID); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("second remove err=%v", err)
	}
}
