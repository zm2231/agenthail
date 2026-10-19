package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
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
	fork := surface.Session{ID: session.ID + "-fork", Surface: session.Surface, RuntimeKind: session.RuntimeKind, Name: "fork", Cwd: session.Cwd, Status: surface.StatusIdle}
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

	for index := 0; index < 160; index++ {
		if err := remote.reg.RecordHistory(registry.HistoryEntry{Kind: "send", SessionID: "remote-a", Message: "synthetic"}); err != nil {
			t.Fatal(err)
		}
	}
	for path, want := range map[string]int{"/api/v1/history?limit=150&sessionId=": 150, "/api/history?limit=150&sessionId=": 150} {
		history := local.request(t, http.MethodGet, path+url.QueryEscape(qualified), nil)
		var page struct {
			Items []dashboardHistory `json:"items"`
		}
		if history.Code != http.StatusOK || json.Unmarshal(history.Body.Bytes(), &page) != nil || len(page.Items) != want {
			t.Fatalf("%s status=%d items=%d", path, history.Code, len(page.Items))
		}
		for _, item := range page.Items {
			if item.SessionID != qualified {
				t.Fatalf("history item from another session or unqualified: %+v", item)
			}
		}
	}
}

func TestFederationLaunchCatalogComesFromTheNamedHost(t *testing.T) {
	local, remote, record := pairedFederation(t)
	remote.fake.modelOptions = []surface.ModelOption{{ID: "remote-model", SupportedReasoningEfforts: []string{"low", "high"}}}
	project := surface.Session{ID: "remote-project", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "project work", Cwd: "/srv/remote-project", Status: surface.StatusIdle, LastActive: time.Now()}
	if err := remote.reg.RegisterSession(project); err != nil {
		t.Fatal(err)
	}
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
	if options.Host.ID != record.ID || len(options.Surfaces) != 1 || options.Surfaces[0].ID != "codex" || !options.Surfaces[0].Connected || len(options.Workspaces) == 0 || options.Workspaces[0] != project.Cwd {
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
		{map[string]any{"action": "session-focus", "sessionId": qualified}, "opening the terminal is not available for sessions on another host yet"},
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

func TestFederationReportsAnOlderHostAsNeedingAnUpdate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	remote := newFederationNode(t, "remote-a")
	remote.serve(t, nil)
	local := newFederationNode(t)
	local.start(t, testRoots(remote.server))
	remote.gate.older.Store(true)
	refused := local.request(t, http.MethodPost, "/api/v1/hosts", map[string]string{"pairingUrl": remote.invite(t), "address": remote.address, "name": "forge"})
	if refused.Code == http.StatusCreated || !strings.Contains(refused.Body.String(), "update Agenthail on that host") {
		t.Fatalf("pairing an older host status=%d body=%s", refused.Code, refused.Body.String())
	}
	if devices, _ := remote.reg.ListDevices(false); len(devices) != 0 {
		t.Fatalf("refused pairing left a device on the host: %+v", devices)
	}

	remote.gate.older.Store(false)
	record := local.pair(t, remote, "forge")
	awaitFederation(t, "remote session mirrored", func() bool { return len(sessionsOf(local.snapshot(t), record.ID)) == 1 })
	remote.gate.older.Store(true)
	host, _ := local.daemon.pairedHost(record.ID)
	host.cancel()
	if err := local.daemon.attachHost(record); err != nil {
		t.Fatal(err)
	}
	awaitFederation(t, "older host marked incompatible", func() bool {
		rows := sessionsOf(local.snapshot(t), record.ID)
		return len(rows) == 1 && rows[0].Host.State == hostStateIncompatible && strings.Contains(rows[0].UnavailableReason, "update Agenthail on that host")
	})
	options := local.request(t, http.MethodGet, "/api/v1/session-options?host=forge", nil)
	if options.Code != http.StatusConflict || !strings.Contains(options.Body.String(), "host_incompatible") {
		t.Fatalf("an incompatible host's launch catalog status=%d body=%s", options.Code, options.Body.String())
	}
	history := local.request(t, http.MethodGet, "/api/v1/history?sessionId="+url.QueryEscape(hosts.Qualify(record.ID, "remote-a")), nil)
	send := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "send", "sessionId": hosts.Qualify(record.ID, "remote-a"), "message": "misread"})
	if history.Code != http.StatusConflict || send.Code != http.StatusConflict || len(remote.fake.delivered()) != 0 {
		t.Fatalf("history=%d send=%d body=%s delivered=%v", history.Code, send.Code, send.Body.String(), remote.fake.delivered())
	}
	if held, _ := local.reg.HostDeliveries(record.ID); len(held) != 0 {
		t.Fatalf("a refused send was held for the incompatible host: %+v", held)
	}
}

