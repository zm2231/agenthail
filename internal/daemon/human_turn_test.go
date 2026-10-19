package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/zm2231/agenthail/internal/signin"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

const (
	idleClaudeTranscript = `{"type":"user","uuid":"u1","timestamp":"2026-07-19T01:00:00Z","message":{"content":"previous"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-07-19T01:00:01Z","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"answer"}]}}
{"type":"system","subtype":"turn_duration","timestamp":"2026-07-19T01:00:01Z"}
`
	busyClaudeTranscript = `{"type":"user","uuid":"u2","timestamp":"2026-07-19T01:00:02Z","message":{"content":"running"}}
`
)

type nativeRecordingClaude struct {
	*surfaces.Claude
	native []string
}

func (c *nativeRecordingClaude) Send(_ context.Context, _ *surface.Session, message string) (*surface.SendResult, error) {
	c.native = append(c.native, message)
	return &surface.SendResult{UUID: "peer", Accepted: true}, nil
}

type humanTurnFixture struct {
	d       *Daemon
	r       *registry.Registry
	claude  *nativeRecordingClaude
	posts   []string
	postErr error
	bridged surface.Session
	native  surface.Session
}

func newHumanTurnFixture(t *testing.T) *humanTurnFixture {
	t.Helper()
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	f := &humanTurnFixture{r: r}
	process := claudetest.New(t)
	process.Listen()
	process.Record("conversation-bridge", process.Home, "session_bridge")
	f.claude = &nativeRecordingClaude{Claude: surfaces.NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, process.Home, func(_ context.Context, method, url string, _ map[string]string, body string, _ surfaces.CookieSource, _ time.Duration) (int, string, error) {
		if method != http.MethodPost {
			t.Fatalf("method=%s", method)
		}
		f.posts = append(f.posts, url+" "+body)
		if f.postErr != nil {
			return 0, "", f.postErr
		}
		return http.StatusOK, `{}`, nil
	})}
	dir := t.TempDir()
	f.bridged = surface.Session{ID: "conversation-bridge", Bridge: "session_bridge", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: process.PID, Transport: "uds", Status: surface.StatusIdle, HasLocal: true, Transcript: filepath.Join(dir, "bridged.jsonl")}
	f.native = surface.Session{ID: "native-only", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: os.Getpid(), Transport: "uds", Status: surface.StatusIdle, HasLocal: true, Transcript: filepath.Join(dir, "native.jsonl")}
	agent := surface.Session{ID: "agent", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transport: "managed", Status: surface.StatusIdle}
	for _, session := range []surface.Session{f.bridged, f.native, agent} {
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	f.transcript(t, f.bridged, idleClaudeTranscript)
	f.transcript(t, f.native, idleClaudeTranscript)
	f.d = New(r, []surface.Surface{f.claude})
	return f
}

