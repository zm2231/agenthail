package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
	"github.com/zm2231/agenthail/internal/voice"
)

type toolAnswer struct {
	success bool
	text    string
}

type toolVoice struct {
	mu      sync.Mutex
	polled  bool
	pending []voice.Batch
	answers map[string]toolAnswer
	asked   int
	asking  chan struct{}
}

func (c *toolVoice) Create(_ context.Context, cwd, _ string) (*surface.Session, error) {
	return &surface.Session{ID: "operator", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "Voice operator", Cwd: cwd, Transport: "desktop"}, nil
}

func (c *toolVoice) Cursor(context.Context, *surface.Session) (voice.Cursor, error) {
	return voice.Cursor{Source: "fixture", Position: 10}, nil
}

func (c *toolVoice) Poll(ctx context.Context, after voice.Cursor, _ string, wait time.Duration) (voice.Batch, error) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		c.mu.Lock()
		if !c.polled {
			c.polled = true
			c.mu.Unlock()
			return voice.Batch{Cursor: voice.Cursor{Source: "fixture", Position: 20}, Events: []voice.Event{
				{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-a"}},
				{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 answer"}},
			}}, nil
		}
		if len(c.pending) > 0 {
			batch := c.pending[0]
			c.pending = c.pending[1:]
			c.mu.Unlock()
			return batch, nil
		}
		if c.asking == nil {
			c.asking = make(chan struct{})
		}
		asking := c.asking
		c.mu.Unlock()
		select {
		case <-asking:
		case <-timer.C:
			return voice.Batch{Cursor: after}, nil
		case <-ctx.Done():
			return voice.Batch{Cursor: after}, ctx.Err()
		}
	}
}

func (c *toolVoice) Request(context.Context, *surface.Session, string, map[string]any) error {
	return nil
}

func (c *toolVoice) Interrupt(context.Context, *surface.Session) error { return nil }
func (c *toolVoice) Release(string, voice.Cursor)                      {}

func (c *toolVoice) RespondDynamicToolCall(_ context.Context, requestID, _ string, success bool, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.answers[requestID] = toolAnswer{success, text}
	return nil
}

func (c *toolVoice) ask(tool string, arguments map[string]any) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked++
	id := fmt.Sprintf("tool-%d", c.asked)
	c.pending = append(c.pending, voice.Batch{Cursor: voice.Cursor{Source: "fixture", Position: 30}, Events: []voice.Event{
		{Method: "item/tool/call", Params: map[string]any{"requestId": id, "threadId": "operator", "namespace": "agenthail", "tool": tool, "arguments": arguments}},
	}})
	if c.asking != nil {
		close(c.asking)
		c.asking = nil
	}
	return id
}

func (c *toolVoice) call(t *testing.T, tool string, arguments map[string]any) toolAnswer {
	t.Helper()
	return c.await(t, tool, c.ask(tool, arguments))
}

