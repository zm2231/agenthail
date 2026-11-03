package daemon

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
)

func mustJSONString(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func liveLocal(t *testing.T, r *registry.Registry, id string) bool {
	t.Helper()
	refs, err := r.LiveLocalSessions()
	if err != nil {
		t.Fatal(err)
	}
	return slices.ContainsFunc(refs, func(ref registry.SessionRef) bool { return ref.ID == id })
}

func TestASpawnerArchivesItsChildButNotTheOtherWay(t *testing.T) {
	f := newMCPWriteFixture(t)
	if code := refusalCode(t, f.child, "agenthail_archive", map[string]any{"target": "from", "clientRequestId": "a1"}); code != "out_of_scope" {
		t.Fatalf("child archiving its spawner: code=%q", code)
	}
	if out, refused := callMCP(t, f.parent, "agenthail_archive", map[string]any{"target": "to", "clientRequestId": "a2"}); refused || liveLocal(t, f.r, "to") {
		t.Fatalf("archive: %v, still live=%v", out, liveLocal(t, f.r, "to"))
	}
}

func TestOnlyTheOperatorChoosesCoordinatorsAndReadsEveryonesHistory(t *testing.T) {
	f := newMCPWriteFixture(t)
	if code := refusalCode(t, f.parent, "agenthail_coordinator", map[string]any{"action": "clear", "project": "x", "clientRequestId": "k1"}); code != "capability_missing" {
		t.Fatalf("worker setting coordinators: code=%q", code)
	}
	if code := refusalCode(t, f.parent, "agenthail_history", nil); code != "capability_missing" {
		t.Fatalf("worker reading all history: code=%q", code)
	}
	session := asOperator(t, f.d, mcpServer(t, f.d), "from")
	callMCP(t, session, "agenthail_send", map[string]any{"target": "to", "message": "logged", "clientRequestId": "h1"})
	history, refused := callMCP(t, session, "agenthail_history", map[string]any{"limit": 5})
	if refused || !strings.Contains(mustJSONString(t, history), "logged") {
		t.Fatalf("operator history=%v", history)
	}
	if out, refused := callMCP(t, session, "agenthail_history", map[string]any{"view": "queue"}); refused {
		t.Fatalf("operator queue: %v", out)
	}
}

func TestQueueRelayChannelAndNotionToolsAreTheOperators(t *testing.T) {
	f := newMCPWriteFixture(t)
	workerCalls := map[string]map[string]any{
		"agenthail_queue":   {"action": "clear", "target": "to", "clientRequestId": "w1"},
		"agenthail_relay":   {"action": "list"},
		"agenthail_channel": {"action": "list"},
		"agenthail_create":  {"surface": "notion", "message": "draft notes", "clientRequestId": "w2"},
	}
	for tool, args := range workerCalls {
		if code := refusalCode(t, f.parent, tool, args); code != "capability_missing" && code != "exceeds_ceiling" {
			t.Fatalf("worker calling %s: code=%q", tool, code)
		}
	}
	if err := f.r.QueueMessage("to", "stale instruction"); err != nil {
		t.Fatal(err)
	}
	operator := asOperator(t, f.d, mcpServer(t, f.d), "from")
	if out, refused := callMCP(t, operator, "agenthail_queue", map[string]any{"action": "clear", "target": "to", "clientRequestId": "o1"}); refused || f.r.QueueCount("to") != 0 {
		t.Fatalf("operator clearing the queue: %v, pending=%d", out, f.r.QueueCount("to"))
	}
	if out, refused := callMCP(t, operator, "agenthail_channel", map[string]any{"action": "create", "channel": "ops", "clientRequestId": "o2"}); refused {
		t.Fatalf("operator creating a channel: %v", out)
	}
	if out, refused := callMCP(t, operator, "agenthail_channel", map[string]any{"action": "add", "channel": "ops", "target": "to", "clientRequestId": "o3"}); refused {
		t.Fatalf("operator adding a member: %v", out)
	}
	channels, refused := callMCP(t, operator, "agenthail_channel", map[string]any{"action": "list"})
	if refused || !strings.Contains(mustJSONString(t, channels), `"to"`) {
		t.Fatalf("channel list=%v", channels)
	}
	if out, refused := callMCP(t, operator, "agenthail_relay", map[string]any{"action": "add", "from": "to", "to": "from", "pattern": "DONE", "clientRequestId": "o4"}); refused {
		t.Fatalf("operator adding a relay: %v", out)
	}
	relays, refused := callMCP(t, operator, "agenthail_relay", map[string]any{"action": "list"})
	if refused || !strings.Contains(mustJSONString(t, relays), `"DONE"`) {
		t.Fatalf("relay list=%v", relays)
	}
}

func TestAChannelPostIsSentAsTheCaller(t *testing.T) {
	f := newMCPWriteFixture(t)
	person := principal{ID: localDashboardPrincipal, Kind: principalPerson}
	for _, request := range []dashboardActionRequest{{Action: "channel-create", Channel: "ops"}, {Action: "channel-add", Channel: "ops", TargetID: "from"}} {
		if reply := f.d.doAction(t.Context(), person, request); reply.Status != 200 {
			t.Fatalf("%s status=%d %s", request.Action, reply.Status, reply.Body)
		}
	}
	if out, refused := callMCP(t, f.child, "agenthail_channel_send", map[string]any{"channel": "ops", "message": "status: green", "clientRequestId": "p1"}); refused {
		t.Fatalf("channel send: %v", out)
	}
	if code := refusalCode(t, f.child, "agenthail_channel_send", map[string]any{"channel": "ops", "message": "no key"}); code != "invalid" {
		t.Fatalf("channel send without clientRequestId: code=%q", code)
	}
}
