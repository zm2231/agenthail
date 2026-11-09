package registry

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	arrivalSource = "h00aa11bb22dd"
	arrivalHome   = "h00aa11bb22ee"
	arrivalPeer   = "h00aa11bb22ff"
)

func arrive(t *testing.T, r *Registry, alias string, carry MoveCarry) {
	t.Helper()
	manifest, err := json.Marshal(arrivingMove{Conversation: moveConversation, Alias: alias, MoveCarry: carry})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.PrepareMoveIn("move-in", moveConversation, arrivalSource, manifest); err != nil {
		t.Fatal(err)
	}
	if err := r.SetMoveState("move-in", MoveReceived, "", MovePrepared); err != nil {
		t.Fatal(err)
	}
}

func takeArrived(t *testing.T, r *Registry, alias string, carry MoveCarry) {
	t.Helper()
	session := surface.Session{ID: moveConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: 7, Transcript: "/tmp/t.jsonl", HasLocal: true}
	if _, err := r.TakeMovedSession("move-in", session, alias, time.Time{}, nil, carry, "", MovedCoordinatorPlace{ProjectRoot: "/target/widget", Cwd: "/target/widget"}); err != nil {
		t.Fatalf("the take after a refused change failed: %v", err)
	}
}

func otherSession(t *testing.T, r *Registry, id string) {
	t.Helper()
	if err := r.RegisterSession(surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer}); err != nil {
		t.Fatal(err)
	}
}

func refusedForArrival(t *testing.T, what string, err error) {
	t.Helper()
	var reserved MoveInReservedError
	if !errors.As(err, &reserved) || reserved.Conversation != moveConversation {
		t.Fatalf("%s while a session moves here answered %v", what, err)
	}
}

func TestANameArrivingWithAMoveStaysFreeForItUntilTheTake(t *testing.T) {
	r := registryWithHosts(t, arrivalSource)
	otherSession(t, r, "codex-other")
	arrive(t, r, "builder", MoveCarry{})

	refusedForArrival(t, "naming another session as the arriving name", r.SetAlias("builder", "codex-other"))
	if generated, err := r.ReserveGeneratedAlias("codex-other", "builder"); err != nil || generated != "builder-2" {
		t.Fatalf("a generated name took %q err=%v, want builder-2", generated, err)
	}
	takeArrived(t, r, "builder", MoveCarry{})
	if owner, err := r.LookupAlias("builder"); err != nil || owner != moveConversation {
		t.Fatalf("@builder names %q err=%v after the take", owner, err)
	}
}

func TestALeadArrivingWithAMoveKeepsItsProjectAndNameUntilTheTake(t *testing.T) {
	r := registryWithHosts(t, arrivalSource)
	lead := &MovedCoordinator{ProjectID: leadProject, Name: leadName, Surface: "claude", Since: time.UnixMilli(1_700_000_000_000).UTC()}
	arrive(t, r, leadName, MoveCarry{Coordinator: lead})

	_, err := r.SetCoordinator(CoordinatorSpec{ProjectID: leadProject, ProjectRoot: "/target/widget", Name: "other-lead", Surface: "claude", Cwd: "/target/widget"}, CoordinatorChange{ChangedBy: "person"})
	refusedForArrival(t, "setting a lead for the arriving lead's project", err)
	_, err = r.SetCoordinator(CoordinatorSpec{ProjectID: "repo:github.com/example/other", ProjectRoot: "/target/other", Name: leadName, Surface: "claude", Cwd: "/target/other"}, CoordinatorChange{ChangedBy: "person"})
	refusedForArrival(t, "setting a lead under the arriving lead's name", err)
	takeArrived(t, r, leadName, MoveCarry{Coordinator: lead})
	if spec, err := r.Coordinator(leadProject); err != nil || spec.SessionID != moveConversation || spec.MovedFrom != arrivalSource {
		t.Fatalf("the lead after the take is %+v err=%v", spec, err)
	}
}

