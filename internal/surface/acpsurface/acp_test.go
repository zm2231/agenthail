package acpsurface

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/acp/acptest"
	"github.com/zm2231/agenthail/internal/acphost"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestMain(m *testing.M) {
	acptest.RunIfAgent()
	os.Exit(m.Run())
}

func fakeAgent(t *testing.T, id string) acphost.Agent {
	argv, env := acptest.Command(t.TempDir())
	return acphost.Agent{ID: id, Label: "Fake " + id, Command: argv, Env: env, Modes: map[string]string{"ask": "plan", "default": "edits", "bypass": "full"}}
}

func scratchHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "acps")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0o700); err != nil {
		t.Fatal(err)
	}
	return home
}

func startHost(t *testing.T, agents ...acphost.Agent) string {
	t.Helper()
	return serveHost(t, scratchHome(t), agents...)
}

func serveHost(t *testing.T, home string, agents ...acphost.Agent) string {
	t.Helper()
	host := acphost.NewHost(home, "test", agents, nil)
	socket := acphost.SocketPath(home)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = host.Serve(listener, socket) }()
	t.Cleanup(func() {
		states, _ := (acphost.Client{Home: home}).Held(context.Background())
		for _, state := range states {
			_ = (acphost.Client{Home: home}).Close(context.Background(), state.SessionID)
		}
	})
	return home
}

type ready struct{ sessions []surface.Session }

func start(t *testing.T, adapter *ACP, message string) *surface.Session {
	t.Helper()
	got := &ready{}
	ctx := surface.WithSessionReady(context.Background(), func(session surface.Session) error {
		got.sessions = append(got.sessions, session)
		return nil
	})
	adapter.OpensConversations(func(string, error) {})
	session, sent, err := adapter.StartSession(ctx, surface.SessionStartOptions{Cwd: t.TempDir(), Message: message})
	if err != nil || session == nil || sent == nil || !sent.Accepted {
		t.Fatalf("session=%+v sent=%+v err=%v", session, sent, err)
	}
	if len(got.sessions) != 1 || got.sessions[0].ID != session.ID || got.sessions[0].RuntimeKind != Kind(adapter.agent.ID) {
		t.Fatalf("ready=%+v", got.sessions)
	}
	return session
}

func waitIdle(t *testing.T, adapter *ACP, session *surface.Session) *surface.TurnObservation {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		observation, err := adapter.Observe(context.Background(), session)
		if err == nil && observation.Status == surface.StatusIdle && observation.CompletedTurnID != "" {
			return observation
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the session never went idle")
	return nil
}

func TestAdapterStartsReadsAndStreamsASession(t *testing.T) {
	home := startHost(t, fakeAgent(t, "fake"))
	adapter := New(home, fakeAgent(t, "fake"))
	session := start(t, adapter, "tool reply:first-reply")
	observation := waitIdle(t, adapter, session)
	read, err := adapter.ReadSession(context.Background(), session, surface.SessionReadRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if read.Reply == nil || read.Reply.Text != "first-reply" || !read.TranscriptOffsetSet || read.TranscriptIdentity == "" {
		t.Fatalf("read=%+v", read)
	}
	kinds := map[string]int{}
	for _, item := range read.Items {
		kinds[item.Kind]++
	}
	if kinds["toolCall"] != 1 || kinds["toolResult"] != 1 || kinds["done"] != 1 || kinds["message"] != 2 {
		t.Fatalf("a growing item must appear once: kinds=%v items=%+v", kinds, read.Items)
	}
	items, err := adapter.TranscriptItems(context.Background(), session, surface.TranscriptItemsRequest{Identity: read.TranscriptIdentity})
	if err != nil || len(items.Items) != len(read.Items) {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if _, err := adapter.TranscriptItems(context.Background(), session, surface.TranscriptItemsRequest{Identity: "another"}); err != surface.ErrTranscriptChanged {
		t.Fatalf("a different identity must be reported changed: %v", err)
	}

	stream := *session
	stream.TranscriptOffset, stream.TranscriptOffsetSet, stream.TranscriptIdentity = read.TranscriptOffset, true, read.TranscriptIdentity
	events := make(chan surface.StreamEvent, 32)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = adapter.Stream(ctx, &stream, "", func(event surface.StreamEvent) { events <- event }, time.Minute)
	}()
	if _, err := adapter.Send(context.Background(), session, "reply:second-reply"); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Text == "first-reply" {
				t.Fatal("the stream must start at the given offset")
			}
			if event.ProviderKey != "timeline:"+event.ID {
				t.Fatalf("a streamed item must carry the key its seeded row has: %+v", event)
			}
			if event.Kind == "message" && event.Role == "assistant" && event.Text == "second-reply" {
				if event.TurnID == "" || event.TurnID == observation.CompletedTurnID {
					t.Fatalf("event=%+v", event)
				}
				return
			}
		case <-deadline:
			t.Fatal("the stream never carried the second reply")
		}
	}
}

