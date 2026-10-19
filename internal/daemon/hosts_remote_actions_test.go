package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/surface"
)

func (f *federatedSurface) Model(_ context.Context, _ *surface.Session, name string) (string, error) {
	if name != "" {
		f.record("model:" + name)
	}
	return name, nil
}

func (f *federatedSurface) Compact(context.Context, *surface.Session) error {
	f.record("compact")
	return nil
}

func (f *federatedSurface) UpdateGoal(_ context.Context, _ *surface.Session, update surface.GoalUpdate) error {
	if update.Objective != nil {
		f.record("goal:" + *update.Objective)
	}
	return nil
}

func (f *federatedSurface) GoalClear(context.Context, *surface.Session) error {
	f.record("goal-clear")
	return nil
}

func (f *federatedSurface) ForkSession(_ context.Context, session *surface.Session, options surface.ForkOptions) (*surface.Session, error) {
	f.record("fork:" + options.Model)
	fork := surface.Session{ID: session.ID + "-fork", Surface: session.Surface, Name: "fork", Cwd: session.Cwd, Status: surface.StatusIdle}
	return &fork, nil
}

func (f *federatedSurface) NativeQueue(_ context.Context, _ *surface.Session, request surface.NativeQueueRequest) (map[string]any, error) {
	f.record("native-queue:" + request.Action)
	return map[string]any{"items": []any{}}, nil
}

func (f *federatedSurface) SessionAction(_ context.Context, _ *surface.Session, action string) (map[string]any, error) {
	f.record("lifecycle:" + action)
	return map[string]any{"state": "running"}, nil
}

func TestFederationRoutesEverySessionActionToTheOwningHost(t *testing.T) {
	local, remote, record := pairedFederation(t)
	qualified := hosts.Qualify(record.ID, "remote-a")
	actions := []map[string]any{
		{"action": "compact"},
		{"action": "model", "model": "gpt-remote"},
		{"action": "goal-set", "message": "ship it"},
		{"action": "goal-clear"},
		{"action": "session-fork", "fork": map[string]string{"model": "gpt-fork"}},
		{"action": "native-queue", "nativeQueue": map[string]string{"queueAction": "list"}},
		{"action": "session-lifecycle-status"},
		{"action": "send", "message": "when idle", "busyDelivery": "queue"},
		{"action": "alias", "alias": "remote-name"},
	}
	for _, action := range actions {
		action["sessionId"] = qualified
		response := local.request(t, http.MethodPost, "/api/v1/actions", action)
		if response.Code != http.StatusOK {
			t.Fatalf("%v status=%d body=%s", action["action"], response.Code, response.Body.String())
		}
		if action["action"] == "session-fork" && !strings.Contains(response.Body.String(), `"id":"`+hosts.Qualify(record.ID, "remote-a-fork")+`"`) {
			t.Fatalf("fork answer is not host-qualified: %s", response.Body.String())
		}
	}
	want := []string{"compact", "model:gpt-remote", "goal:ship it", "goal-clear", "fork:gpt-fork", "native-queue:list", "lifecycle:status", "when idle"}
	if got := remote.fake.delivered(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("remote applied %v, want %v", got, want)
	}
	if got := local.fake.delivered(); len(got) != 0 {
		t.Fatalf("local surface received remote actions: %v", got)
	}
	if id, err := remote.reg.ResolveTarget("remote-name"); err != nil || id != "remote-a" {
		t.Fatalf("alias on remote id=%q err=%v", id, err)
	}

	history := local.request(t, http.MethodGet, "/api/v1/history?limit=10&sessionId="+url.QueryEscape(qualified), nil)
	var page struct {
		Items []dashboardHistory `json:"items"`
	}
	if history.Code != http.StatusOK || json.Unmarshal(history.Body.Bytes(), &page) != nil || len(page.Items) == 0 {
		t.Fatalf("history status=%d body=%s", history.Code, history.Body.String())
	}
	for _, item := range page.Items {
		if item.SessionID != qualified {
			t.Fatalf("history item from another session or unqualified: %+v", item)
		}
	}
}

func TestFederationLaunchCatalogComesFromTheNamedHost(t *testing.T) {
	local, remote, record := pairedFederation(t)
	remote.fake.modelOptions = []surface.ModelOption{{ID: "remote-model", SupportedReasoningEfforts: []string{"low", "high"}}}
	var options struct {
		Host     dashboardHost `json:"host"`
		Surfaces []struct {
			ID        string `json:"id"`
			Connected bool   `json:"connected"`
		} `json:"surfaces"`
		Workspaces []string `json:"workspaces"`
	}
	response := local.request(t, http.MethodGet, "/api/v1/session-options?host=forge", nil)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &options) != nil {
		t.Fatalf("options status=%d body=%s", response.Code, response.Body.String())
	}
	remoteSession, _ := remote.reg.Session("remote-a")
	if options.Host.ID != record.ID || len(options.Surfaces) != 1 || options.Surfaces[0].ID != "codex" || !options.Surfaces[0].Connected || len(options.Workspaces) != 1 || options.Workspaces[0] != remoteSession.Cwd {
		t.Fatalf("remote options=%+v", options)
	}
	localIdentity, _ := local.reg.HostIdentity()
	response = local.request(t, http.MethodGet, "/api/v1/session-options", nil)
	if json.Unmarshal(response.Body.Bytes(), &options) != nil || options.Host.ID != localIdentity.ID {
		t.Fatalf("local options status=%d body=%s", response.Code, response.Body.String())
	}
	models := local.request(t, http.MethodGet, "/api/v1/models?surface=codex&host="+record.ID, nil)
	if models.Code != http.StatusOK || !strings.Contains(models.Body.String(), `"supportedReasoningEfforts":["low","high"]`) {
		t.Fatalf("models status=%d body=%s", models.Code, models.Body.String())
	}
}

func TestFederationRefusesActionsThatOnlyMeanSomethingOnOneHost(t *testing.T) {
	local, _, record := pairedFederation(t)
	qualified := hosts.Qualify(record.ID, "remote-a")
	for _, test := range []struct {
		request map[string]any
		reason  string
	}{
		{map[string]any{"action": "session-focus", "sessionId": qualified}, "only possible on that host"},
		{map[string]any{"action": "relay-add", "fromId": qualified, "toId": hosts.Qualify(record.ID, "remote-b"), "pattern": "."}, "relay-add is not available for sessions on another host yet"},
		{map[string]any{"action": "send", "sessionId": qualified, "sourceSessionId": hosts.Qualify(record.ID, "remote-b"), "message": "hi"}, "not available for sessions on another host yet"},
	} {
		response := local.request(t, http.MethodPost, "/api/v1/actions", test.request)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), test.reason) {
			t.Fatalf("%v status=%d body=%s", test.request["action"], response.Code, response.Body.String())
		}
	}
	if _, err := local.daemon.resolveVoiceTarget(context.Background(), qualified); err == nil || !strings.Contains(err.Error(), "voice is not available for sessions on another host yet") {
		t.Fatalf("voice err=%v", err)
	}
}

func TestLaunchWorkspacesListNewestFoldersOnce(t *testing.T) {
	sessions := []surface.Session{{Cwd: "/work/b"}, {Cwd: "/private/var/folders/x"}, {Cwd: "/work/a"}, {Cwd: "/work/b"}, {Cwd: ""}, {Cwd: "/home/u/.no-mistakes/run"}, {Cwd: "/tmp/scratch"}}
	if got := launchWorkspaces(sessions); strings.Join(got, ",") != "/work/b,/work/a" {
		t.Fatalf("workspaces=%v", got)
	}
}
