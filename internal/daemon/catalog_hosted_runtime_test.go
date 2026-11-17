package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestADiscoveryPassThatListsAHostedProcessAsATerminalKeepsTheOpensRegistration(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, r, base, _, _ := daemonFixture(t)
	base.kind = surface.KindClaude
	opened := surface.Session{ID: "opened-by-host", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Transport: "uds", PID: 4242, Status: surface.StatusIdle, Cwd: "/fixture"}
	if err := r.RegisterSession(opened); err != nil {
		t.Fatal(err)
	}
	base.sessions = map[string]surface.Session{opened.ID: opened}
	d.Surfaces = []surface.Surface{base}
	d.discoverCatalog(context.Background())
	if !catalogHasRow(t, r, opened.ID) {
		t.Fatal("the pass did not publish the hosted listing")
	}
	stale := opened
	stale.RuntimeKind, stale.Status = surface.RuntimeClaudeCLI, surface.StatusBusy
	base.sessions = map[string]surface.Session{stale.ID: stale}
	d.discoverCatalog(context.Background())
	if stored, err := r.Session(opened.ID); err != nil || stored.RuntimeKind != surface.RuntimeClaudeHost {
		t.Fatalf("stored=%+v err=%v, want the host's process kept on the host runtime", stored, err)
	}
	projection, _, err := r.CatalogProjectionFingerprint(opened.ID)
	if err != nil {
		t.Fatal(err)
	}
	var row struct {
		RuntimeKind surface.RuntimeKind   `json:"runtimeKind"`
		Status      surface.SessionStatus `json:"status"`
	}
	if err := json.Unmarshal([]byte(projection), &row); err != nil || row.RuntimeKind != surface.RuntimeClaudeHost || row.Status != surface.StatusBusy {
		t.Fatalf("projection=%s err=%v, want the listing's status published on the host runtime", projection, err)
	}
}

func TestAHostOpenOfAListedTerminalProcessRebuildsItsProjectionOnTheHostRuntime(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, r, base, _, _ := daemonFixture(t)
	base.kind = surface.KindClaude
	listed := surface.Session{ID: "listed-then-hosted", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transport: "uds", PID: 4242, Status: surface.StatusIdle, Cwd: "/fixture"}
	base.sessions = map[string]surface.Session{listed.ID: listed}
	d.Surfaces = []surface.Surface{base}
	d.discoverCatalog(context.Background())
	if !catalogHasRow(t, r, listed.ID) {
		t.Fatal("the pass did not publish the listed session")
	}
	hosted := listed
	hosted.RuntimeKind = surface.RuntimeClaudeHost
	if err := r.RegisterSession(hosted); err != nil {
		t.Fatal(err)
	}
	d.discoverCatalog(context.Background())
	projection, _, err := r.CatalogProjectionFingerprint(listed.ID)
	if err != nil {
		t.Fatal(err)
	}
	var row struct {
		RuntimeKind surface.RuntimeKind `json:"runtimeKind"`
	}
	if err := json.Unmarshal([]byte(projection), &row); err != nil || row.RuntimeKind != surface.RuntimeClaudeHost {
		t.Fatalf("projection=%s err=%v, want the projection rebuilt on the host runtime", projection, err)
	}
}

func TestASendThatReachesANewHostProcessKeepsItHostedWhenAStaleListingNamesItATerminal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, r, source := localStatusFixture(t)
	source.kind = surface.KindClaude
	opened := surface.Session{ID: "resumed-in-host", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Transport: "uds", PID: 4242, PIDIdentity: "a", Status: surface.StatusIdle, Cwd: "/fixture"}
	source.sessions = map[string]surface.Session{opened.ID: opened}
	source.writeStatus(t, opened.ID, opened.Status)
	d.discoverCatalog(context.Background())
	if !catalogHasRow(t, r, opened.ID) {
		t.Fatal("the pass did not publish the hosted session")
	}
	reached := opened
	reached.PID, reached.PIDIdentity = 4343, "b"
	if err := r.RegisterSession(reached); err != nil {
		t.Fatal(err)
	}
	d.publishRoute(reached)
	stale := reached
	stale.RuntimeKind = surface.RuntimeClaudeCLI
	source.sessions = map[string]surface.Session{stale.ID: stale}
	d.discoverCatalog(context.Background())
	if stored, err := r.Session(opened.ID); err != nil || stored.RuntimeKind != surface.RuntimeClaudeHost {
		t.Fatalf("stored=%+v err=%v, want the process the send reached kept on the host runtime", stored, err)
	}
	projection, _, err := r.CatalogProjectionFingerprint(opened.ID)
	if err != nil {
		t.Fatal(err)
	}
	var row struct {
		RuntimeKind surface.RuntimeKind `json:"runtimeKind"`
	}
	if err := json.Unmarshal([]byte(projection), &row); err != nil || row.RuntimeKind != surface.RuntimeClaudeHost {
		t.Fatalf("projection=%s err=%v, want the projection kept on the host runtime", projection, err)
	}
}
