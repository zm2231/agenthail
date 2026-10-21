package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

const goneCoordinator = "0d1e2f3a-4b5c-4d6e-8f70-81a2b3c4d5e6"

type coordinatorFixture struct {
	claude  *claudetest.Fixture
	host    *claudehosttest.Fixture
	daemon  *Daemon
	r       *registry.Registry
	handler http.Handler
	token   string
}

// newCoordinatorFixture sets a coordinator whose conversation Claude no
// longer has: no transcript, no process and no background job. A running
// Claude host starts its replacement.
func newCoordinatorFixture(t *testing.T) *coordinatorFixture {
	t.Helper()
	f := &coordinatorFixture{claude: claudetest.New(t)}
	f.host = claudehosttest.New(t, f.claude.Home)
	f.host.Start(t)
	r, err := registry.Open(t.TempDir() + "/registry.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	f.r = r
	gone := surface.Session{ID: goneCoordinator, Bridge: "session_gone", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: orphanPID, Status: surface.StatusIdle, Cwd: f.host.Cwd}
	if err := r.RegisterSession(gone); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetCoordinator(registry.CoordinatorSpec{ProjectID: "project-fixture", ProjectRoot: f.host.Cwd, Name: "fixture-lead", Surface: string(surface.KindClaude), Cwd: f.host.Cwd, Instructions: "You coordinate fixture workers."}, registry.CoordinatorChange{SessionID: goneCoordinator}); err != nil {
		t.Fatal(err)
	}
	posts := &orphanPosts{}
	f.daemon = newTestDaemon(t, r, []surface.Surface{surfaces.NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, f.claude.Home, posts.request)})
	f.handler = dashboardRouter(f.daemon)
	f.token = pairedControlToken(t, r)
	return f
}

func (f *coordinatorFixture) send(t *testing.T, key, sessionID, message string) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"action": "send", "sessionId": sessionID, "message": message, "author": "person"})
	response := serveV1Action(f.handler, string(body), key, f.token)
	var decoded map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Errorf("status=%d body=%s", response.Code, response.Body.String())
	}
	return response.Code, decoded
}

// current is the conversation @fixture-lead names.
func (f *coordinatorFixture) current(t *testing.T) string {
	t.Helper()
	sid, err := f.r.LookupAlias("fixture-lead")
	if err != nil {
		t.Fatal(err)
	}
	return sid
}

// started counts the conversations the Claude host started for Agenthail.
func (f *coordinatorFixture) started(t *testing.T) int {
	t.Helper()
	sessions, err := f.r.ListSessions(1000)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, session := range sessions {
		if session.RuntimeKind == surface.RuntimeClaudeHost {
			count++
		}
	}
	return count
}

// resumedGone reports whether anything reopened the gone conversation.
func (f *coordinatorFixture) resumedGone(t *testing.T) bool {
	t.Helper()
	return len(f.host.Spawns(t, goneCoordinator)) != 0
}

// landed waits until each text reached conversation, as a turn it ran or a
// message queued for it, and returns its turns.
func (f *coordinatorFixture) landed(t *testing.T, conversation string, texts ...string) []claudehosttest.Turn {
	t.Helper()
	var turns []claudehosttest.Turn
	var queue []registry.QueueRow
	reached := func() bool {
		f.daemon.scanAndRelay(context.Background())
		turns = f.host.Turns(t, conversation)
		queue, _ = f.r.ListQueue(false, nil)
		for _, text := range texts {
			found := false
			for _, turn := range turns {
				found = found || strings.Contains(turn.Text, text)
			}
			for _, row := range queue {
				found = found || (row.SessionID == conversation && row.Message == text)
			}
			if !found {
				return false
			}
		}
		return true
	}
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
		if reached() {
			return turns
		}
	}
	t.Fatalf("%q did not all reach %s: turns=%+v queue=%+v", texts, conversation, turns, queue)
	return nil
}

func TestAPISendsToAGoneCoordinatorStartOneConversation(t *testing.T) {
	f := newCoordinatorFixture(t)
	var wg sync.WaitGroup
	details := make([]string, 3)
	for i := range details {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			code, body := f.send(t, fmt.Sprintf("lead-%d", i), goneCoordinator, fmt.Sprintf("task %d", i))
			if code != http.StatusOK {
				t.Errorf("send %d status=%d body=%v", i, code, body)
			}
			details[i] = receiptField(body, "detail")
		}(i)
	}
	wg.Wait()
	started := 0
	for _, detail := range details {
		if strings.Contains(detail, "Started a new conversation for coordinator @fixture-lead") {
			started++
		}
	}
	current := f.current(t)
	if started != 1 || f.started(t) != 1 || current == goneCoordinator || f.resumedGone(t) {
		t.Fatalf("details=%q started=%d current=%s resumed gone=%v", details, f.started(t), current, f.resumedGone(t))
	}
	turns := f.landed(t, current, "task 0", "task 1", "task 2")
	if !strings.HasPrefix(turns[0].Text, "You coordinate fixture workers.") {
		t.Fatalf("first turn=%q", turns[0].Text)
	}
}

func TestQueuedMessageForAGoneCoordinatorStartsItInsteadOfResumingBlank(t *testing.T) {
	f := newCoordinatorFixture(t)
	if err := f.r.QueueMessage(goneCoordinator, "held for the coordinator"); err != nil {
		t.Fatal(err)
	}
	f.daemon.scanAndRelay(context.Background())
	current := f.current(t)
	if current == goneCoordinator || f.started(t) != 1 || f.resumedGone(t) {
		t.Fatalf("current=%s started=%d resumed gone=%v", current, f.started(t), f.resumedGone(t))
	}
	turns := f.landed(t, current, "held for the coordinator")
	if len(turns) != 1 || !strings.HasSuffix(turns[0].Text, "held for the coordinator") {
		t.Fatalf("turns=%+v", turns)
	}
	if f.r.QueueCount(goneCoordinator) != 0 || f.r.QueueCount(current) != 0 {
		t.Fatalf("queue gone=%d current=%d", f.r.QueueCount(goneCoordinator), f.r.QueueCount(current))
	}
}