func TestAdapterAnswersPermissionsAsApprovals(t *testing.T) {
	home := startHost(t, fakeAgent(t, "fake"))
	adapter := New(home, fakeAgent(t, "fake"))
	session := start(t, adapter, "ask")
	var state surface.RequestState
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && len(state.Requests) == 0 {
		state, _ = adapter.Requests(context.Background(), session)
		time.Sleep(20 * time.Millisecond)
	}
	if len(state.Requests) != 1 {
		t.Fatalf("state=%+v", state)
	}
	request := state.Requests[0]
	if request.Kind != surface.WaitingApproval || request.Tool != "Bash" || !strings.Contains(request.Detail(), "echo hi") || request.AlwaysAllow || request.NoAllow {
		t.Fatalf("request=%+v detail=%q", request, request.Detail())
	}
	actions := request.Actions()
	ids := []string{}
	for _, action := range actions {
		ids = append(ids, action.ID)
	}
	if strings.Join(ids, ",") != "allow,deny,stop" {
		t.Fatalf("actions=%v", ids)
	}
	observation, err := adapter.Observe(context.Background(), session)
	if err != nil || observation.Status != surface.StatusBusy || observation.WaitingOn == nil || observation.WaitingOn.ID != request.ID {
		t.Fatalf("a waiting session must be busy with its request: %+v err=%v", observation, err)
	}
	if _, err := adapter.Respond(context.Background(), session, request.ID, surface.Decision{Behavior: "allow", Always: true}); err == nil {
		t.Fatal("an always answer must be refused")
	}
	if outcome, err := adapter.Respond(context.Background(), session, request.ID, surface.Decision{Behavior: "allow"}); err != nil || outcome != "allowed" {
		t.Fatalf("outcome=%q err=%v", outcome, err)
	}
	if _, err := adapter.Respond(context.Background(), session, request.ID, surface.Decision{Behavior: "allow"}); err != surface.ErrRequestStale {
		t.Fatalf("a second answer must be stale: %v", err)
	}
	waitIdle(t, adapter, session)
	if reply, err := adapter.Reply(context.Background(), session, 1); err != nil || reply.Text != "allowed" {
		t.Fatalf("reply=%+v err=%v", reply, err)
	}

	if _, err := adapter.Send(context.Background(), session, "noallow"); err != nil {
		t.Fatal(err)
	}
	state = surface.RequestState{}
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && len(state.Requests) == 0 {
		state, _ = adapter.Requests(context.Background(), session)
		time.Sleep(20 * time.Millisecond)
	}
	if len(state.Requests) != 1 || !state.Requests[0].NoAllow {
		t.Fatalf("state=%+v", state)
	}
	for _, action := range state.Requests[0].Actions() {
		if action.ID == "allow" {
			t.Fatal("a request with no one-time allow must offer no allow")
		}
	}
	if _, err := state.Requests[0].Resolve(surface.Response{Choices: []string{"allow"}}); err == nil {
		t.Fatal("allow must not resolve")
	}
}

