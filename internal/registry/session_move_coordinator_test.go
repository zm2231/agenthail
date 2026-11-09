package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	leadProject = "repo:github.com/example/widget"
	leadName    = "widget-lead"
	leadSource  = "h00aa11bb22cc"
)

func leadRegistry(t *testing.T, projectID string) *Registry {
	t.Helper()
	r := moveRegistry(t)
	if _, err := r.SetCoordinator(CoordinatorSpec{ProjectID: projectID, ProjectRoot: "/work/widget", Name: leadName, Surface: "claude", Cwd: "/work/widget", Instructions: "keep the widget shipping"}, CoordinatorChange{SessionID: moveConversation, ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestALeadMovesOnlyWithItsSessionAndOnlyAsTheSameProject(t *testing.T) {
	r := leadRegistry(t, leadProject)
	if refusal, err := r.SessionMoveRefusal(moveConversation, false); err != nil || !strings.Contains(refusal, "coordinator") {
		t.Fatalf("a move without its roles answered %q err=%v, want the coordinator refused", refusal, err)
	}
	if refusal, err := r.SessionMoveRefusal(moveConversation, true); err != nil || refusal != "" {
		t.Fatalf("a move that carries the lead was refused: %q err=%v", refusal, err)
	}

	local := leadRegistry(t, "project-widget")
	if refusal, err := local.SessionMoveRefusal(moveConversation, true); err != nil || !strings.Contains(refusal, "not a Git repository") {
		t.Fatalf("a lead of a project only this host knows answered %q err=%v, want it refused", refusal, err)
	}
}

func TestTheSourceGivesUpItsLeadOnlyWhenTheMoveCompletes(t *testing.T) {
	r := leadRegistry(t, leadProject)
	since, _, err := r.CoordinatorSince(leadProject, moveConversation)
	if err != nil {
		t.Fatal(err)
	}
	carry := carryOf(t, r)
	if carry.Coordinator == nil || carry.Coordinator.ProjectID != leadProject || carry.Coordinator.Name != leadName || carry.Coordinator.Instructions != "keep the widget shipping" || !carry.Coordinator.Since.Equal(since) {
		t.Fatalf("the move carries %+v, want the lead since %s", carry.Coordinator, since)
	}
	if err := r.BeginMoveOut("move-1", moveConversation, moveTarget, []byte(`{}`), carry, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ClearCoordinator(leadProject, "person", time.Time{}); err == nil || !strings.Contains(err.Error(), "moving to another host") {
		t.Fatalf("clearing the lead while it moves answered %v", err)
	}
	if spec, err := r.Coordinator(leadProject); err != nil || spec.Name != leadName {
		t.Fatalf("the lead left before the move completed: %+v err=%v", spec, err)
	}
	if err := r.SetMoveState("move-1", MoveSent, "", MovePreparing); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CompleteMoveOut("move-1", testPlaceholder); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Coordinator(leadProject); !errors.Is(err, ErrNoCoordinator) {
		t.Fatalf("the source keeps the lead after the move: %v", err)
	}
	if host, err := r.CoordinatorMovedTo(leadName); err != nil || host != moveTarget {
		t.Fatalf("the source says @%s went to %q err=%v, want %s", leadName, host, err, moveTarget)
	}
}

func TestTheTargetTakesTheLeadAsItWas(t *testing.T) {
	r := openTestRegistry(t)
	if _, err := r.AddHost(PairedHost{ID: leadSource, Name: "source", Endpoint: "https://source.example.ts.net:7412", Address: "100.64.0.1", Token: "ahd_secret"}); err != nil {
		t.Fatal(err)
	}
	entity := leadSource + "::" + moveConversation
	if _, _, err := r.ApplyHostCatalogRow(leadSource, HostCatalogRow{Session: surface.Session{ID: moveConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost}, EntityID: entity, Projection: json.RawMessage(`{"id":"` + entity + `"}`)}); err != nil {
		t.Fatal(err)
	}
	since := time.UnixMilli(1_700_000_000_000).UTC()
	if err := r.PutHostCoordinator(leadSource, CoordinatorEntry{ProjectID: leadProject, ProjectRoot: "/src/widget", Name: leadName, Surface: "claude", SessionID: entity, ChangedAt: since}); err != nil {
		t.Fatal(err)
	}
	lead := &MovedCoordinator{ProjectID: leadProject, Name: leadName, Surface: "claude", Instructions: "keep the widget shipping", ChangedBy: "fixture", Since: since}
	if err := r.CheckMovedCoordinator(moveConversation, lead, "project-elsewhere"); err == nil || !strings.Contains(err.Error(), "lands in project-elsewhere") {
		t.Fatalf("a lead landing in another project answered %v", err)
	}
	if err := r.CheckMovedCoordinator(moveConversation, lead, leadProject); err != nil {
		t.Fatal(err)
	}
	if _, err := r.PrepareMoveIn("move-1", moveConversation, leadSource, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := r.SetMoveState("move-1", MoveReceived, "", MovePrepared); err != nil {
		t.Fatal(err)
	}
	session := surface.Session{ID: moveConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: 77, Transcript: "/home/user/.claude/projects/x/t.jsonl", HasLocal: true}
	place := MovedCoordinatorPlace{ProjectRoot: "/target/widget", Cwd: "/target/widget"}
	if _, err := r.TakeMovedSession("move-1", session, leadName, time.Time{}, nil, MoveCarry{Coordinator: lead}, "", place); err != nil {
		t.Fatal(err)
	}
	spec, err := r.Coordinator(leadProject)
	if err != nil || spec.SessionID != moveConversation || spec.Cwd != place.Cwd || spec.Instructions != lead.Instructions || spec.MovedFrom != leadSource {
		t.Fatalf("the target's lead is %+v err=%v", spec, err)
	}
	if held, ok, err := r.CoordinatorSince(leadProject, moveConversation); err != nil || !ok || !held.Equal(since) {
		t.Fatalf("the target has the lead since %s ok=%v err=%v, want since %s", held, ok, err, since)
	}
	leads, err := r.ProjectCoordinators(leadProject)
	if err != nil || len(leads) != 1 || leads[0].HostID != "" {
		t.Fatalf("the target lists %+v err=%v while the source still lists the lead, want only its own", leads, err)
	}
	if err := r.CheckMovedCoordinator(moveConversation, lead, leadProject); err == nil || !strings.Contains(err.Error(), "already has coordinator") {
		t.Fatalf("a second lead for a project that has one answered %v", err)
	}
}

func TestAThirdHostListsOneLeadForAMovingConversation(t *testing.T) {
	const target = "h00dd44ee55ff"
	at := time.UnixMilli(1_700_000_000_000).UTC()
	third := func(t *testing.T, owner string) *Registry {
		t.Helper()
		r := openTestRegistry(t)
		for id, name := range map[string]string{leadSource: "source", target: "target"} {
			if _, err := r.AddHost(PairedHost{ID: id, Name: name, Endpoint: "https://" + name + ".example.ts.net:7412", Address: "100.64.0.9", Token: "ahd_" + name}); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.PutHostCoordinator(leadSource, CoordinatorEntry{ProjectID: leadProject, ProjectRoot: "/src/widget", Name: leadName, Surface: "claude", SessionID: leadSource + "::" + moveConversation, ChangedAt: at}); err != nil {
			t.Fatal(err)
		}
		if err := r.PutHostCoordinator(target, CoordinatorEntry{ProjectID: leadProject, ProjectRoot: "/target/widget", Name: leadName, Surface: "claude", SessionID: target + "::" + moveConversation, ChangedAt: at.Add(time.Minute), MovedFrom: leadSource}); err != nil {
			t.Fatal(err)
		}
		if owner != "" {
			entity := owner + "::" + moveConversation
			if _, _, err := r.ApplyHostCatalogRow(owner, HostCatalogRow{Session: surface.Session{ID: moveConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost}, EntityID: entity, Projection: json.RawMessage(`{"id":"` + entity + `"}`)}); err != nil {
				t.Fatal(err)
			}
		}
		return r
	}
	for _, test := range []struct {
		name, owner, want string
	}{
		{"before it knows who runs the conversation", "", leadSource},
		{"while the source runs it", leadSource, leadSource},
		{"once the target runs it", target, target},
	} {
		t.Run(test.name, func(t *testing.T) {
			leads, err := third(t, test.owner).ProjectCoordinators(leadProject)
			if err != nil || len(leads) != 1 || leads[0].HostID != test.want {
				t.Fatalf("listed %+v err=%v, want only the lead on %s", leads, err, test.want)
			}
		})
	}

	r := third(t, "")
	if err := r.PutHostCoordinator(target, CoordinatorEntry{ProjectID: leadProject, ProjectRoot: "/target/widget", Name: "other-lead", Surface: "claude", SessionID: target + "::another", ChangedAt: at}); err != nil {
		t.Fatal(err)
	}
	if leads, err := r.ProjectCoordinators(leadProject); err != nil || len(leads) != 2 {
		t.Fatalf("two leads of different conversations listed %+v err=%v, want both", leads, err)
	}
}

func TestAnUpgradedRegistryKeepsItsLeadHistoryAndRecordsAMovedLead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	old, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.RegisterSession(surface.Session{ID: moveConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, HasLocal: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := old.SetCoordinator(CoordinatorSpec{ProjectID: leadProject, ProjectRoot: "/work/widget", Name: leadName, Surface: "claude", Cwd: "/work/widget"}, CoordinatorChange{SessionID: moveConversation, ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	asBeforePresentationTriggers(t, old)
	var definition string
	if err := old.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='coordinator_history'`).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	unmoved := strings.Replace(definition, ",'moved'", "", 1)
	if unmoved == definition {
		t.Fatalf("the history table does not list moved: %s", definition)
	}
	for _, statement := range []string{
		`ALTER TABLE coordinator_history RENAME TO coordinator_history_current`,
		unmoved,
		`INSERT INTO coordinator_history SELECT * FROM coordinator_history_current`,
		`DROP TABLE coordinator_history_current`,
		`ALTER TABLE coordinators DROP COLUMN moved_from`,
		fmt.Sprintf(`PRAGMA user_version=%d`, schemaVersion-1),
	} {
		if _, err := old.db.Exec(statement); err != nil {
			old.Close()
			t.Fatal(err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	history, err := r.CoordinatorHistory(leadProject)
	if err != nil || len(history) != 1 || history[0].Action != "set" || history[0].Name != leadName {
		t.Fatalf("the upgraded history is %+v err=%v, want the lead's set kept", history, err)
	}
	presentation := func() int64 {
		var version int64
		if err := r.read.QueryRow(`SELECT version FROM catalog_presentation_version WHERE id=1`).Scan(&version); err != nil {
			t.Fatal(err)
		}
		return version
	}
	if _, err := r.AddHost(PairedHost{ID: moveTarget, Name: "server", Endpoint: "https://server.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_secret"}); err != nil {
		t.Fatal(err)
	}
	before := presentation()
	if err := r.BeginMoveOut("move-1", moveConversation, moveTarget, []byte(`{}`), carryOf(t, r), ""); err != nil {
		t.Fatal(err)
	}
	if err := r.SetMoveState("move-1", MoveSent, "", MovePreparing); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CompleteMoveOut("move-1", testPlaceholder); err != nil {
		t.Fatalf("the upgraded registry could not record the lead's move: %v", err)
	}
	if after := presentation(); after == before {
		t.Fatalf("the presentation version stayed %d while the lead moved, want the history triggers back", after)
	}
}