func TestAPISendToACoordinatorClaudeCannotReportOnWaitsInTheQueue(t *testing.T) {
	f := newCoordinatorFixture(t)
	f.claude.AgentsUnreadable()
	code, body := f.send(t, "lead-unreadable", goneCoordinator, "keep this")
	if code != http.StatusOK || receiptField(body, "evidence") != string(surface.EvidenceQueued) || f.r.QueueCount(goneCoordinator) != 1 {
		t.Fatalf("status=%d body=%v queued=%d", code, body, f.r.QueueCount(goneCoordinator))
	}
	if f.started(t) != 0 || f.resumedGone(t) {
		t.Fatalf("started=%d resumed gone=%v", f.started(t), f.resumedGone(t))
	}
}

func TestSendsToAConversationARestartReplacedReachTheCoordinator(t *testing.T) {
	f := newCoordinatorFixture(t)
	if err := f.r.RegisterSession(surface.Session{ID: "fixture-sender", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	_, pending, err := f.r.QueueDeliveryWithIntent(goneCoordinator, "queued before the restart", "", surface.SendOptions{SourceSessionID: "fixture-sender"})
	if err != nil {
		t.Fatal(err)
	}
	if code, body := f.send(t, "lead-first", goneCoordinator, "first"); code != http.StatusOK {
		t.Fatalf("status=%d body=%v", code, body)
	}
	if code, body := f.send(t, "lead-reply", goneCoordinator, "reply to an old notification"); code != http.StatusOK {
		t.Fatalf("status=%d body=%v", code, body)
	}
	_, claimed, err := f.r.QueueDeliveryWithIntent(goneCoordinator, "queued by an old client", "", surface.SendOptions{SourceSessionID: "fixture-sender"})
	if err != nil {
		t.Fatal(err)
	}
	current := f.current(t)
	f.landed(t, current, "first", "reply to an old notification", "queued before the restart", "queued by an old client")
	if f.started(t) != 1 || f.resumedGone(t) || f.r.QueueCount(goneCoordinator) != 0 {
		t.Fatalf("started=%d resumed gone=%v queued for the replaced conversation=%d", f.started(t), f.resumedGone(t), f.r.QueueCount(goneCoordinator))
	}
	for _, id := range []int64{pending, claimed} {
		intent, err := f.r.DeliveryIntent(id)
		if err != nil || intent.TargetSessionID != current {
			t.Fatalf("intent %d=%+v err=%v", id, intent, err)
		}
	}
}

func TestFilesAttachedToAMessageThatStartsTheCoordinatorAreNamedInItsFirstTurn(t *testing.T) {
	f := newCoordinatorFixture(t)
	attached := surface.Uploads{{ID: "fixture-upload", Name: "plan.pdf", Path: "/fixture/uploads/plan.pdf", MediaType: "application/pdf", Bytes: 1}}
	if _, err := f.r.QueueMessageWithOptions(goneCoordinator, "read the attached plan", "", surface.SendOptions{Uploads: attached}); err != nil {
		t.Fatal(err)
	}
	f.daemon.scanAndRelay(context.Background())
	turns := f.landed(t, f.current(t), "read the attached plan")
	if !strings.Contains(turns[0].Text, "Attached file: /fixture/uploads/plan.pdf") {
		t.Fatalf("first turn=%q", turns[0].Text)
	}
}

func (f *coordinatorFixture) channelSend(t *testing.T, key, channel, source, message string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"action": "channel-send", "channel": channel, "sourceSessionId": source, "message": message})
	response := serveV1Action(f.handler, string(body), key, f.token)
	return response.Code, response.Body.String()
}

func TestAChannelMessageForAGoneCoordinatorStartsItInsteadOfResumingBlank(t *testing.T) {
	f := newCoordinatorFixture(t)
	if _, err := f.r.CreateChannel("crew"); err != nil {
		t.Fatal(err)
	}
	if err := f.r.AddToChannel("crew", goneCoordinator); err != nil {
		t.Fatal(err)
	}
	if code, body := f.channelSend(t, "crew-standup", "crew", "", "standup"); code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	current := f.current(t)
	if current == goneCoordinator || f.started(t) != 1 || f.resumedGone(t) {
		t.Fatalf("current=%s started=%d resumed gone=%v", current, f.started(t), f.resumedGone(t))
	}
	f.landed(t, current, "standup")
}

func TestAChannelMessageFromAConversationARestartReplacedSkipsTheCoordinator(t *testing.T) {
	f := newCoordinatorFixture(t)
	if _, err := f.r.CreateChannel("crew"); err != nil {
		t.Fatal(err)
	}
	if err := f.r.AddToChannel("crew", goneCoordinator); err != nil {
		t.Fatal(err)
	}
	if code, body := f.send(t, "lead-first", goneCoordinator, "first"); code != http.StatusOK {
		t.Fatalf("status=%d body=%v", code, body)
	}
	current := f.current(t)
	f.landed(t, current, "first")
	code, body := f.channelSend(t, "crew-echo", "crew", goneCoordinator, "from the old composer")
	if code != http.StatusBadRequest || !strings.Contains(body, "no members other than the sender") {
		t.Fatalf("status=%d body=%s", code, body)
	}
	for _, turn := range f.host.Turns(t, current) {
		if strings.Contains(turn.Text, "from the old composer") {
			t.Fatalf("the coordinator received its own channel message: %+v", turn)
		}
	}
}