func TestEachAgentIsRoutedToItsOwnAdapter(t *testing.T) {
	a, b := fakeAgent(t, "fake-a"), fakeAgent(t, "fake-b")
	home := startHost(t, a, b)
	adapters := surface.Adapters{New(home, a), New(home, b)}
	first := start(t, adapters[0].(*ACP), "reply:from-a")
	second := start(t, adapters[1].(*ACP), "reply:from-b")
	for _, pair := range []struct {
		session *surface.Session
		want    *ACP
		reply   string
	}{{first, adapters[0].(*ACP), "from-a"}, {second, adapters[1].(*ACP), "from-b"}} {
		adapter, err := adapters.For(pair.session)
		if err != nil || adapter != pair.want {
			t.Fatalf("session %s routed to %v err=%v", pair.session.ID, adapter, err)
		}
		waitIdle(t, pair.want, pair.session)
		if reply, err := pair.want.Reply(context.Background(), pair.session, 1); err != nil || reply.Text != pair.reply {
			t.Fatalf("reply=%+v err=%v", reply, err)
		}
		listed, err := pair.want.List(context.Background())
		if err != nil || len(listed) != 1 || listed[0].ID != pair.session.ID || listed[0].Surface != pair.want.Name() {
			t.Fatalf("listed=%+v err=%v", listed, err)
		}
	}
	launchers := surface.NewLaunchers([]surface.Surface{adapters[0], adapters[1]})
	for _, launcher := range launchers {
		if launcher.ID() == surface.LauncherACP {
			if agents := launcher.Agents(); len(agents) != 2 {
				t.Fatalf("agents=%v", agents)
			}
			return
		}
	}
	t.Fatal("no acp launcher")
}

func TestAdapterInterruptsATurn(t *testing.T) {
	home := startHost(t, fakeAgent(t, "fake"))
	adapter := New(home, fakeAgent(t, "fake"))
	session := start(t, adapter, "slow")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if observation, err := adapter.Observe(context.Background(), session); err == nil && observation.Status == surface.StatusBusy {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := adapter.Control(context.Background(), session, surface.ControlRequest{Action: "interrupt"}); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, adapter, session)
	read, err := adapter.ReadSession(context.Background(), session, surface.SessionReadRequest{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	last := read.Items[len(read.Items)-1]
	if last.Kind != "done" || last.Status != "interrupted" {
		t.Fatalf("last=%+v", last)
	}
}

func TestASecondAgentNeedsOnlyAConfigEntry(t *testing.T) {
	home := scratchHome(t)
	argv, env := acptest.Command(t.TempDir())
	config, err := json.Marshal(map[string]any{"acpAgents": []map[string]any{{"id": "helper", "label": "Helper", "command": argv, "env": env, "modes": map[string]string{"default": "edits"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(surface.AgenthailConfigPath(home), config, 0o600); err != nil {
		t.Fatal(err)
	}
	agents, problems := acphost.LoadAgents(home)
	if len(problems) != 0 || len(agents) != 2 || agents[0].ID != "devin" || agents[1].ID != "helper" {
		t.Fatalf("agents=%+v problems=%v", agents, problems)
	}
	serveHost(t, home, agents...)
	adapter := New(home, agents[1])
	if adapter.Name() != "helper" || adapter.Label() != "Helper" {
		t.Fatalf("name=%s label=%s", adapter.Name(), adapter.Label())
	}
	session := start(t, adapter, "reply:from-config")
	waitIdle(t, adapter, session)
	if reply, err := adapter.Reply(context.Background(), session, 1); err != nil || reply.Text != "from-config" {
		t.Fatalf("reply=%+v err=%v", reply, err)
	}
}

func TestAdapterControlsModelAndModeAndListsModels(t *testing.T) {
	home := startHost(t, fakeAgent(t, "fake"))
	adapter := New(home, fakeAgent(t, "fake"))
	session := start(t, adapter, "reply:ready")
	waitIdle(t, adapter, session)
	result, err := adapter.Control(context.Background(), session, surface.ControlRequest{Action: "model", Value: "large"})
	if err != nil || result.Output != "large" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := adapter.Control(context.Background(), session, surface.ControlRequest{Action: "permission-mode", Value: "ask"}); err != nil {
		t.Fatal(err)
	}
	if state, err := adapter.Requests(context.Background(), session); err != nil || state.PermissionMode != "ask" {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if _, err := adapter.Control(context.Background(), session, surface.ControlRequest{Action: "compact"}); err != surface.ErrUnsupported {
		t.Fatalf("an action the agent has no way to do must be unsupported: %v", err)
	}
	models, err := adapter.Models(context.Background())
	if err != nil || len(models) != 2 || models[0].ID != "small" || models[1].DisplayName != "Large" {
		t.Fatalf("models=%+v err=%v", models, err)
	}
}
