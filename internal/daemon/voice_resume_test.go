package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
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

// peerManager answers the Claude peer manager socket under home, accepting
// every send.
type peerManager struct {
	mu       sync.Mutex
	messages []string
}

func listenPeerManager(t *testing.T, home string) *peerManager {
	t.Helper()
	manager := &peerManager{}
	path := filepath.Join(home, ".agenthail", "claude-peers.sock")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			var request struct {
				Message string `json:"message"`
			}
			if json.NewDecoder(conn).Decode(&request) == nil {
				manager.mu.Lock()
				manager.messages = append(manager.messages, request.Message)
				id := fmt.Sprintf("peer-%d", len(manager.messages))
				manager.mu.Unlock()
				_ = json.NewEncoder(conn).Encode(map[string]any{"ok": true, "result": map[string]any{"uuid": id, "accepted": true}})
			}
			_ = conn.Close()
		}
	}()
	return manager
}

func (m *peerManager) count(message string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, sent := range m.messages {
		if sent == message {
			count++
		}
	}
	return count
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

// recordTurns makes each posted message a transcript turn Claude has not
// answered yet.
func (f *orphanFixture) recordTurns(t *testing.T) {
	t.Helper()
	f.posts.onPost = func(body string) {
		var envelope struct {
			Events []struct {
				Payload struct {
					UUID    string `json:"uuid"`
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"payload"`
			} `json:"events"`
		}
		if json.Unmarshal([]byte(body), &envelope) != nil || len(envelope.Events) != 1 {
			return
		}
		f.appendTranscript(t, map[string]any{"type": "user", "uuid": envelope.Events[0].Payload.UUID, "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "message": map[string]any{"role": "user", "content": envelope.Events[0].Payload.Message.Content}})
	}
}

func (f *orphanFixture) appendTranscript(t *testing.T, record map[string]any) {
	t.Helper()
	line, _ := json.Marshal(record)
	file, err := os.OpenFile(f.session.Transcript, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Error(err)
		return
	}
	defer file.Close()
	if _, err := file.Write(append(line, '\n')); err != nil {
		t.Error(err)
	}
}

func (f *orphanFixture) answer(t *testing.T, text string) {
	t.Helper()
	f.appendTranscript(t, map[string]any{"type": "assistant", "uuid": "fixture-reply", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "message": map[string]any{"id": "fixture-reply-message", "role": "assistant", "stop_reason": "end_turn", "content": []map[string]any{{"type": "text", "text": text}}}})
	f.appendTranscript(t, map[string]any{"type": "system", "subtype": "turn_duration", "timestamp": time.Now().UTC().Format(time.RFC3339Nano)})
}

func TestVoiceDelegationToEndedBackgroundJobResumesItAndSpeaksTheReply(t *testing.T) {
	f := newOrphanFixtureOn(t, "")
	f.recordTurns(t)
	service, call := connectVoice(t, f, "orphan")
	delegate(t, service, "hello")
	time.Sleep(200 * time.Millisecond)
	f.answer(t, "fixture reply")
	deadline := time.Now().Add(5 * time.Second)
	for !call.said("fixture reply") {
		if time.Now().After(deadline) {
			window, _ := f.r.SessionJournalAfter(f.session.ID, 0, 100)
			var payloads []string
			for _, entry := range window.Entries {
				payloads = append(payloads, string(entry.Payload))
			}
			t.Fatalf("reply was not spoken: spoken=%q journal=%s events=%+v", call.spoken, strings.Join(payloads, "\n"), service.View("phone").Events)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if f.resumes() != 1 || f.posts.count("hello") != 1 || !strings.Contains(service.View("phone").Message, "Woke orphan") || failed(service.View("phone")) {
		t.Fatalf("resumes=%d posts=%d message=%q", f.resumes(), f.posts.count("hello"), service.View("phone").Message)
	}
}

func TestVoiceDelegationWhoseResumeChangesTransportDeliversAndCannotFollow(t *testing.T) {
	f := newOrphanFixture(t)
	service, call := connectVoice(t, f, "orphan")
	state := delegate(t, service, "hello")
	if !strings.Contains(state.Message, "cannot follow this session's reply") || f.resumes() != 1 || f.posts.count("hello") != 1 || call.said("hello") {
		t.Fatalf("message=%q resumes=%d posts=%d", state.Message, f.resumes(), f.posts.count("hello"))
	}
}

func TestVoiceDelegationWhoseResumeComesBackSocketOnlyDeliversAndCannotFollow(t *testing.T) {
	f := newOrphanFixture(t)
	f.claude.Listen()
	f.claude.ResumeBridge("")
	manager := listenPeerManager(t, f.claude.Home)
	f.session = surface.Session{ID: orphanConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "orphan", PID: orphanPID, Status: surface.StatusIdle, Transport: "uds", Cwd: f.claude.Home, Transcript: f.session.Transcript}
	if err := f.r.RegisterSession(f.session); err != nil {
		t.Fatal(err)
	}
	if err := f.r.SetAlias("orphan", f.session.ID); err != nil {
		t.Fatal(err)
	}
	service, call := connectVoice(t, f, "orphan")
	state := delegate(t, service, "hello")
	if !strings.Contains(state.Message, "cannot follow this session's reply") || f.resumes() != 1 || manager.count("hello") != 1 || f.posts.count("hello") != 0 || call.said("hello") {
		t.Fatalf("message=%q resumes=%d peer=%d posts=%d", state.Message, f.resumes(), manager.count("hello"), f.posts.count("hello"))
	}
}

func TestVoiceDelegationToLiveSocketOnlySessionDeliversInsteadOfRejecting(t *testing.T) {
	f := newOrphanFixture(t)
	f.claude.Listen()
	f.claude.Record(orphanConversation, f.claude.Home, "")
	manager := listenPeerManager(t, f.claude.Home)
	f.session = surface.Session{ID: orphanConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "orphan", PID: f.claude.PID, Status: surface.StatusIdle, Transport: "uds", Cwd: f.claude.Home, Transcript: f.session.Transcript}
	if err := f.r.RegisterSession(f.session); err != nil {
		t.Fatal(err)
	}
	if err := f.r.SetAlias("orphan", f.session.ID); err != nil {
		t.Fatal(err)
	}
	service, _ := connectVoice(t, f, "orphan")
	state := delegate(t, service, "hello")
	if !strings.Contains(state.Message, "cannot follow this session's reply") || f.resumes() != 0 || manager.count("hello") != 1 {
		t.Fatalf("message=%q resumes=%d peer=%d", state.Message, f.resumes(), manager.count("hello"))
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
