package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

const (
	orphanConversation = "fe758937-0caa-4912-823f-927677775b2b"
	orphanPID          = 2147483647
)

type orphanPosts struct {
	mu     sync.Mutex
	bodies []string
	onPost func(body string)
}

func (p *orphanPosts) request(_ context.Context, method, url string, _ map[string]string, body, _ string, _ string, _ time.Duration) (int, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if method == "POST" && strings.Contains(url, "/v1/code/sessions/") {
		p.bodies = append(p.bodies, body)
		if p.onPost != nil {
			p.onPost(body)
		}
	}
	return 200, `{}`, nil
}

func (p *orphanPosts) count(message string) int {
	return p.containing(`"content":"` + message + `"`)
}

func (p *orphanPosts) containing(text string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	count := 0
	for _, body := range p.bodies {
		if strings.Contains(body, text) {
			count++
		}
	}
	return count
}

type orphanFixture struct {
	claude  *claudetest.Fixture
	posts   *orphanPosts
	daemon  *Daemon
	r       *registry.Registry
	handler http.Handler
	token   string
	session surface.Session
}

// newOrphanFixture registers a background Claude session whose process has
// exited, behind the real Claude adapter and the daemon's public API.
func newOrphanFixture(t *testing.T) *orphanFixture {
	t.Helper()
	return newOrphanFixtureOn(t, "uds")
}

// newOrphanFixtureOn registers the exited session on transport, "uds" for a
// messaging socket or "" for Remote Control alone.
func newOrphanFixtureOn(t *testing.T, transport string) *orphanFixture {
	t.Helper()
	fixture := &orphanFixture{claude: claudetest.New(t), posts: &orphanPosts{}}
	r, err := registry.Open(t.TempDir() + "/registry.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	fixture.r = r
	fixture.session = surface.Session{ID: orphanConversation, Bridge: "session_orphan", Surface: surface.KindClaude, Name: "orphan", PID: orphanPID, Status: surface.StatusBusy, Transport: transport, Cwd: fixture.claude.Home, Transcript: fixture.claude.Transcript(orphanConversation, fixture.claude.Home)}
	if err := r.RegisterSession(fixture.session); err != nil {
		t.Fatal(err)
	}
	if err := r.SetAlias("orphan", fixture.session.ID); err != nil {
		t.Fatal(err)
	}
	fixture.claude.Job("fe758937", orphanConversation, fixture.claude.Home, "done", 0)
	fixture.claude.ResumeBridge("session_orphan")
	fixture.daemon = New(r, []surface.Surface{surfaces.NewClaudeWithRequest("", fixture.claude.Home, fixture.posts.request)})
	fixture.handler = dashboardRouter(fixture.daemon)
	fixture.token = pairedControlToken(t, r)
	return fixture
}

func (f *orphanFixture) send(t *testing.T, key, message string) (int, map[string]any) {
	t.Helper()
	return f.action(t, key, map[string]string{"action": "send", "sessionId": f.session.ID, "message": message})
}

func (f *orphanFixture) action(t *testing.T, key string, request map[string]string) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(request)
	response := serveV1Action(f.handler, string(body), key, f.token)
	var decoded map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	return response.Code, decoded
}

func (f *orphanFixture) resumes() int {
	return f.claude.Invocations("--bg", "--resume", orphanConversation)
}

func receiptField(body map[string]any, field string) string {
	if value, ok := body[field].(string); ok {
		return value
	}
	if result, ok := body["result"].(map[string]any); ok {
		value, _ := result[field].(string)
		return value
	}
	return ""
}

func TestAPISendToEndedBackgroundJobResumesAndDeliversOnce(t *testing.T) {
	f := newOrphanFixture(t)
	code, body := f.send(t, "orphan-1", "hello")
	if code != http.StatusOK || receiptField(body, "evidence") != string(surface.EvidenceDelivered) {
		t.Fatalf("status=%d body=%v", code, body)
	}
	if code, body = f.send(t, "orphan-1", "hello"); code != http.StatusOK {
		t.Fatalf("replay status=%d body=%v", code, body)
	}
	if f.resumes() != 1 || f.posts.count("hello") != 1 {
		t.Fatalf("resumes=%d posts=%d", f.resumes(), f.posts.count("hello"))
	}
	session, err := f.r.Session(f.session.ID)
	if err != nil || session.PID != f.claude.PID {
		t.Fatalf("session=%+v err=%v", session, err)
	}
	if sid, err := f.r.LookupAlias("orphan"); err != nil || sid != f.session.ID {
		t.Fatalf("alias=%q err=%v", sid, err)
	}
	if code, _ := f.send(t, "orphan-2", "next"); code != http.StatusOK || f.resumes() != 1 || f.posts.count("next") != 1 {
		t.Fatalf("second send status=%d resumes=%d posts=%d", code, f.resumes(), f.posts.count("next"))
	}
}

func TestAPISendHoldsForLiveUnreachableProcessUntilItIsReachable(t *testing.T) {
	f := newOrphanFixture(t)
	f.claude.Record(orphanConversation, f.claude.Home, "")
	code, body := f.send(t, "held-1", "hello")
	if code != http.StatusOK || receiptField(body, "evidence") != string(surface.EvidenceQueued) {
		t.Fatalf("status=%d body=%v", code, body)
	}
	f.daemon.scanAndRelay(context.Background())
	if f.posts.count("hello") != 0 || f.resumes() != 0 {
		t.Fatalf("held message went out: posts=%d resumes=%d", f.posts.count("hello"), f.resumes())
	}
	f.claude.Record(orphanConversation, f.claude.Home, f.session.ID)
	f.daemon.scanAndRelay(context.Background())
	f.daemon.scanAndRelay(context.Background())
	if f.posts.count("hello") != 1 || f.resumes() != 0 {
		t.Fatalf("posts=%d resumes=%d", f.posts.count("hello"), f.resumes())
	}
}

