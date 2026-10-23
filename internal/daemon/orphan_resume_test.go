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

	"github.com/zm2231/agenthail/internal/signin"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
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

func (p *orphanPosts) request(_ context.Context, method, url string, _ map[string]string, body string, _ surfaces.CookieSource, _ time.Duration) (int, string, error) {
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
	host    *claudehosttest.Fixture
	posts   *orphanPosts
	daemon  *Daemon
	r       *registry.Registry
	handler http.Handler
	token   string
	session surface.Session
}

// newOrphanFixture registers a background Claude session whose process has
// exited, behind the real Claude adapter, a running Claude host and the
// daemon's public API.
func newOrphanFixture(t *testing.T) *orphanFixture {
	t.Helper()
	return newOrphanFixtureOn(t, "uds")
}

// newOrphanFixtureOn registers the exited session on transport, "uds" for a
// messaging socket or "" for Remote Control alone.
func newOrphanFixtureOn(t *testing.T, transport string) *orphanFixture {
	t.Helper()
	fixture := &orphanFixture{claude: claudetest.New(t), posts: &orphanPosts{}}
	fixture.host = claudehosttest.New(t, fixture.claude.Home)
	fixture.host.Start(t)
	r, err := registry.Open(t.TempDir() + "/registry.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	fixture.r = r
	fixture.session = surface.Session{ID: orphanConversation, Bridge: "session_orphan", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "orphan", PID: orphanPID, Status: surface.StatusBusy, Transport: transport, Cwd: fixture.claude.Home, HasLocal: true, Transcript: fixture.claude.Transcript(orphanConversation, fixture.claude.Home)}
	if err := r.RegisterSession(fixture.session); err != nil {
		t.Fatal(err)
	}
	if err := r.SetAlias("orphan", fixture.session.ID); err != nil {
		t.Fatal(err)
	}
	fixture.claude.Job("fe758937", orphanConversation, fixture.claude.Home, "done", 0)
	// Codex carries the voice operator the voice fixtures create.
	operator := surface.Session{ID: "operator", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "Voice operator", Transport: "desktop", Status: surface.StatusIdle}
	codex := &daemonSurface{sessions: map[string]surface.Session{operator.ID: operator}, observations: map[string]*surface.TurnObservation{}, accepted: true}
	fixture.daemon = newTestDaemon(t, r, []surface.Surface{surfaces.NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, fixture.claude.Home, fixture.posts.request), codex})
	fixture.handler = dashboardRouter(fixture.daemon)
	fixture.token = pairedControlToken(t, r)
	return fixture
}

func (f *orphanFixture) send(t *testing.T, key, message string) (int, map[string]any) {
	t.Helper()
	return f.action(t, key, map[string]string{"action": "send", "sessionId": f.session.ID, "message": message})
}