func (c *toolVoice) await(t *testing.T, tool, id string) toolAnswer {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		c.mu.Lock()
		answer, ok := c.answers[id]
		c.mu.Unlock()
		if ok {
			return answer
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s was not answered", tool)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func connectToolVoice(t *testing.T, node *federationNode) (*voice.Service, *toolVoice) {
	t.Helper()
	call := &toolVoice{answers: map[string]toolAnswer{}}
	node.fake.mu.Lock()
	node.fake.sessions["operator"] = surface.Session{ID: "operator", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "Voice operator", Transport: "desktop", Status: surface.StatusIdle}
	node.fake.mu.Unlock()
	service := voice.NewWithTargetsAndOperatorSourceAndStream(filepath.Join(t.TempDir(), "voice", "operator.json"), call, node.daemon.registerVoiceOperator, node.daemon.resolveVoiceTarget, node.daemon.dispatcher(), nil, daemonVoiceStreamProvider{daemon: node.daemon})
	service.SetLeads(voiceLeads{d: node.daemon})
	t.Cleanup(service.Close)
	for _, action := range []voice.Action{{Action: "prepare"}, {Action: "start", AttemptID: "call-a", SDP: "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\n"}} {
		if _, err := service.Apply(context.Background(), "phone", action); err != nil {
			t.Fatalf("%s: %v", action.Action, err)
		}
	}
	testkit.Eventually(t, "the call negotiated", 10*time.Second, func() bool { return service.View("phone").SDP != "" })
	if _, err := service.Apply(context.Background(), "phone", voice.Action{Action: "connected", AttemptID: "call-a"}); err != nil {
		t.Fatal(err)
	}
	return service, call
}

func setLead(t *testing.T, node *federationNode, project, root, name, sessionID string, change registry.CoordinatorChange) {
	t.Helper()
	change.SessionID, change.ChangedBy = sessionID, "fixture"
	if _, err := node.reg.SetCoordinator(registry.CoordinatorSpec{ProjectID: project, ProjectRoot: root, Name: name, Surface: "codex", Cwd: root}, change); err != nil {
		t.Fatal(err)
	}
}

func sendToProject(t *testing.T, operator *mcp.ClientSession, project, message, key string) (coordinatorSendReply, string, bool) {
	t.Helper()
	out, refused := callMCP(t, operator, "agenthail_send", map[string]any{"project": project, "message": message, "clientRequestId": key})
	if refused {
		text, _ := out["message"].(string)
		return coordinatorSendReply{}, text, false
	}
	var reply coordinatorSendReply
	if err := json.Unmarshal([]byte(mustJSONString(t, out)), &reply); err != nil {
		t.Fatalf("send to %s: %v in %v", project, err, out)
	}
	return reply, "", true
}

func TestTheOperatorReachesProjectLeadsOnBothHostsByProjectName(t *testing.T) {
	local, remote, _ := pairedFederation(t)
	now := time.Now()
	widget := widgetRepo(t, "https://github.com/example/widget.git")
	moveSession(t, local, "lead", widget, now)
	setLead(t, local, "repo:github.com/example/widget", widget, "widget-lead", "lead", registry.CoordinatorChange{})
	fresh := widgetRepo(t, "https://github.com/example/fresh.git")
	moveSession(t, local, "fresh-a", fresh, now)
	setLead(t, local, "repo:github.com/example/fresh", fresh, "fresh-lead", "", registry.CoordinatorChange{})
	moveSession(t, local, "bare-a", widgetRepo(t, "https://github.com/example/bare.git"), now)
	moveSession(t, local, "tool-a", widgetRepo(t, "https://github.com/alpha/tool.git"), now)
	moveSession(t, local, "tool-b", widgetRepo(t, "https://github.com/beta/tool.git"), now)
	gadget := widgetRepo(t, "https://github.com/example/gadget.git")
	moveSession(t, remote, "remote-a", gadget, now)
	setLead(t, remote, "repo:github.com/example/gadget", gadget, "gadget-lead", "remote-a", registry.CoordinatorChange{})
	remoteSprocket := widgetRepo(t, "https://github.com/example/sprocket.git")
	moveSession(t, remote, "remote-b", remoteSprocket, now)
	setLead(t, remote, "repo:github.com/example/sprocket", remoteSprocket, "vps-sprocket", "remote-b", registry.CoordinatorChange{})
	remote.daemon.discoverCatalog(context.Background())
	testkit.Eventually(t, "the paired host's coordinators mirrored", 10*time.Second, func() bool {
		mirrored, _ := local.reg.ProjectCoordinators("repo:github.com/example/sprocket")
		gadgets, _ := local.reg.ProjectCoordinators("repo:github.com/example/gadget")
		return len(mirrored) == 1 && len(gadgets) == 1
	})
	sprocket := widgetRepo(t, "https://github.com/example/sprocket.git")
	moveSession(t, local, "sprocket-a", sprocket, now)
	setLead(t, local, "repo:github.com/example/sprocket", sprocket, "laptop-sprocket", "sprocket-a", registry.CoordinatorChange{Replace: true, OverPeers: true})
	local.daemon.discoverCatalog(context.Background())
	service, call := connectToolVoice(t, local)
	operator := asOperator(t, local.daemon, mcpServer(t, local.daemon), "operator")

	listed, refused := callMCP(t, operator, "agenthail_projects", nil)
	var answer struct{ Projects []projectEntry }
	if refused || json.Unmarshal([]byte(mustJSONString(t, listed)), &answer) != nil {
		t.Fatalf("agenthail_projects: %+v", listed)
	}
	byName := map[string][]projectEntry{}
	for _, project := range answer.Projects {
		byName[project.Name] = append(byName[project.Name], project)
	}
	here := local.daemon.hostIdentity().Name
	if got := byName["widget"]; len(got) != 1 || got[0].ID != "repo:github.com/example/widget" || got[0].Conflict || len(got[0].Coordinators) != 1 || got[0].Coordinators[0] != (leadEntry{Ref: "@widget-lead", Host: here, SessionID: "lead"}) {
		t.Fatalf("widget=%+v", got)
	}
	if got := byName["gadget"]; len(got) != 1 || len(got[0].Coordinators) != 1 || got[0].Coordinators[0].Ref != "vps::@gadget-lead" || got[0].Coordinators[0].Host != "vps" {
		t.Fatalf("gadget=%+v", got)
	}
	if got := byName["sprocket"]; len(got) != 1 || !got[0].Conflict || len(got[0].Coordinators) != 2 {
		t.Fatalf("sprocket=%+v", got)
	}
	if got := byName["bare"]; len(got) != 1 || len(got[0].Coordinators) != 0 {
		t.Fatalf("bare=%+v", got)
	}
	if got := byName["tool"]; len(got) != 2 {
		t.Fatalf("tool=%+v", got)
	}

	localSent, remoteSent, started := len(local.fake.delivered()), len(remote.fake.delivered()), len(local.fake.startOptions)
	for index, refusal := range []struct{ project, reason string }{
		{"tool", `"tool" names several projects: `},
		{"bare", "bare has no coordinator"},
		{"sprocket", "sprocket has coordinators "},
		{"nothing", `no project is named "nothing"`},
	} {
		if _, text, ok := sendToProject(t, operator, refusal.project, "standup", fmt.Sprintf("refused-%d", index)); ok || !strings.Contains(text, refusal.reason) {
			t.Fatalf("send to %s: ok=%v %q", refusal.project, ok, text)
		}
	}
	if _, text, _ := sendToProject(t, operator, "sprocket", "standup", "conflict"); !strings.Contains(text, "@laptop-sprocket") || !strings.Contains(text, "vps::@vps-sprocket") {
		t.Fatalf("the conflict did not name both coordinators: %q", text)
	}
	for _, refused := range []struct {
		arguments map[string]any
		reason    string
	}{
		{map[string]any{"project": "sprocket"}, "sprocket has coordinators "},
		{map[string]any{"project": "tool"}, `"tool" names several projects: `},
		{map[string]any{"project": "gadget"}, "@gadget-lead runs on host vps"},
		{map[string]any{"project": "fresh"}, "@fresh-lead has no conversation yet"},
		{map[string]any{"project": "widget", "targetId": "lead"}, "give exactly one of targetId or project"},
	} {
		if got := call.call(t, "voice_transfer", refused.arguments); got.success || !strings.Contains(got.text, refused.reason) {
			t.Fatalf("voice_transfer %v: %+v", refused.arguments, got)
		}
	}
	if service.View("phone").Target != nil || len(local.fake.delivered()) != localSent || len(remote.fake.delivered()) != remoteSent || len(local.fake.startOptions) != started {
		t.Fatalf("a refused call acted: target=%+v local=%q remote=%q", service.View("phone").Target, local.fake.delivered(), remote.fake.delivered())
	}

	if transferred := call.call(t, "voice_transfer", map[string]any{"project": "widget"}); !transferred.success {
		t.Fatalf("voice_transfer widget: %+v", transferred)
	}
	testkit.Eventually(t, "the call moved to the widget lead", 10*time.Second, func() bool {
		target := service.View("phone").Target
		return target != nil && target.ID == "lead"
	})

	if reply, text, ok := sendToProject(t, operator, "widget", "ship the widget", "widget"); !ok || reply.Ref != "@widget-lead" || reply.Started || reply.Result == nil {
		t.Fatalf("send to widget: %+v %q", reply, text)
	}
	delivered := local.fake.delivered()
	if len(delivered) != localSent+1 || !strings.HasSuffix(delivered[len(delivered)-1], "ship the widget") || local.fake.senders()[len(local.fake.senders())-1] != "operator" {
		t.Fatalf("local delivered=%q senders=%q", delivered, local.fake.senders())
	}
	if reply, text, ok := sendToProject(t, operator, "fresh", "begin", "fresh"); !ok || reply.Ref != "@fresh-lead" || !reply.Started || len(local.fake.startOptions) != started+1 || !strings.HasSuffix(local.fake.startOptions[started].Message, "begin") {
		t.Fatalf("send to an unstarted coordinator: %+v %q starts=%+v", reply, text, local.fake.startOptions)
	}
	if reply, text, ok := sendToProject(t, operator, "#gadget", "ship the gadget", "gadget"); !ok || reply.Ref != "vps::@gadget-lead" || reply.Result == nil {
		t.Fatalf("send to the paired host's lead: %+v %q", reply, text)
	}
	testkit.Eventually(t, "the paired host's lead received the message", 10*time.Second, func() bool {
		delivered := remote.fake.delivered()
		return len(delivered) == remoteSent+1 && strings.HasSuffix(delivered[len(delivered)-1], "ship the gadget")
	})

	hold := &snapshotHold{release: make(chan struct{})}
	remote.gate.actions.Store(hold)
	var once sync.Once
	release := func() {
		once.Do(func() {
			remote.gate.actions.Store(nil)
			close(hold.release)
		})
	}
	t.Cleanup(release)
	type heldSend struct {
		reply coordinatorSendReply
		text  string
		ok    bool
	}
	held := make(chan heldSend, 1)
	go func() {
		reply, text, ok := sendToProject(t, operator, "gadget", "while held", "held")
		held <- heldSend{reply, text, ok}
	}()
	testkit.Eventually(t, "the relay to the paired host is held", 10*time.Second, func() bool { return hold.waiting.Load() == 1 })
	if listed, refused := callMCP(t, operator, "agenthail_projects", nil); refused {
		t.Fatalf("agenthail_projects while a relay is held: %+v", listed)
	}
	if _, err := service.Apply(context.Background(), "phone", voice.Action{Action: "heartbeat", AttemptID: "call-a"}); err != nil {
		t.Fatal(err)
	}
	release()
	if answer := <-held; !answer.ok || answer.reply.Ref != "vps::@gadget-lead" {
		t.Fatalf("the held send: %+v", answer)
	}
}
