package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
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

func (c *voiceCall) Cursor(context.Context) (voice.Cursor, error) {
	return voice.Cursor{Source: "fixture", Position: 10}, nil
}

func (c *voiceCall) Poll(_ context.Context, after voice.Cursor, _ string) (voice.Batch, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.polled {
		return voice.Batch{Cursor: after}, nil
	}
	c.polled = true
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
	service := voice.NewWithTargetsAndOperatorSourceAndStream(filepath.Join(t.TempDir(), "voice", "operator.json"), call, f.r.RegisterSession, "/fixture/agenthail", f.daemon.resolveVoiceTarget, f.daemon.dispatcher(), nil, daemonVoiceStreamProvider{daemon: f.daemon})
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

// delegateToResumed delegates to the exited session, which resumes in the
// Claude host. A delegation is the operator's message, so it takes the native
// socket and its reply cannot be followed.
func (f *orphanFixture) delegateToResumed(t *testing.T) {
	t.Helper()
	manager := claudetest.ListenPeerManager(t, f.claude.Home)
	service, call := connectVoice(t, f, "orphan")
	state := delegate(t, service, "hello")
	if !strings.Contains(state.Message, "cannot follow this session's reply") || f.resumes(t) != 1 || manager.Count("hello") != 1 || f.posts.count("hello") != 0 || call.said("hello") {
		t.Fatalf("message=%q resumes=%d peer=%d posts=%d", state.Message, f.resumes(t), manager.Count("hello"), f.posts.count("hello"))
	}
}

func TestVoiceDelegationToEndedBackgroundJobResumesItInTheHostAndCannotFollow(t *testing.T) {
	newOrphanFixtureOn(t, "").delegateToResumed(t)
}

func TestVoiceDelegationToEndedSocketSessionResumesItInTheHostAndCannotFollow(t *testing.T) {
	newOrphanFixture(t).delegateToResumed(t)
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
	if !strings.Contains(state.Message, "cannot follow this session's reply") || f.resumes(t) != 0 || manager.Count("hello") != 1 {
		t.Fatalf("message=%q resumes=%d peer=%d", state.Message, f.resumes(t), manager.Count("hello"))
	}
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
