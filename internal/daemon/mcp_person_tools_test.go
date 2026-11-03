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
	server := mcpServer(t, f.d)
	f.d.attachMCPCredentials(strings.TrimPrefix(server.URL, "http://"))
	operator, _ := f.d.Registry.Session("from")
	makeVoiceOperator(t, f.d, *operator)
	f.d.leadsChanged()
	token, _ := f.d.mcpCredentials.Load().Live("from")
	session := mcpSession(t, server, token)
	callMCP(t, session, "agenthail_send", map[string]any{"target": "to", "message": "logged", "clientRequestId": "h1"})
	history, refused := callMCP(t, session, "agenthail_history", map[string]any{"limit": 5})
	if refused || !strings.Contains(mustJSONString(t, history), "logged") {
		t.Fatalf("operator history=%v", history)
	}
	if out, refused := callMCP(t, session, "agenthail_history", map[string]any{"view": "queue"}); refused {
		t.Fatalf("operator queue: %v", out)
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