func TestTheHostsAnArrivingSessionReachesStayPairedUntilTheTake(t *testing.T) {
	r := registryWithHosts(t, arrivalSource, arrivalHome, arrivalPeer)
	const target = "relay-target"
	mirror(t, r, arrivalPeer, target)
	carry := MoveCarry{Channels: []MovedChannel{{Name: "builders", Home: arrivalHome}}, Relays: []MovedRelay{{ID: 4, ToSession: target, ToHost: arrivalPeer, Pattern: ".*", Handoff: HandoffTurnFinished}}}
	arrive(t, r, "", carry)

	_, err := r.RemoveHost(arrivalHome)
	refusedForArrival(t, "unpairing the host that keeps the arriving session's channel", err)
	_, err = r.RemoveHost(arrivalPeer)
	refusedForArrival(t, "unpairing the host the arriving session relays to", err)
	takeArrived(t, r, "", carry)
	if homes, err := r.RemoteChannelHomes(moveConversation, "builders"); err != nil || !slices.Equal(homes, []string{arrivalHome}) {
		t.Fatalf("#builders is kept on %v err=%v after the take", homes, err)
	}
	if routes := relaysFrom(t, r, moveConversation); !slices.Equal(routes, []string{target}) {
		t.Fatalf("the moved session relays to %v, want %s", routes, target)
	}
	if _, err := r.RemoveHost(arrivalHome); err != nil {
		t.Fatalf("unpairing once the move was taken: %v", err)
	}
}