func (f *orphanFixture) sendAsPerson(t *testing.T, key, message string) (int, map[string]any) {
	t.Helper()
	return f.action(t, key, map[string]string{"action": "send", "author": "person", "sessionId": f.session.ID, "message": message})
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

// resumes counts the Claude processes the host started for the conversation.
func (f *orphanFixture) resumes(t *testing.T) int {
	t.Helper()
	return len(f.host.Spawns(t, orphanConversation))
}

// turns are the messages the resumed Claude ran, by text.
func (f *orphanFixture) turns(t *testing.T) []string {
	t.Helper()
	var texts []string
	for _, turn := range f.host.Turns(t, orphanConversation) {
		texts = append(texts, turn.Text)
	}
	return texts
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

func TestAPIPersonSendToEndedBackgroundJobResumesItInTheHostAndDeliversOnce(t *testing.T) {
	f := newOrphanFixture(t)
	code, body := f.sendAsPerson(t, "orphan-1", "hello")
	if code != http.StatusOK || receiptField(body, "evidence") != string(surface.EvidenceDelivered) {
		t.Fatalf("status=%d body=%v", code, body)
	}
	if code, body = f.sendAsPerson(t, "orphan-1", "hello"); code != http.StatusOK {
		t.Fatalf("replay status=%d body=%v", code, body)
	}
	claudehosttest.WaitFor(t, "the turn", func() bool { return len(f.turns(t)) == 1 })
	turn := f.host.Turns(t, orphanConversation)[0]
	intent, err := f.r.DeliveryIntent(int64(body["result"].(map[string]any)["deliveryId"].(float64)))
	if err != nil || turn.Text != "hello" || turn.UUID != intent.UUID || turnOrigin(t, turn) != "human" || f.resumes(t) != 1 || f.posts.count("hello") != 0 {
		t.Fatalf("turn=%+v intent=%+v err=%v resumes=%d posts=%d", turn, intent, err, f.resumes(t), f.posts.count("hello"))
	}
	session, err := f.r.Session(f.session.ID)
	if err != nil || session.RuntimeKind != surface.RuntimeClaudeHost || session.PID != f.host.Spawns(t, orphanConversation)[0].PID {
		t.Fatalf("session=%+v err=%v", session, err)
	}
	if sid, err := f.r.LookupAlias("orphan"); err != nil || sid != f.session.ID {
		t.Fatalf("alias=%q err=%v", sid, err)
	}
	if code, _ := f.sendAsPerson(t, "orphan-2", "next"); code != http.StatusOK || f.resumes(t) != 1 {
		t.Fatalf("second send status=%d resumes=%d", code, f.resumes(t))
	}
	claudehosttest.WaitFor(t, "the second turn", func() bool { return len(f.turns(t)) == 2 })
}

func TestAPIAgentSendToEndedBackgroundJobResumesItAndTakesItsSocket(t *testing.T) {
	f := newOrphanFixture(t)
	manager := claudetest.ListenPeerManager(t, f.claude.Home)
	code, body := f.send(t, "agent-1", "hello")
	if code != http.StatusOK || receiptField(body, "evidence") != string(surface.EvidenceTransportAccepted) {
		t.Fatalf("status=%d body=%v", code, body)
	}
	if f.resumes(t) != 1 || manager.Count("hello") != 1 || f.posts.count("hello") != 0 {
		t.Fatalf("resumes=%d peer=%d posts=%d", f.resumes(t), manager.Count("hello"), f.posts.count("hello"))
	}
}

func TestAPISendHoldsForLiveUnreachableProcessUntilItIsReachable(t *testing.T) {
	f := newOrphanFixture(t)
	f.claude.Record(orphanConversation, f.claude.Home, "")
	code, body := f.sendAsPerson(t, "held-1", "hello")
	if code != http.StatusOK || receiptField(body, "evidence") != string(surface.EvidenceQueued) {
		t.Fatalf("status=%d body=%v", code, body)
	}
	f.daemon.scanAndRelay(context.Background())
	if f.posts.count("hello") != 0 || f.resumes(t) != 0 {
		t.Fatalf("held message went out: posts=%d resumes=%d", f.posts.count("hello"), f.resumes(t))
	}
	f.claude.Record(orphanConversation, f.claude.Home, f.session.ID)
	f.daemon.scanAndRelay(context.Background())
	f.daemon.scanAndRelay(context.Background())
	if f.posts.count("hello") != 1 || f.resumes(t) != 0 {
		t.Fatalf("posts=%d resumes=%d", f.posts.count("hello"), f.resumes(t))
	}
}

func TestAPISendHoldsForLiveUnreachableProcessThenResumesAfterItExits(t *testing.T) {
	f := newOrphanFixture(t)
	f.claude.Record(orphanConversation, f.claude.Home, "")
	if code, body := f.sendAsPerson(t, "held-2", "hello"); code != http.StatusOK || receiptField(body, "evidence") != string(surface.EvidenceQueued) {
		t.Fatalf("status=%d body=%v", code, body)
	}
	f.claude.RemoveRecord()
	f.daemon.scanAndRelay(context.Background())
	f.daemon.scanAndRelay(context.Background())
	claudehosttest.WaitFor(t, "the held turn", func() bool { return len(f.turns(t)) == 1 })
	if f.resumes(t) != 1 || f.turns(t)[0] != "hello" {
		t.Fatalf("resumes=%d turns=%q", f.resumes(t), f.turns(t))
	}
}

func TestAPISendThatClaudeRefusesToResumeFailsWithClaudesReason(t *testing.T) {
	f := newOrphanFixture(t)
	f.host.Refuse(t, orphanConversation, "Error: Settings file not found: /fixture/settings.json")
	code, body := f.sendAsPerson(t, "refused-1", "hello")
	message := fmt.Sprint(body["error"])
	if code != http.StatusBadGateway || !strings.Contains(message, "Claude refused to resume "+orphanConversation) || !strings.Contains(message, "Settings file not found") {
		t.Fatalf("status=%d body=%v", code, body)
	}
	if len(f.turns(t)) != 0 {
		t.Fatalf("turns=%q, want nothing delivered", f.turns(t))
	}
}

func TestRelayToExitedClaudeSessionResumesAndDeliversOnce(t *testing.T) {
	f := newOrphanFixture(t)
	manager := claudetest.ListenPeerManager(t, f.claude.Home)
	from := surface.Session{ID: "from", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
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
	if f.resumes(t) != 1 || manager.Containing("] finished") != 1 {
		t.Fatalf("resumes=%d peer=%q", f.resumes(t), manager.Messages())
	}
	if routes, err := f.r.ListRoutes(); err != nil || len(routes) != 1 {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
}

func TestQueuedMessageToAResumeClaudeRefusesIsHeldWithItsReasonAndAnnouncedOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newOrphanFixture(t)
	f.host.Refuse(t, orphanConversation, "Error: Settings file not found: /fixture/settings.json")
	sender := surface.Session{ID: "sender", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
	if err := f.r.RegisterSession(sender); err != nil {
		t.Fatal(err)
	}
	id, _, err := f.r.QueueDeliveryWithIntent(f.session.ID, "hello", "refused-1", surface.SendOptions{SourceSessionID: sender.ID})
	if err != nil {
		t.Fatal(err)
	}
	f.daemon.scanAndRelay(context.Background())
	item, err := f.r.QueueItem(id, nil)
	if err != nil || item.Status != "pending" || item.HeldBy != registry.QueueHeldRefused || !strings.Contains(item.LastError, "Settings file not found") || len(f.turns(t)) != 0 {
		t.Fatalf("item=%+v err=%v turns=%q", item, err, f.turns(t))
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
		if row.SessionID == sender.ID && strings.Contains(row.Message, "Settings file not found") {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("sender notices=%d rows=%+v", notices, rows)
	}
}

// retire records the session in the catalog and runs the discovery passes
// that omit it, as the Claude adapter does once its process exits: the first
// pass retires it offline and the second drops its catalog row.
func (f *orphanFixture) retire(t *testing.T) {
	t.Helper()
	if _, _, err := f.r.RecordCatalogSession(registry.CatalogSessionState{Session: f.session, Project: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: time.Now().UTC(), ProjectionFingerprint: `{}`}, registry.CatalogEvent{DedupeKey: "session.upserted:" + f.session.ID, Type: "session.upserted", EntityID: f.session.ID, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := f.r.RecordCatalogDiscovery(surface.KindClaude, map[string]struct{}{}, time.Now().UTC(), true, 2); err != nil {
			t.Fatal(err)
		}
	}
	if stored, err := f.r.Session(f.session.ID); err != nil || stored.Status != surface.StatusOffline {
		t.Fatalf("stored=%+v err=%v, want the retired session offline", stored, err)
	}
}

func TestQueuedMessageToARetiredExitedClaudeSessionResumesAndDeliversOnce(t *testing.T) {
	f := newOrphanFixture(t)
	manager := claudetest.ListenPeerManager(t, f.claude.Home)
	f.retire(t)
	sender := surface.Session{ID: "sender", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
	if err := f.r.RegisterSession(sender); err != nil {
		t.Fatal(err)
	}
	id, _, err := f.r.QueueDeliveryWithIntent(f.session.ID, "hello", "retired-1", surface.SendOptions{SourceSessionID: sender.ID})
	if err != nil {
		t.Fatal(err)
	}
	if item, err := f.r.QueueItem(id, f.daemon.adapters()); err != nil || item.HeldBy != "" {
		t.Fatalf("item=%+v err=%v, want a resumable session's message ready to send", item, err)
	}
	for range 2 {
		f.daemon.scanAndRelay(context.Background())
	}
	if f.resumes(t) != 1 || manager.Count("hello") != 1 {
		t.Fatalf("resumes=%d peer=%q", f.resumes(t), manager.Messages())
	}
}

func TestRelayToARetiredExitedClaudeSessionResumesAndDeliversOnce(t *testing.T) {
	f := newOrphanFixture(t)
	manager := claudetest.ListenPeerManager(t, f.claude.Home)
	f.retire(t)
	from := surface.Session{ID: "from", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
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
	if f.resumes(t) != 1 || manager.Containing("] finished") != 1 {
		t.Fatalf("resumes=%d peer=%q", f.resumes(t), manager.Messages())
	}
}