func TestAPISendHoldsForLiveUnreachableProcessThenResumesAfterItExits(t *testing.T) {
	f := newOrphanFixture(t)
	f.claude.Record(orphanConversation, f.claude.Home, "")
	if code, body := f.send(t, "held-2", "hello"); code != http.StatusOK || receiptField(body, "evidence") != string(surface.EvidenceQueued) {
		t.Fatalf("status=%d body=%v", code, body)
	}
	f.claude.RemoveRecord()
	f.daemon.scanAndRelay(context.Background())
	f.daemon.scanAndRelay(context.Background())
	if f.posts.count("hello") != 1 || f.resumes() != 1 {
		t.Fatalf("posts=%d resumes=%d", f.posts.count("hello"), f.resumes())
	}
}

func TestAPISendWhoseResumeIsUncertainStaysUnknown(t *testing.T) {
	f := newOrphanFixture(t)
	f.claude.ResumeCopy("90b6ce14")
	code, body := f.send(t, "copy-1", "hello")
	if code != http.StatusOK || receiptField(body, "status") != "submitted" || receiptField(body, "evidence") != string(surface.EvidenceSubmitted) {
		t.Fatalf("status=%d body=%v", code, body)
	}
	if f.posts.count("hello") != 0 || f.claude.Invocations("stop", "90b6ce14") != 1 {
		t.Fatalf("posts=%d stops=%d", f.posts.count("hello"), f.claude.Invocations("stop", "90b6ce14"))
	}
}

func TestQueuedRelayToExitedSessionWithUncertainResumeIsNotRetried(t *testing.T) {
	f := newOrphanFixture(t)
	f.claude.ResumeCopy("90b6ce14")
	id, err := f.r.QueueRelayMessage(f.session.ID, "relayed", "relay:fixture", 1)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		f.daemon.scanAndRelay(context.Background())
	}
	item, err := f.r.QueueItem(id, nil)
	if err != nil || item.Status != "dead" || f.resumes() != 1 || f.posts.count("relayed") != 0 {
		t.Fatalf("item=%+v err=%v resumes=%d posts=%d", item, err, f.resumes(), f.posts.count("relayed"))
	}
}

func TestRelayToExitedClaudeSessionResumesAndDeliversOnce(t *testing.T) {
	f := newOrphanFixture(t)
	from := surface.Session{ID: "from", Surface: surface.KindCodex, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
	if err := f.r.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.AddRoute(from.ID, f.session.ID, ".*"); err != nil {
		t.Fatal(err)
	}
	f.daemon.fireRelays(&from, "done", 0, "finished")
	for range 2 {
		f.daemon.scanAndRelay(context.Background())
	}
	if f.resumes() != 1 || f.posts.containing("] finished") != 1 {
		t.Fatalf("resumes=%d posts=%d", f.resumes(), f.posts.containing("] finished"))
	}
	if routes, err := f.r.ListRoutes(); err != nil || len(routes) != 1 {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
}

func TestAPIPersonSendToEndedBackgroundJobResumesAndPostsOneHumanTurn(t *testing.T) {
	f := newOrphanFixture(t)
	code, body := f.action(t, "person-1", map[string]string{"action": "send", "author": "person", "sessionId": f.session.ID, "message": "hello"})
	if code != http.StatusOK || receiptField(body, "evidence") != string(surface.EvidenceDelivered) {
		t.Fatalf("status=%d body=%v", code, body)
	}
	if f.resumes() != 1 || f.posts.count("hello") != 1 || f.posts.containing(`"type":"user"`) != 1 || f.posts.containing(`"session_id":"session_orphan"`) != 1 {
		t.Fatalf("resumes=%d posts=%v", f.resumes(), f.posts.bodies)
	}
}

func TestQueuedMessageToAResumeClaudeRefusesIsHeldWithItsReasonAndAnnouncedOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newOrphanFixture(t)
	f.claude.ResumeRefused("Workspace not trusted. Run claude in this directory first.")
	sender := surface.Session{ID: "sender", Surface: surface.KindCodex, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
	if err := f.r.RegisterSession(sender); err != nil {
		t.Fatal(err)
	}
	id, _, err := f.r.QueueDeliveryWithIntent(f.session.ID, "hello", "refused-1", surface.SendOptions{SourceSessionID: sender.ID})
	if err != nil {
		t.Fatal(err)
	}
	f.daemon.scanAndRelay(context.Background())
	item, err := f.r.QueueItem(id, nil)
	if err != nil || item.Status != "pending" || item.HeldBy != registry.QueueHeldRefused || !strings.Contains(item.LastError, "Workspace not trusted") || f.posts.count("hello") != 0 {
		t.Fatalf("item=%+v err=%v posts=%d", item, err, f.posts.count("hello"))
	}
	attention, err := f.r.ListAttentionItems(false)
	if err != nil || len(attention) != 1 || attention[0].QueueID != id {
		t.Fatalf("attention=%+v err=%v", attention, err)
	}
	rows, err := f.r.ListQueue(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	notices := 0
	for _, row := range rows {
		if row.SessionID == sender.ID && strings.Contains(row.Message, "Workspace not trusted") {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("sender notices=%d rows=%+v", notices, rows)
	}
}
