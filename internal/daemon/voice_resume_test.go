package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
	"github.com/zm2231/agenthail/internal/testkit"
	"github.com/zm2231/agenthail/internal/voice"
)

// voiceCall is a realtime voice provider whose call connects on its first
// poll and which records what it is asked to speak.
type voiceCall struct {
	mu     sync.Mutex
	polled bool
	spoken []string
}

func (c *voiceCall) Create(_ context.Context, cwd, _ string) (*surface.Session, error) {
	return &surface.Session{ID: "operator", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "Voice operator", Cwd: cwd, Transport: "desktop"}, nil
}

func (c *voiceCall) Cursor(context.Context, *surface.Session) (voice.Cursor, error) {
	return voice.Cursor{Source: "fixture", Position: 10}, nil
}

func (c *voiceCall) Poll(ctx context.Context, after voice.Cursor, _ string, wait time.Duration) (voice.Batch, error) {
	c.mu.Lock()
	polled := c.polled
	c.polled = true
	c.mu.Unlock()
	if polled {
		select {
		case <-time.After(wait):
			return voice.Batch{Cursor: after}, nil
		case <-ctx.Done():
			return voice.Batch{Cursor: after}, ctx.Err()
		}
	}
	return voice.Batch{Cursor: voice.Cursor{Source: "fixture", Position: 20}, Events: []voice.Event{
		{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-a"}},
		{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 answer"}},
	}}, nil
}

func (c *voiceCall) Request(_ context.Context, _ *surface.Session, method string, params map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if text, ok := params["text"].(string); ok && method == "thread/realtime/appendText" {
		c.spoken = append(c.spoken, text)
	}
	return nil
}

func (c *voiceCall) Interrupt(context.Context, *surface.Session) error { return nil }
func (c *voiceCall) Release(string, voice.Cursor)                      {}

func (c *voiceCall) said(text string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, spoken := range c.spoken {
		if strings.Contains(spoken, text) {
			return true
		}
	}
	return false
}

// connectVoice starts a voice call through the daemon's voice target
// resolution, delivery and session streams, and selects target.
func connectVoice(t *testing.T, f *orphanFixture, target string) (*voice.Service, *voiceCall) {
	t.Helper()
	call := &voiceCall{}
	service := voice.NewWithTargetsAndOperatorSourceAndStream(filepath.Join(t.TempDir(), "voice", "operator.json"), call, f.daemon.registerVoiceOperator, "/fixture/agenthail", f.daemon.resolveVoiceTarget, f.daemon.dispatcher(), nil, daemonVoiceStreamProvider{daemon: f.daemon})
	t.Cleanup(service.Close)
	for _, action := range []voice.Action{{Action: "prepare"}, {Action: "select", TargetID: target}, {Action: "start", AttemptID: "call-a", SDP: "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\n"}} {
		if _, err := service.Apply(context.Background(), "phone", action); err != nil {
			t.Fatalf("%s: %v", action.Action, err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for service.View("phone").SDP == "" {
		if time.Now().After(deadline) {
			t.Fatalf("call did not negotiate: %+v", service.View("phone"))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := service.Apply(context.Background(), "phone", voice.Action{Action: "connected", AttemptID: "call-a"}); err != nil {
		t.Fatal(err)
	}
	return service, call
}

// delegate posts a delegation through the public voice handler, whose request
// context ends when it returns.
func delegate(t *testing.T, service *voice.Service, text string) voice.State {
	t.Helper()
	body, _ := json.Marshal(voice.Action{Action: "delegate", AttemptID: "call-a", MessageID: "voice-" + text, Text: text})
	response := httptest.NewRecorder()
	voiceActionHandler(service, "phone", response, httptest.NewRequest(http.MethodPost, "/api/v1/voice", bytes.NewReader(body)), true)
	var state voice.State
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &state) != nil {
		t.Fatalf("delegate: status=%d body=%s", response.Code, response.Body)
	}
	return state
}

// delegateAndHear delegates to the exited session, which resumes in the Claude
// host. A delegation is the person speaking, so it is their turn there and voice
// speaks the reply it follows.
func (f *orphanFixture) delegateAndHear(t *testing.T) {
	t.Helper()
	manager := claudetest.ListenPeerManager(t, f.claude.Home)
	service, call := connectVoice(t, f, "orphan")
	delegate(t, service, "hello")
	deadline := time.Now().Add(10 * time.Second)
	for !call.said("echo:hello") {
		if time.Now().After(deadline) {
			t.Fatalf("reply was not spoken: spoken=%q message=%q events=%+v", call.spoken, service.View("phone").Message, service.View("phone").Events)
		}
		time.Sleep(20 * time.Millisecond)
	}
	turns := f.host.Turns(t, orphanConversation)
	if f.resumes(t) != 1 || len(turns) != 1 || turnOrigin(t, turns[0]) != "human" || manager.Count("hello") != 0 || !strings.Contains(service.View("phone").Message, "Woke orphan") || failed(service.View("phone")) {
		t.Fatalf("resumes=%d turns=%+v peer=%d message=%q", f.resumes(t), turns, manager.Count("hello"), service.View("phone").Message)
	}
}

func TestVoiceDelegationToEndedBackgroundJobResumesItInTheHostAndSpeaksTheReply(t *testing.T) {
	newOrphanFixtureOn(t, "").delegateAndHear(t)
}

func TestVoiceDelegationToEndedSocketSessionResumesItInTheHostAndSpeaksTheReply(t *testing.T) {
	newOrphanFixture(t).delegateAndHear(t)
}

func TestVoiceDelegationToAHostSessionIsAPersonsTurnAndSpeaksTheReply(t *testing.T) {
	f := newOrphanFixture(t)
	client := f.host.Start(t)
	f.host.Open(t, client, orphanConversation)
	claude := f.daemon.adapters()[0].(*surfaces.Claude)
	hosted := hostedSession(t, claude, orphanConversation)
	hosted.Name = "orphan"
	if err := f.r.RegisterSession(hosted); err != nil {
		t.Fatal(err)
	}
	f.session = hosted
	service, call := connectVoice(t, f, "orphan")
	delegate(t, service, "hello")
	testkit.Eventually(t, "the spoken reply", 15*time.Second, func() bool { return call.said("echo:hello") })
	turns := f.host.Turns(t, orphanConversation)
	if f.resumes(t) != 1 || len(turns) != 1 || turnOrigin(t, turns[0]) != "human" || failed(service.View("phone")) {
		t.Fatalf("resumes=%d turns=%+v message=%q", f.resumes(t), turns, service.View("phone").Message)
	}
}

func TestVoiceDelegationToLiveSocketOnlySessionDeliversInsteadOfRejecting(t *testing.T) {
	f := newOrphanFixture(t)
	f.claude.Listen()
	f.claude.Record(orphanConversation, f.claude.Home, "")
	manager := claudetest.ListenPeerManager(t, f.claude.Home)
	f.session = surface.Session{ID: orphanConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "orphan", PID: f.claude.PID, Status: surface.StatusIdle, Transport: "uds", Cwd: f.claude.Home, Transcript: f.session.Transcript}
	if err := f.r.RegisterSession(f.session); err != nil {
		t.Fatal(err)
	}
	if err := f.r.SetAlias("orphan", f.session.ID); err != nil {
		t.Fatal(err)
	}
	service, _ := connectVoice(t, f, "orphan")
	state := delegate(t, service, "hello")
	if !followedFrame(state, f.session.ID) || f.resumes(t) != 0 || manager.Count("hello") != 1 {
		t.Fatalf("events=%+v resumes=%d peer=%d", state.Events, f.resumes(t), manager.Count("hello"))
	}
}

func followedFrame(state voice.State, sessionID string) bool {
	for _, event := range state.Events {
		if event.Method == "voice/delegation/held" {
			return false
		}
		if turn, _ := event.Params["turnId"].(string); event.Method == "voice/delegation/dispatched" && turn != "" && turn != sessionID {
			return true
		}
	}
	return false
}

func failed(state voice.State) bool {
	for _, event := range state.Events {
		if event.Method == "voice/delegation/failed" {
			return true
		}
	}
	return false
}

func TestVoiceTargetTakesTheLiveSessionForItsExactIDAndOnlyWakingSurfacesTakeAClosedRow(t *testing.T) {
	d, _, fake, _, registered := daemonFixture(t)
	live := registered
	live.Source, live.Transport = "agenthail", "managed"
	fake.sessions[registered.ID] = live
	target, err := d.resolveVoiceTarget(context.Background(), registered.ID)
	if err != nil || target.Session.Transport != "managed" {
		t.Fatalf("target=%+v err=%v", target, err)
	}
	delete(fake.sessions, registered.ID)
	if _, err := d.resolveVoiceTarget(context.Background(), registered.ID); err == nil {
		t.Fatal("a surface that cannot wake a session accepted its closed row")
	}
}

func TestPrepareRegistersASavedOperatorAsItsSurfaceCarriesItNow(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	fake.sessions["operator"] = surface.Session{ID: "operator", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Source: "agenthail", Transport: "managed", Status: surface.StatusIdle}
	path := filepath.Join(t.TempDir(), "voice", "operator.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	saved := `{"state":{"protocol":1,"session":{"id":"operator","surface":"codex","name":"Voice operator","status":"idle","source":"agenthail","transport":"desktop"},"phase":"ended","events":[]}}`
	if err := os.WriteFile(path, []byte(saved), 0600); err != nil {
		t.Fatal(err)
	}
	service := voice.NewWithTargetsAndOperatorSourceAndStream(path, &voiceCall{}, d.registerVoiceOperator, "/fixture/agenthail", d.resolveVoiceTarget, d.dispatcher(), nil, daemonVoiceStreamProvider{daemon: d})
	t.Cleanup(service.Close)
	if _, err := service.Apply(context.Background(), "phone", voice.Action{Action: "prepare"}); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	registered, err := r.Session("operator")
	if err != nil || registered.RuntimeKind != surface.RuntimeCodexAppServer || registered.Transport != "managed" || registered.Name != "Voice operator" {
		t.Fatalf("registered=%+v err=%v", registered, err)
	}
	delete(fake.sessions, "operator")
	if _, err := service.Apply(context.Background(), "phone", voice.Action{Action: "prepare"}); err == nil || !strings.Contains(err.Error(), "verify voice operator") {
		t.Fatalf("prepare registered an operator its surface no longer has: %v", err)
	}
}

func TestVoiceTargetAcceptsTheFromSessionOfAPeerMessage(t *testing.T) {
	d, _, fake, _, registered := daemonFixture(t)
	fake.sessions[registered.ID] = registered
	target, err := d.resolveVoiceTarget(context.Background(), surface.PeerUUID(registered.Surface, registered.ID))
	if err != nil || target.Session.ID != registered.ID {
		t.Fatalf("target=%+v err=%v", target, err)
	}
}

func TestTheVoiceAliasFollowsTheCurrentOperatorButIsNeverTakenFromAnotherSession(t *testing.T) {
	d, r, fake, from, _ := daemonFixture(t)
	operator := func(id string) surface.Session {
		session := surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Source: "agenthail", Transport: "managed", Cwd: "/home/user/.agenthail/voice/operator", Status: surface.StatusIdle}
		fake.sessions[id] = session
		return session
	}
	ctx := context.Background()
	if err := d.registerVoiceOperator(ctx, operator("operator-1")); err != nil {
		t.Fatal(err)
	}
	if owner, _ := r.LookupAlias("voice"); owner != "operator-1" {
		t.Fatalf("@voice=%q", owner)
	}
	if err := d.registerVoiceOperator(ctx, operator("operator-2")); err != nil {
		t.Fatal(err)
	}
	if owner, _ := r.LookupAlias("voice"); owner != "operator-2" {
		t.Fatalf("@voice did not move to the new operator: %q", owner)
	}
	if err := r.RemoveAlias("voice"); err != nil {
		t.Fatal(err)
	}
	if err := r.SetAlias("voice", from.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.registerVoiceOperator(ctx, operator("operator-3")); err != nil {
		t.Fatal(err)
	}
	if owner, _ := r.LookupAlias("voice"); owner != from.ID {
		t.Fatalf("@voice was taken from a session someone named: %q", owner)
	}
}