func TestFederationSendsNothingBeyondVersionUntilTheHostIsConfirmed(t *testing.T) {
	local, remote, record := pairedFederation(t)
	qualified := hosts.Qualify(record.ID, "remote-a")
	hold := &snapshotHold{release: make(chan struct{})}
	var released sync.Once
	release := func() { released.Do(func() { close(hold.release) }) }
	t.Cleanup(release)
	remote.gate.versions.Store(hold)
	host, _ := local.daemon.pairedHost(record.ID)
	host.cancel()
	if err := local.daemon.attachHost(record); err != nil {
		t.Fatal(err)
	}
	awaitFederation(t, "reconnect waits on the version check", func() bool { return hold.waiting.Load() > 0 })
	remote.gate.beyondVersion.Store(0)

	local.request(t, http.MethodGet, "/api/v1/history?sessionId="+url.QueryEscape(qualified), nil)
	streamCtx, cancelStream := context.WithTimeout(context.Background(), 2*time.Second)
	stream := httptest.NewRequestWithContext(streamCtx, http.MethodGet, "/api/v1/session-stream?id="+url.QueryEscape(qualified), nil)
	stream.Header.Set("Authorization", "Bearer "+dashboardTestToken)
	local.handler.ServeHTTP(httptest.NewRecorder(), stream)
	cancelStream()
	local.request(t, http.MethodGet, "/api/v1/session-options?host=forge", nil)
	send := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "send", "sessionId": qualified, "message": "before confirmed"})
	if got := remote.gate.beyondVersion.Load(); got != 0 {
		t.Fatalf("%d requests reached a host before its version was confirmed", got)
	}
	if !strings.Contains(send.Body.String(), `"status":"queued"`) {
		t.Fatalf("a send before the host is confirmed is not held: %s", send.Body.String())
	}

	remote.gate.older.Store(true)
	remote.gate.versions.Store(nil)
	release()
	awaitFederation(t, "older host marked incompatible", func() bool {
		current, _ := local.daemon.pairedHost(record.ID)
		state, _ := current.status()
		return state == hostStateIncompatible
	})
	history := local.request(t, http.MethodGet, "/api/v1/history?sessionId="+url.QueryEscape(qualified), nil)
	if history.Code != http.StatusConflict || !strings.Contains(history.Body.String(), "host_incompatible") {
		t.Fatalf("history after incompatibility status=%d body=%s", history.Code, history.Body.String())
	}
	if got := remote.gate.beyondVersion.Load(); got != 0 || len(remote.fake.delivered()) != 0 {
		t.Fatalf("an incompatible host received %d requests, delivered=%v", got, remote.fake.delivered())
	}
}

func TestFederationStopsReplayingHeldSendsOnceTheHostIsIncompatible(t *testing.T) {
	local, remote, record := pairedFederation(t)
	hold := &snapshotHold{release: make(chan struct{})}
	var released sync.Once
	release := func() { released.Do(func() { close(hold.release) }) }
	t.Cleanup(release)
	remote.gate.actions.Store(hold)
	var ids []int64
	for _, message := range []string{"first", "second"} {
		id, err := local.reg.QueueHostDelivery("remote-a", message, "fwd-"+message, []byte(`{"action":"send","sessionId":"remote-a","message":"`+message+`"}`), false)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	host, _ := local.daemon.pairedHost(record.ID)
	done := make(chan struct{})
	go func() {
		local.daemon.drainHostDeliveries(context.Background(), host)
		close(done)
	}()
	awaitFederation(t, "first held send reaches the host", func() bool { return hold.waiting.Load() == 1 })

	remote.gate.older.Store(true)
	_, err := host.client.Version(context.Background())
	if err == nil {
		t.Fatal("an older host passed the version check")
	}
	local.daemon.markHostDown(host, err)
	remote.gate.actions.Store(nil)
	release()
	<-done

	if got := remote.fake.delivered(); strings.Join(got, "|") != "first" {
		t.Fatalf("host received %v after it became incompatible", got)
	}
	if item, err := local.reg.QueueItem(ids[1], nil); err != nil || item.Status != "pending" {
		t.Fatalf("second held send item=%+v err=%v", item, err)
	}
}

func TestFederationMirrorKeepsEachSessionsRuntime(t *testing.T) {
	local, remote, record := pairedFederation(t)
	appServer := surface.Session{ID: "remote-c", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, ConfiguredModel: "gpt-remote", Name: "work remote-c", Status: surface.StatusIdle, Source: "agenthail", Transport: "managed", LastActive: time.Now()}
	if err := remote.reg.RegisterSession(appServer); err != nil {
		t.Fatal(err)
	}
	remote.fake.mu.Lock()
	remote.fake.sessions["remote-c"] = appServer
	remote.fake.mu.Unlock()
	remote.daemon.discoverCatalog(context.Background())
	awaitFederation(t, "app-server session mirrored", func() bool { return len(sessionsOf(local.snapshot(t), record.ID)) == 3 })

	check := func(stage string) {
		t.Helper()
		for id, want := range map[string]surface.RuntimeKind{"remote-a": surface.RuntimeCodexDesktop, "remote-c": surface.RuntimeCodexAppServer} {
			session, err := local.reg.Session(id)
			if owner, _ := local.reg.SessionHost(id); err != nil || owner != record.ID || session.RuntimeKind != want {
				t.Fatalf("%s: mirrored %s=%+v err=%v, want runtime %s", stage, id, session, err, want)
			}
			if _, err := local.daemon.adapters().For(session); err != nil {
				t.Fatalf("%s: mirrored %s does not resolve its runtime: %v", stage, id, err)
			}
			if id == "remote-c" && (session.ConfiguredModel != "gpt-remote" || session.Transport != "managed" || session.LastActive.IsZero()) {
				t.Fatalf("%s: mirrored row lost session fields: %+v", stage, session)
			}
		}
		for _, row := range sessionsOf(local.snapshot(t), record.ID) {
			if row.RuntimeKind == "" {
				t.Fatalf("%s: snapshot row %s has no runtime kind", stage, row.ID)
			}
		}
	}
	check("connected")
	host, _ := local.daemon.pairedHost(record.ID)
	local.daemon.markHostDown(host, errors.New("synthetic outage"))
	check("unavailable")
	routed := local.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "compact", "sessionId": hosts.Qualify(record.ID, "remote-c")})
	if routed.Code != http.StatusOK || strings.Join(remote.fake.delivered(), "|") != "compact" {
		t.Fatalf("a mirrored row with a local runtime is not routed to its host: %d %s %v", routed.Code, routed.Body.String(), remote.fake.delivered())
	}
}
