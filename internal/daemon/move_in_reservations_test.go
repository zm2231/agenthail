package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestAPairedHostsProjectMoveHeldForAnArrivingLeadAppliesWhenTheMoveEnds(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	claudetest.New(t)
	source, target := newMoveNode(t), newMoveNode(t)
	_, toSource := moveBothWays(t, source, target)
	const (
		conversation = "6f1c9d2e-0b0a-4a51-9f3e-6f9a1d2c3b4f"
		formerKey    = "repo:example.com/acme/old-widget"
		leadKey      = "repo:example.com/acme/widget"
	)
	manifest, _ := json.Marshal(map[string]any{"conversation": conversation, "alias": "widget-lead", "coordinator": registry.MovedCoordinator{ProjectID: leadKey, Name: "widget-lead", Surface: "claude"}})
	if _, err := target.reg.PrepareMoveIn("move-lead", conversation, toSource.ID, manifest); err != nil {
		t.Fatal(err)
	}
	moved := func() bool {
		moves, err := target.reg.ProjectMoves()
		return err == nil && slices.ContainsFunc(moves, func(m registry.ProjectMove) bool { return m.From == formerKey && m.To == leadKey })
	}

	move := registry.ProjectMove{From: formerKey, To: leadKey, ChangedAt: time.Now().UTC().Truncate(time.Millisecond), OriginHost: toSource.ID}
	if _, err := target.daemon.adoptProjectMoves([]registry.ProjectMove{move}); err != nil {
		t.Fatal(err)
	}
	if moved() {
		t.Fatal("the project moved onto the key of a lead still arriving")
	}
	if _, err := target.reg.AbortMoveIn("move-lead", conversation, toSource.ID); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the held project move to apply once the move ended", 10*time.Second, moved)
	if refusals, err := target.reg.ProjectMoveRefusals(); err != nil || len(refusals) != 0 {
		t.Fatalf("the held move was recorded as refused: %+v err=%v", refusals, err)
	}
}

func TestAMoveNamesTheRelayItCouldNotTakeOverBecauseItsTargetLeft(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	claudetest.New(t)
	source, target := newMoveNode(t), newMoveNode(t)
	var listener string
	var gone sync.Once
	next := target.handler
	target.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/session-move/transfer" {
			gone.Do(func() {
				if _, err := openSweepDB(t, target.reg.Path()).Exec(`DELETE FROM sessions WHERE id=?`, listener); err != nil {
					t.Error(err)
				}
			})
		}
		next.ServeHTTP(w, r)
	})
	_, toSource := moveBothWays(t, source, target)
	session := startMovable(t, source)
	listener = startMovable(t, source).ID
	mirroredOn(t, target, source, toSource.ID, listener)
	relay := relayFrom(t, source, session, listener)

	result, err := source.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "server"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.DroppedRelays) != 1 || result.DroppedRelays[0].RelayID != relay || result.DroppedRelays[0].ToSession != listener {
		t.Fatalf("the move answered dropped relays %+v, want relay #%d to %s", result.DroppedRelays, relay, listener)
	}
	if routes := routesFrom(t, target, session.ID); len(routes) != 0 {
		t.Fatalf("the target took over %+v", routes)
	}
}