func (f *humanTurnFixture) transcript(t *testing.T, session surface.Session, content string) {
	t.Helper()
	if err := os.WriteFile(session.Transcript, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func (f *humanTurnFixture) send(t *testing.T, body string) map[string]any {
	t.Helper()
	response := serveDashboardRequest(dashboardRouter(f.d), http.MethodPost, "/api/action", body)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var receipt struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	return receipt.Result
}

func assertHumanTurnPost(t *testing.T, post, message string) {
	t.Helper()
	url, body, _ := strings.Cut(post, " ")
	if !strings.Contains(url, "/v1/code/sessions/cse_bridge/events") {
		t.Fatalf("url=%q", url)
	}
	var envelope struct {
		Events []struct {
			Payload struct {
				Type    string `json:"type"`
				Message struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"message"`
			} `json:"payload"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Events) != 1 || envelope.Events[0].Payload.Type != "user" || envelope.Events[0].Payload.Message.Role != "user" || envelope.Events[0].Payload.Message.Content != message {
		t.Fatalf("body=%s", body)
	}
}

func TestPersonSendToBridgedClaudeIsHumanTurn(t *testing.T) {
	f := newHumanTurnFixture(t)
	receipt := f.send(t, `{"action":"send","author":"person","sessionId":"conversation-bridge","message":"Please inspect the failure."}`)
	if len(f.claude.native) != 0 || len(f.posts) != 1 {
		t.Fatalf("native=%q posts=%q", f.claude.native, f.posts)
	}
	assertHumanTurnPost(t, f.posts[0], "Please inspect the failure.")
	if receipt["evidence"] != string(surface.EvidenceDelivered) {
		t.Fatalf("receipt=%v", receipt)
	}
}

func TestAgentSendToBridgedClaudeStaysNative(t *testing.T) {
	f := newHumanTurnFixture(t)
	receipt := f.send(t, `{"action":"send","sessionId":"conversation-bridge","sourceSessionId":"agent","message":"peer note"}`)
	if len(f.posts) != 0 || len(f.claude.native) != 1 || f.claude.native[0] != "peer note" {
		t.Fatalf("native=%q posts=%q", f.claude.native, f.posts)
	}
	if receipt["evidence"] != string(surface.EvidenceTransportAccepted) {
		t.Fatalf("receipt=%v", receipt)
	}
}

func TestPersonSendWithoutBridgeUsesNativeTransport(t *testing.T) {
	f := newHumanTurnFixture(t)
	receipt := f.send(t, `{"action":"send","author":"person","sessionId":"native-only","message":"hello"}`)
	if len(f.posts) != 0 || len(f.claude.native) != 1 || f.claude.native[0] != "hello" {
		t.Fatalf("native=%q posts=%q", f.claude.native, f.posts)
	}
	if receipt["evidence"] != string(surface.EvidenceTransportAccepted) {
		t.Fatalf("receipt=%v", receipt)
	}
}

func TestPersonSendToBusyBridgedClaudeQueuesHumanTurn(t *testing.T) {
	f := newHumanTurnFixture(t)
	f.transcript(t, f.bridged, idleClaudeTranscript+busyClaudeTranscript)
	receipt := f.send(t, `{"action":"send","author":"person","sessionId":"conversation-bridge","busyDelivery":"queue","message":"after this turn"}`)
	if receipt["evidence"] != string(surface.EvidenceQueued) || len(f.posts) != 0 || len(f.claude.native) != 0 {
		t.Fatalf("receipt=%v native=%q posts=%q", receipt, f.claude.native, f.posts)
	}
	queueID := int64(receipt["queueId"].(float64))
	f.transcript(t, f.bridged, idleClaudeTranscript)
	f.d.drainMessageQueue(context.Background(), f.claude, &f.bridged)
	if len(f.claude.native) != 0 || len(f.posts) != 1 {
		t.Fatalf("native=%q posts=%q", f.claude.native, f.posts)
	}
	assertHumanTurnPost(t, f.posts[0], "after this turn")
	item, err := f.r.QueueItem(queueID)
	if err != nil || item.Status != "delivered" || item.Evidence != surface.EvidenceDelivered {
		t.Fatalf("item=%+v err=%v", item, err)
	}
}

func TestQueuedAgentMessageToBridgedClaudeStaysNative(t *testing.T) {
	f := newHumanTurnFixture(t)
	if _, err := f.r.QueueMessageWithOptions(f.bridged.ID, "relayed", "", surface.SendOptions{SourceSessionID: "agent"}); err != nil {
		t.Fatal(err)
	}
	f.d.drainMessageQueue(context.Background(), f.claude, &f.bridged)
	if len(f.posts) != 0 || len(f.claude.native) != 1 || f.claude.native[0] != "relayed" {
		t.Fatalf("native=%q posts=%q", f.claude.native, f.posts)
	}
}

func TestNativeAppPersonSendToBridgedClaudeIsHumanTurn(t *testing.T) {
	f := newHumanTurnFixture(t)
	response := serveV1Action(dashboardRouter(f.d), `{"action":"send","author":"person","sessionId":"conversation-bridge","message":"from the app"}`, "app-send", pairedControlToken(t, f.r))
	if response.Code != http.StatusOK || len(f.claude.native) != 0 || len(f.posts) != 1 {
		t.Fatalf("status=%d body=%s native=%q posts=%q", response.Code, response.Body, f.claude.native, f.posts)
	}
	assertHumanTurnPost(t, f.posts[0], "from the app")
}

func TestUncertainHumanTurnPostIsNeverRetried(t *testing.T) {
	f := newHumanTurnFixture(t)
	f.postErr = errors.New("connection reset after write")
	receipt := f.send(t, `{"action":"send","author":"person","sessionId":"conversation-bridge","message":"maybe delivered"}`)
	if receipt["evidence"] != string(surface.EvidenceSubmitted) || len(f.posts) != 1 || len(f.claude.native) != 0 {
		t.Fatalf("receipt=%v native=%q posts=%q", receipt, f.claude.native, f.posts)
	}
	f.d.drainMessageQueue(context.Background(), f.claude, &f.bridged)
	if len(f.posts) != 1 || len(f.claude.native) != 0 {
		t.Fatalf("uncertain human turn was resent: native=%q posts=%q", f.claude.native, f.posts)
	}
}

func TestUncertainQueuedHumanTurnDeadLettersWithoutRetry(t *testing.T) {
	f := newHumanTurnFixture(t)
	f.transcript(t, f.bridged, idleClaudeTranscript+busyClaudeTranscript)
	receipt := f.send(t, `{"action":"send","author":"person","sessionId":"conversation-bridge","busyDelivery":"queue","message":"queued maybe"}`)
	queueID := int64(receipt["queueId"].(float64))
	f.transcript(t, f.bridged, idleClaudeTranscript)
	f.postErr = errors.New("connection reset after write")
	f.d.drainMessageQueue(context.Background(), f.claude, &f.bridged)
	f.d.drainMessageQueue(context.Background(), f.claude, &f.bridged)
	if len(f.posts) != 1 || len(f.claude.native) != 0 {
		t.Fatalf("native=%q posts=%q", f.claude.native, f.posts)
	}
	item, err := f.r.QueueItem(queueID)
	if err != nil || item.Status != "dead" || item.Evidence != surface.EvidenceUnknown {
		t.Fatalf("item=%+v err=%v", item, err)
	}
}

func TestUnmarkedSendToBridgedClaudeStaysNative(t *testing.T) {
	f := newHumanTurnFixture(t)
	receipt := f.send(t, `{"action":"send","sessionId":"conversation-bridge","message":"no author declared"}`)
	if len(f.posts) != 0 || len(f.claude.native) != 1 || receipt["evidence"] != string(surface.EvidenceTransportAccepted) {
		t.Fatalf("receipt=%v native=%q posts=%q", receipt, f.claude.native, f.posts)
	}
	response := serveV1Action(dashboardRouter(f.d), `{"action":"send","sessionId":"conversation-bridge","message":"script"}`, "unmarked", pairedControlToken(t, f.r))
	if response.Code != http.StatusOK || len(f.posts) != 0 || len(f.claude.native) != 2 {
		t.Fatalf("status=%d native=%q posts=%q", response.Code, f.claude.native, f.posts)
	}
}

func TestSendRejectsContradictoryOrUnknownAuthor(t *testing.T) {
	f := newHumanTurnFixture(t)
	for _, body := range []string{
		`{"action":"send","author":"person","sourceSessionId":"agent","sessionId":"conversation-bridge","message":"both"}`,
		`{"action":"send","author":"robot","sessionId":"conversation-bridge","message":"unknown"}`,
	} {
		response := serveDashboardRequest(dashboardRouter(f.d), http.MethodPost, "/api/action", body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, response.Code, response.Body)
		}
	}
	if len(f.posts) != 0 || len(f.claude.native) != 0 {
		t.Fatalf("native=%q posts=%q", f.claude.native, f.posts)
	}
}

func TestPersonSendWithOptionsIsRejectedNotDowngraded(t *testing.T) {
	f := newHumanTurnFixture(t)
	response := serveDashboardRequest(dashboardRouter(f.d), http.MethodPost, "/api/action", `{"action":"send","author":"person","sessionId":"conversation-bridge","model":"claude-haiku-5-5","message":"with model"}`)
	if response.Code == http.StatusOK || len(f.posts) != 0 || len(f.claude.native) != 0 {
		t.Fatalf("status=%d body=%s native=%q posts=%q", response.Code, response.Body, f.claude.native, f.posts)
	}
	if !strings.Contains(response.Body.String(), "not supported for a person's Claude turn") {
		t.Fatalf("body=%s", response.Body)
	}
}

func TestQueuedPersonMessageWithOptionsIsRejectedAtDrain(t *testing.T) {
	f := newHumanTurnFixture(t)
	queueID, err := f.r.QueueMessageWithOptions(f.bridged.ID, "queued with effort", "", surface.SendOptions{Author: surface.AuthorPerson, TurnOptions: surface.TurnOptions{Effort: "high"}})
	if err != nil {
		t.Fatal(err)
	}
	f.d.drainMessageQueue(context.Background(), f.claude, &f.bridged)
	if len(f.posts) != 0 || len(f.claude.native) != 0 {
		t.Fatalf("native=%q posts=%q", f.claude.native, f.posts)
	}
	if item, err := f.r.QueueItem(queueID); err != nil || item.Status != "dead" {
		t.Fatalf("item=%+v err=%v", item, err)
	}
}