func TestARelayToASessionThatMovedOnDuringTheMoveIsTakenOver(t *testing.T) {
	r := registryWithHosts(t, arrivalSource, arrivalPeer)
	identity, err := r.HostIdentity()
	if err != nil {
		t.Fatal(err)
	}
	const target = "relay-target"
	if err := r.RegisterSession(surface.Session{ID: target, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: 9, Transcript: "/tmp/target.jsonl", HasLocal: true}); err != nil {
		t.Fatal(err)
	}
	relay := MovedRelay{ID: 5, ToSession: target, ToHost: identity.ID, Pattern: ".*", Handoff: HandoffTurnFinished}
	if err := r.CheckMovedRelays(moveConversation, identity.ID, []MovedRelay{relay}); err != nil {
		t.Fatal(err)
	}
	arrive(t, r, "", MoveCarry{Relays: []MovedRelay{relay}})

	if err := r.BeginMoveOut("move-out", target, arrivalPeer, []byte(`{}`), MoveCarry{}, identity.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.SetMoveState("move-out", MoveSent, "", MovePreparing); err != nil {
		t.Fatal(err)
	}
	entity := arrivalPeer + "::" + target
	if _, err := r.CompleteMoveOut("move-out", MovePlaceholder{EntityID: entity, Projection: json.RawMessage(`{"id":"` + entity + `","surface":"claude"}`), UnavailableReason: "moved"}); err != nil {
		t.Fatal(err)
	}
	if owner, err := r.SessionHost(target); err != nil || owner != arrivalPeer {
		t.Fatalf("the relay's target runs on %q err=%v, want it moved to %s", owner, err, arrivalPeer)
	}
	takeArrived(t, r, "", MoveCarry{Relays: []MovedRelay{relay}})
	if routes := relaysFrom(t, r, moveConversation); !slices.Equal(routes, []string{target}) {
		t.Fatalf("the moved session relays to %v, want %s", routes, target)
	}
	if dropped, err := r.MoveDroppedRelays("move-in"); err != nil || len(dropped) != 0 {
		t.Fatalf("dropped %+v err=%v", dropped, err)
	}
}

func TestARelayToASessionGoneDuringTheMoveIsDroppedAndNamed(t *testing.T) {
	r := registryWithHosts(t, arrivalSource)
	const target = "local-target"
	otherSession(t, r, target)
	relay := MovedRelay{ID: 6, ToSession: target, ToHost: "", Pattern: ".*", Handoff: HandoffTurnFinished}
	arrive(t, r, "", MoveCarry{Relays: []MovedRelay{relay}})
	if _, err := r.db.Exec(`DELETE FROM sessions WHERE id=?`, target); err != nil {
		t.Fatal(err)
	}

	takeArrived(t, r, "", MoveCarry{Relays: []MovedRelay{relay}})
	if routes := relaysFrom(t, r, moveConversation); len(routes) != 0 {
		t.Fatalf("the moved session relays to %v", routes)
	}
	want := DroppedRelay{MoveID: "move-in", SessionID: moveConversation, RelayID: 6, ToSession: target}
	for name, read := range map[string]func() ([]DroppedRelay, error){"the move's answer": func() ([]DroppedRelay, error) { return r.MoveDroppedRelays("move-in") }, "doctor": r.DroppedRelays} {
		dropped, err := read()
		if err != nil || len(dropped) != 1 {
			t.Fatalf("%s names %+v err=%v", name, dropped, err)
		}
		got := dropped[0]
		got.At = time.Time{}
		if got != want {
			t.Fatalf("%s names %+v, want %+v", name, got, want)
		}
	}
	history, err := r.ListHistory(10, moveConversation)
	if err != nil || !slices.ContainsFunc(history, func(entry HistoryEntry) bool { return strings.Contains(entry.Result, "relay #6 was not taken over") }) {
		t.Fatalf("history %+v err=%v does not name the dropped relay", history, err)
	}
}

func TestAProjectKeyMoveWaitsForAnArrivingLeadAndAppliesOnceTheMoveEnds(t *testing.T) {
	const formerKey = "repo:github.com/example/old-widget"
	lead := &MovedCoordinator{ProjectID: leadProject, Name: leadName, Surface: "claude"}
	set := func(r *Registry, projectID, name string) {
		t.Helper()
		if _, err := r.SetCoordinator(CoordinatorSpec{ProjectID: projectID, ProjectRoot: "/target/widget", Name: name, Surface: "claude", Cwd: "/target/widget"}, CoordinatorChange{ChangedBy: "person"}); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("a local key change, until the take", func(t *testing.T) {
		r := registryWithHosts(t, arrivalSource)
		if _, err := r.ObserveRepositoryKey("/work/widget", leadProject); err != nil {
			t.Fatal(err)
		}
		arrive(t, r, leadName, MoveCarry{Coordinator: lead})
		outcome, err := r.ObserveRepositoryKey("/work/widget", formerKey)
		if err != nil || !outcome.Waiting || outcome.Moved || outcome.Refused != nil {
			t.Fatalf("moving the arriving lead's project away answered %+v err=%v, want it waiting", outcome, err)
		}
		if refusals, err := r.ProjectMoveRefusals(); err != nil || len(refusals) != 0 {
			t.Fatalf("a waiting move is recorded as refused: %+v err=%v", refusals, err)
		}
		takeArrived(t, r, leadName, MoveCarry{Coordinator: lead})
		released, err := r.ReleaseWaitingProjectMoves()
		if err != nil || len(released) != 1 || !released[0].Moved || released[0].Move.From != leadProject || released[0].Move.To != formerKey {
			t.Fatalf("releasing after the take answered %+v err=%v", released, err)
		}
		if spec, err := r.Coordinator(formerKey); err != nil || spec.SessionID != moveConversation {
			t.Fatalf("the lead after the key moved is %+v err=%v, want the arrived lead under %s", spec, err, formerKey)
		}
		if outcome, err := r.ObserveRepositoryKey("/work/widget", formerKey); err != nil || outcome.Moved || outcome.Waiting {
			t.Fatalf("the folder's key was not recorded with the move: %+v err=%v", outcome, err)
		}
	})

	t.Run("a paired host's key change, until an abort", func(t *testing.T) {
		r := registryWithHosts(t, arrivalSource)
		set(r, formerKey, "old-lead")
		arrive(t, r, leadName, MoveCarry{Coordinator: lead})
		move := ProjectMove{From: formerKey, To: leadProject, ChangedAt: time.UnixMilli(1_700_000_000_000).UTC(), OriginHost: arrivalSource}
		if outcomes, err := r.AdoptProjectMoves([]ProjectMove{move}); err != nil || len(outcomes) != 0 {
			t.Fatalf("adopting a move onto the arriving lead's project answered %+v err=%v", outcomes, err)
		}
		if released, err := r.ReleaseWaitingProjectMoves(); err != nil || len(released) != 0 {
			t.Fatalf("a move released while the lead still arrives: %+v err=%v", released, err)
		}
		if _, err := r.AbortMoveIn("move-in", moveConversation, arrivalSource); err != nil {
			t.Fatal(err)
		}
		released, err := r.ReleaseWaitingProjectMoves()
		if err != nil || len(released) != 1 || !released[0].Moved {
			t.Fatalf("releasing after the abort answered %+v err=%v", released, err)
		}
		if spec, err := r.Coordinator(leadProject); err != nil || spec.Name != "old-lead" {
			t.Fatalf("the lead after the key moved is %+v err=%v", spec, err)
		}
		if moves, err := r.ProjectMoves(); err != nil || !slices.ContainsFunc(moves, func(m ProjectMove) bool { return m.From == formerKey && m.To == leadProject }) {
			t.Fatalf("the adopted move is not recorded: %+v err=%v", moves, err)
		}
	})

	at := time.UnixMilli(1_700_000_000_000).UTC()
	settled := func(t *testing.T, from string, held ...[]ProjectMove) string {
		t.Helper()
		r := registryWithHosts(t, arrivalSource)
		arrive(t, r, leadName, MoveCarry{Coordinator: lead})
		for _, moves := range held {
			if _, err := r.AdoptProjectMoves(moves); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := r.AbortMoveIn("move-in", moveConversation, arrivalSource); err != nil {
			t.Fatal(err)
		}
		if _, err := r.ReleaseWaitingProjectMoves(); err != nil {
			t.Fatal(err)
		}
		moves, err := r.ProjectMoves()
		index := slices.IndexFunc(moves, func(m ProjectMove) bool { return m.From == from })
		if err != nil || index < 0 {
			t.Fatalf("no move recorded for %s: %+v err=%v", from, moves, err)
		}
		return moves[index].To
	}

	t.Run("two held moves at one instant settle on the later origin, in either order", func(t *testing.T) {
		low := ProjectMove{From: leadProject, To: "repo:github.com/example/low", ChangedAt: at, OriginHost: "h1111111111aa"}
		high := ProjectMove{From: leadProject, To: "repo:github.com/example/high", ChangedAt: at, OriginHost: "h2222222222aa"}
		for _, order := range [][]ProjectMove{{low}, {high}} {
			rest := high
			if order[0] == high {
				rest = low
			}
			if got := settled(t, leadProject, order, []ProjectMove{rest}); got != high.To {
				t.Fatalf("arriving %s first settled on %s, want %s", order[0].OriginHost, got, high.To)
			}
		}
	})

	t.Run("a later move of a held key waits behind it even when it leaves the lead alone", func(t *testing.T) {
		held := ProjectMove{From: formerKey, To: leadProject, ChangedAt: at, OriginHost: arrivalSource}
		later := ProjectMove{From: formerKey, ChangedAt: at.Add(time.Second), OriginHost: arrivalSource}
		if got := settled(t, formerKey, []ProjectMove{held}, []ProjectMove{later}); got != "" {
			t.Fatalf("the earlier held move overrode the later one: settled on %q", got)
		}
	})
}

func mirror(t *testing.T, r *Registry, host, id string) {
	t.Helper()
	entity := host + "::" + id
	if _, _, err := r.ApplyHostCatalogRow(host, HostCatalogRow{Session: surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer}, EntityID: entity, Projection: json.RawMessage(`{"id":"` + entity + `"}`)}); err != nil {
		t.Fatal(err)
	}
}

func relaysFrom(t *testing.T, r *Registry, session string) []string {
	t.Helper()
	routes, err := queryRows(r.read, []string{}, func(row rowScanner) (string, error) {
		var to string
		return to, row.Scan(&to)
	}, `SELECT to_session FROM routes WHERE from_session=? ORDER BY id`, session)
	if err != nil {
		t.Fatal(err)
	}
	return routes
}
