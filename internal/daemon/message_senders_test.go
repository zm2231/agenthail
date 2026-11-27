package daemon

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestEveryMessageAgenthailHandsAProviderNamesItsSender(t *testing.T) {
	d, r, fake, from, to := daemonFixture(t)
	fake.kind = surface.KindCodex
	fake.caps = surface.Capabilities{Send: true, Steer: true}
	cwd := t.TempDir()
	for _, step := range []struct {
		name   string
		body   map[string]any
		sender string
	}{
		{"an agent's message", map[string]any{"action": "send", "sessionId": to.ID, "sourceSessionId": from.ID, "message": "Rebase onto main"}, from.ID},
		{"the person's message", map[string]any{"action": "send", "sessionId": to.ID, "author": "person", "message": "Rename the flag"}, surface.PersonSessionID},
		{"the person's steer", map[string]any{"action": "steer", "sessionId": to.ID, "author": "person", "message": "Stop and summarise"}, surface.PersonSessionID},
		{"an agent's brief to the session it starts", map[string]any{"action": "session-create", "surface": "codex", "sourceSessionId": from.ID, "message": "Fix the parser", "cwd": cwd}, from.ID},
	} {
		before := len(fake.messageIDs)
		encoded, _ := json.Marshal(step.body)
		if w := serveDashboardRequest(dashboardRouter(d), http.MethodPost, "/api/action", string(encoded)); w.Code >= 300 {
			t.Fatalf("%s: %d %s", step.name, w.Code, w.Body.String())
		}
		if len(fake.messageIDs) != before+1 {
			t.Fatalf("%s reached the provider %d times", step.name, len(fake.messageIDs)-before)
		}
		id := fake.messageIDs[before]
		if sender, found, err := r.MessageSender(id); id == "" || err != nil || !found || sender != step.sender {
			t.Fatalf("%s: id %q sender=%q found=%v err=%v, want %q", step.name, id, sender, found, err, step.sender)
		}
	}

	adapter := &operationSurface{daemonSurface: fake}
	d.Surfaces = []surface.Surface{adapter}
	queued := `{"action":"native-queue","sessionId":"` + to.ID + `","sourceSessionId":"` + from.ID + `","nativeQueue":{"queueAction":"add","message":"Then tag the release","clientUserMessageId":"queued-1"}}`
	if w := serveDashboardRequest(dashboardRouter(d), http.MethodPost, "/api/action", queued); w.Code != http.StatusOK {
		t.Fatalf("native queue: %d %s", w.Code, w.Body.String())
	}
	if sender, found, err := r.MessageSender("queued-1"); err != nil || !found || sender != from.ID || adapter.queue.ClientID != "queued-1" {
		t.Fatalf("a message queued in Codex: sender=%q found=%v err=%v queue=%+v", sender, found, err, adapter.queue)
	}
}
