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
	remote := PairedHost{ID: "h0123456789ab", Name: "vps", Endpoint: "https://vps.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_secret"}
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
	if _, err := r.AddHost(PairedHost{ID: "h0123456789ad", Name: "vps", Endpoint: remote.Endpoint, Address: remote.Address, Token: remote.Token}); err == nil {
		t.Fatal("duplicate host name was accepted")
	}
	if _, err := r.SetHostName("vps"); err == nil {
		t.Fatal("local rename onto a paired host name was accepted")
	}
	byName, err := r.Host("VPS")
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
	if _, err := r.AddHost(PairedHost{ID: hostID, Name: "vps", Endpoint: "https://vps.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_secret"}); err != nil {
		t.Fatal(err)
	}
	local := surface.Session{ID: "local-thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle}
	if err := r.RegisterSession(local); err != nil {
		t.Fatal(err)
	}
	remote := surface.Session{ID: "remote-thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "remote work", Status: surface.StatusBusy, PID: 42, Transcript: "/home/remote/t.jsonl"}
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
	page, err := r.CatalogSnapshotPage(CatalogPageRequest{Query: "builder", Limit: 10, Now: time.Now()})
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
	if _, err := r.AddHost(PairedHost{ID: hostID, Name: "vps", Endpoint: "https://vps.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_secret"}); err != nil {
		t.Fatal(err)
	}
	entity := hostID + "::remote-thread"
	if _, _, err := r.ApplyHostCatalogRow(hostID, HostCatalogRow{Session: surface.Session{ID: "remote-thread", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}, EntityID: entity, Projection: json.RawMessage(`{"id":"` + entity + `"}`)}); err != nil {
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

func TestHeldHostDeliveryIsInvisibleToLocalClaimsAndMatchesByAlias(t *testing.T) {
	r := openTestRegistry(t)
	hostID := "h0123456789ab"
	if _, err := r.AddHost(PairedHost{ID: hostID, Name: "vps", Endpoint: "https://vps.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_secret"}); err != nil {
		t.Fatal(err)
	}
	entity := hostID + "::remote-thread"
	if _, _, err := r.ApplyHostCatalogRow(hostID, HostCatalogRow{Session: surface.Session{ID: "remote-thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "remote work"}, EntityID: entity, Projection: json.RawMessage(`{"id":"` + entity + `","alias":"builder"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.QueueHostDelivery("remote-thread", "held", "cli-key", []byte(`{"action":"send"}`), false); err != nil {
		t.Fatal(err)
	}
	if item, err := r.ClaimNextMessage("remote-thread", time.Now()); err != nil || item != nil {
		t.Fatalf("local claim took a held host delivery: item=%+v err=%v", item, err)
	}
	held, err := r.HostDeliveries(hostID)
	if err != nil || len(held) != 1 || held[0].Status != "pending" || held[0].Key != "cli-key" {
		t.Fatalf("held=%+v err=%v", held, err)
	}
	for _, target := range []string{"@builder", "builder", "remote-thread"} {
		matches, err := r.MatchHostSessions("", target)
		if err != nil || len(matches) != 1 || matches[0].HostID != hostID || matches[0].ID != "remote-thread" || matches[0].Alias != "builder" {
			t.Fatalf("target %q matches=%+v err=%v", target, matches, err)
		}
	}
	if matches, err := r.MatchHostSessions("hffffffffffff", "builder"); err != nil || len(matches) != 0 {
		t.Fatalf("other host matches=%+v err=%v", matches, err)
	}
	if matches, err := r.MatchHostSessions("", "remote"); err != nil || len(matches) != 0 {
		t.Fatalf("prefix matched mirrored row: %+v err=%v", matches, err)
	}
}

func TestLocalRegistrationRefusesHostOwnedSession(t *testing.T) {
	r := openTestRegistry(t)
	hostID := "h0123456789ab"
	if _, err := r.AddHost(PairedHost{ID: hostID, Name: "vps", Endpoint: "https://vps.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_secret"}); err != nil {
		t.Fatal(err)
	}
	entity := hostID + "::remote-thread"
	if _, _, err := r.ApplyHostCatalogRow(hostID, HostCatalogRow{Session: surface.Session{ID: "remote-thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "remote work"}, EntityID: entity, Projection: json.RawMessage(`{"id":"` + entity + `"}`)}); err != nil {
		t.Fatal(err)
	}
	before, err := r.Session("remote-thread")
	if err != nil {
		t.Fatal(err)
	}
	takeover := surface.Session{ID: "remote-thread", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "local takeover", PID: 7, Transcript: "/tmp/local.jsonl"}
	if err := r.RegisterSession(takeover); !errors.As(err, &SessionOwnedError{}) {
		t.Fatalf("RegisterSession err=%v", err)
	}
	name, err := r.ReserveAlias("takeover", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.RecordPendingLaunch("terminal", surface.KindClaude, "/work", "local takeover", name, surface.Location{}, time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	launches, err := r.PendingLaunches()
	if err != nil || len(launches) != 1 {
		t.Fatalf("launches=%+v err=%v", launches, err)
	}
	if _, err := r.RegisterSessionAndDeletePending(takeover, launches[0]); !errors.As(err, &SessionOwnedError{}) {
		t.Fatalf("RegisterSessionAndDeletePending err=%v", err)
	}
	after, err := r.Session("remote-thread")
	if err != nil || after.Name != before.Name || after.Surface != before.Surface || after.PID != before.PID || after.Transcript != before.Transcript {
		t.Fatalf("before=%+v after=%+v err=%v", before, after, err)
	}
	if owner, err := r.SessionHost("remote-thread"); err != nil || owner != hostID {
		t.Fatalf("owner=%q err=%v", owner, err)
	}
	pending, err := r.PendingLaunches()
	if err != nil || len(pending) != 1 {
		t.Fatalf("refused registration consumed the pending launch: %+v err=%v", pending, err)
	}
	rows, err := r.HostCatalogRows(hostID)
	if err != nil || len(rows) != 1 || rows[0].Session.ID != "remote-thread" {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

func TestLocalRegistrationTakesASessionItsHostStoppedServing(t *testing.T) {
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
	unsent, err := r.QueueHostDelivery("remote-thread", "never sent", "key-unsent", []byte(`{"action":"send"}`), false)
	if err != nil {
		t.Fatal(err)
	}
	sent, err := r.QueueHostDelivery("remote-thread", "sent once", "key-sent", []byte(`{"action":"send"}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.RemoveHostCatalogRow(hostID, "remote-thread", entity, "removed_by_host"); err != nil {
		t.Fatal(err)
	}
	local := surface.Session{ID: "remote-thread", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "here now", PID: 7, Transcript: "/tmp/local.jsonl", HasLocal: true}
	if err := r.RegisterSession(local); err != nil {
		t.Fatalf("a session its host no longer serves stayed blocked: %v", err)
	}
	if owner, err := r.SessionHost("remote-thread"); err != nil || owner != "" {
		t.Fatalf("owner=%q err=%v", owner, err)
	}
	if stored, err := r.Session("remote-thread"); err != nil || stored.PID != 7 || stored.Transcript != "/tmp/local.jsonl" {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	if held, err := r.HostDeliveries(hostID); err != nil || len(held) != 0 {
		t.Fatalf("sends still held for the host=%+v err=%v", held, err)
	}
	for id, want := range map[int64]string{unsent: "before the message was sent", sent: "outcome is unknown"} {
		item, err := r.QueueItem(id, nil)
		if err != nil || item.Status != "dead" || !strings.Contains(item.LastError, want) {
			t.Fatalf("held send %d=%+v err=%v", id, item, err)
		}
	}
	if next, err := r.ClaimNextMessage("remote-thread", time.Now()); err != nil || next != nil {
		t.Fatalf("a failed host send became deliverable here: %+v err=%v", next, err)
	}
	if _, _, err := r.ApplyHostCatalogRow(hostID, row); !errors.As(err, &SessionOwnedError{}) {
		t.Fatalf("the host took back a local session: %v", err)
	}
}

func TestHostCatalogRowRequiresPairedHost(t *testing.T) {
	r := openTestRegistry(t)
	hostID := "h0123456789ab"
	entity := hostID + "::remote-thread"
	row := HostCatalogRow{Session: surface.Session{ID: "remote-thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop}, EntityID: entity, Projection: json.RawMessage(`{"id":"` + entity + `"}`)}
	if _, _, err := r.ApplyHostCatalogRow(hostID, row); err == nil {
		t.Fatal("mirrored a row for a host that is not paired")
	}
	if _, err := r.AddHost(PairedHost{ID: hostID, Name: "vps", Endpoint: "https://vps.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_secret"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.ApplyHostCatalogRow(hostID, row); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RemoveHost(hostID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.ApplyHostCatalogRow(hostID, row); err == nil {
		t.Fatal("mirrored a row after the host was removed")
	}
	if _, err := r.Session("remote-thread"); err == nil {
		t.Fatal("removed host row came back")
	}
}

func TestHeldHostDeliveryStaysPendingUntilTheHostTakesIt(t *testing.T) {
	r := openTestRegistry(t)
	hostID := "h0123456789ab"
	if _, err := r.AddHost(PairedHost{ID: hostID, Name: "vps", Endpoint: "https://vps.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_secret"}); err != nil {
		t.Fatal(err)
	}
	entity := hostID + "::remote-thread"
	if _, _, err := r.ApplyHostCatalogRow(hostID, HostCatalogRow{Session: surface.Session{ID: "remote-thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop}, EntityID: entity, Projection: json.RawMessage(`{"id":"` + entity + `"}`)}); err != nil {
		t.Fatal(err)
	}
	held, err := r.QueueHostDelivery("remote-thread", "while away", "key-held", []byte(`{"action":"send"}`), false)
	if err != nil {
		t.Fatal(err)
	}
	canceled, err := r.QueueHostDelivery("remote-thread", "never mind", "key-canceled", []byte(`{"action":"send"}`), false)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	r.now = func() time.Time { return start.Add(72 * time.Hour) }
	if _, err := r.RecoverInflight(r.now()); err != nil {
		t.Fatal(err)
	}
	if err := r.ReconcileAttentionItems(r.now()); err != nil {
		t.Fatal(err)
	}
	if item, err := r.QueueItem(held, nil); err != nil || item.Status != "pending" || item.HeldBy != QueueHeldHostUnreachable {
		t.Fatalf("held item after three days=%+v err=%v", item, err)
	}
	if err := r.CancelMessage(canceled); err != nil {
		t.Fatal(err)
	}
	pending, err := r.HostDeliveries(hostID)
	if err != nil || len(pending) != 1 || pending[0].ID != held {
		t.Fatalf("deliveries=%+v err=%v", pending, err)
	}
	if _, claimed, err := r.ClaimHostDelivery(canceled); err != nil || claimed {
		t.Fatalf("claimed a canceled delivery: claimed=%v err=%v", claimed, err)
	}
	if _, claimed, err := r.ClaimHostDelivery(held); err != nil || !claimed {
		t.Fatalf("held claim=%v err=%v", claimed, err)
	}
	if err := r.ReleaseHostDelivery(held); err != nil {
		t.Fatal(err)
	}
	if item, err := r.QueueItem(held, nil); err != nil || item.Status != "pending" || item.HeldBy != QueueHeldHostUnreachable {
		t.Fatalf("released item=%+v err=%v", item, err)
	}
}

func TestHostDeliveryConfirmationWindowStartsAtFirstSend(t *testing.T) {
	r := openTestRegistry(t)
	hostID := "h0123456789ab"
	if _, err := r.AddHost(PairedHost{ID: hostID, Name: "vps", Endpoint: "https://vps.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_secret"}); err != nil {
		t.Fatal(err)
	}
	entity := hostID + "::remote-thread"
	if _, _, err := r.ApplyHostCatalogRow(hostID, HostCatalogRow{Session: surface.Session{ID: "remote-thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop}, EntityID: entity, Projection: json.RawMessage(`{"id":"` + entity + `"}`)}); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Truncate(time.Millisecond)
	r.now = func() time.Time { return start }
	id, err := r.QueueHostDelivery("remote-thread", "waited offline", "key-wait", []byte(`{"action":"send"}`), false)
	if err != nil {
		t.Fatal(err)
	}
	firstSend := start.Add(20 * time.Minute)
	r.now = func() time.Time { return firstSend }
	inflightAt, claimed, err := r.ClaimHostDelivery(id)
	if err != nil || !claimed || !inflightAt.Equal(firstSend) {
		t.Fatalf("first claim at=%v claimed=%v err=%v", inflightAt, claimed, err)
	}
	r.now = func() time.Time { return firstSend.Add(5 * time.Minute) }
	inflightAt, claimed, err = r.ClaimHostDelivery(id)
	if err != nil || !claimed || !inflightAt.Equal(firstSend) {
		t.Fatalf("replay claim moved the window: at=%v claimed=%v err=%v", inflightAt, claimed, err)
	}
	sent, err := r.QueueHostDelivery("remote-thread", "written once", "key-sent", []byte(`{"action":"send"}`), true)
	if err != nil {
		t.Fatal(err)
	}
	inflightAt, claimed, err = r.ClaimHostDelivery(sent)
	if err != nil || !claimed || !inflightAt.Equal(firstSend.Add(5*time.Minute)) {
		t.Fatalf("written delivery window at=%v claimed=%v err=%v", inflightAt, claimed, err)
	}
}

func TestSurfaceAndExactResolutionNeverReachHostOwnedSessions(t *testing.T) {
	r := openTestRegistry(t)
	hostID := "h0123456789ab"
	if _, err := r.AddHost(PairedHost{ID: hostID, Name: "vps", Endpoint: "https://vps.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_secret"}); err != nil {
		t.Fatal(err)
	}
	entity := hostID + "::remote-thread"
	if _, _, err := r.ApplyHostCatalogRow(hostID, HostCatalogRow{Session: surface.Session{ID: "remote-thread", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "remote work", Cwd: "/srv/remote"}, EntityID: entity, Projection: json.RawMessage(`{"id":"` + entity + `"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := r.SetAlias("builder", "remote-thread"); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"remote-thread", "@builder", "builder"} {
		if id, err := r.ExactTarget("", target); err == nil {
			t.Fatalf("ExactTarget(%q) reached a host-owned row: %s", target, id)
		}
		if id, err := r.ExactTarget(surface.KindClaude, target); err == nil {
			t.Fatalf("ExactTarget(claude, %q) reached a host-owned row: %s", target, id)
		}
	}
	for _, target := range []string{"remote-thread", "remote-th", "remote work", "/srv/remote"} {
		if id, err := r.ResolveSurfaceTarget(surface.KindClaude, target); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("ResolveSurfaceTarget(%q)=%q err=%v", target, id, err)
		}
	}
}
