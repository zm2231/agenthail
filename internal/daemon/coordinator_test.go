package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

const (
	goneCoordinator  = "0d1e2f3a-4b5c-4d6e-8f70-81a2b3c4d5e6"
	freshCoordinator = "9a8b7c6d-5e4f-4a3b-9c2d-1e0f9a8b7c6d"
)

type coordinatorFixture struct {
	claude  *claudetest.Fixture
	daemon  *Daemon
	r       *registry.Registry
	handler http.Handler
	token   string
}

// newCoordinatorFixture sets a coordinator whose conversation Claude no
// longer has: no transcript, no process and no background job.
func newCoordinatorFixture(t *testing.T) *coordinatorFixture {
	t.Helper()
	f := &coordinatorFixture{claude: claudetest.New(t)}
	f.claude.StartAs(freshCoordinator)
	f.claude.ResumeBridge("session_fresh")
	r, err := registry.Open(t.TempDir() + "/registry.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	f.r = r
	gone := surface.Session{ID: goneCoordinator, Bridge: "session_gone", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: orphanPID, Status: surface.StatusIdle, Cwd: f.claude.Home}
	if err := r.RegisterSession(gone); err != nil {
		t.Fatal(err)
	}
	if err := r.SetCoordinator(registry.CoordinatorSpec{Name: "lead", Surface: string(surface.KindClaude), Cwd: f.claude.Home, Instructions: "You coordinate fixture workers."}, goneCoordinator); err != nil {
		t.Fatal(err)
	}
	posts := &orphanPosts{}
	f.daemon = New(r, []surface.Surface{surfaces.NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, f.claude.Home, posts.request)})
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
	if f.claude.Starts() != 1 {
		t.Fatalf("starts=%d", f.claude.Starts())
	}
	started := 0
	for _, detail := range details {
		if strings.Contains(detail, "Started a new conversation for coordinator @lead") {
			started++
		}
	}
	if started != 1 || f.r.QueueCount(freshCoordinator) != 2 {
		t.Fatalf("details=%q queued=%d", details, f.r.QueueCount(freshCoordinator))
	}
	if sid, err := f.r.LookupAlias("lead"); err != nil || sid != freshCoordinator {
		t.Fatalf("alias=%q err=%v", sid, err)
	}
	if prompt := f.claude.Prompt(freshCoordinator); !strings.HasPrefix(prompt, "You coordinate fixture workers.") {
		t.Fatalf("prompt=%q", prompt)
	}
}

func TestQueuedMessageForAGoneCoordinatorStartsItInsteadOfResumingBlank(t *testing.T) {
	f := newCoordinatorFixture(t)
	if err := f.r.QueueMessage(goneCoordinator, "held for the coordinator"); err != nil {
		t.Fatal(err)
	}
	f.daemon.scanAndRelay(context.Background())
	if f.claude.Starts() != 1 || f.claude.Invocations("--bg", "--resume", goneCoordinator) != 0 {
		t.Fatalf("starts=%d resumes=%d", f.claude.Starts(), f.claude.Invocations("--bg", "--resume", goneCoordinator))
	}
	if prompt := f.claude.Prompt(freshCoordinator); !strings.HasSuffix(prompt, "held for the coordinator") {
		t.Fatalf("prompt=%q", prompt)
	}
	if f.r.QueueCount(goneCoordinator) != 0 || f.r.QueueCount(freshCoordinator) != 0 {
		t.Fatalf("queue gone=%d fresh=%d", f.r.QueueCount(goneCoordinator), f.r.QueueCount(freshCoordinator))
	}
}

func TestAPISendToACoordinatorClaudeCannotReportOnWaitsInTheQueue(t *testing.T) {
	f := newCoordinatorFixture(t)
	f.claude.AgentsUnreadable()
	code, body := f.send(t, "lead-unreadable", goneCoordinator, "keep this")
	if code != http.StatusOK || receiptField(body, "evidence") != string(surface.EvidenceQueued) || f.r.QueueCount(goneCoordinator) != 1 {
		t.Fatalf("status=%d body=%v queued=%d", code, body, f.r.QueueCount(goneCoordinator))
	}
	if f.claude.Starts() != 0 || f.claude.Invocations("--bg", "--resume", goneCoordinator) != 0 {
		t.Fatalf("starts=%d resumes=%d", f.claude.Starts(), f.claude.Invocations("--bg", "--resume", goneCoordinator))
	}
}
