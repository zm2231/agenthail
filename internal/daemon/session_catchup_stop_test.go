package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func TestAStoppingDaemonStopsACatchUpAndTheNextStartFinishesIt(t *testing.T) {
	previousGrace := sessionPageHandoffGrace
	sessionPageHandoffGrace = 10 * time.Millisecond
	t.Cleanup(func() { sessionPageHandoffGrace = previousGrace })
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	d, r, _, _, _ := daemonFixture(t)
	fixture := claudetest.New(t)
	const conversation = "catch-up-stop"
	path := filepath.Join(fixture.Home, ".claude", "projects", "fixture-project", conversation+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(claudeTurns(conversation, 1, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession(surface.Session{ID: conversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle, Transcript: path, Cwd: fixture.Home}); err != nil {
		t.Fatal(err)
	}
	claude := surfaces.NewClaude(signin.Fixed{Browser: signin.Chrome}, fixture.Home)
	d.Surfaces = []surface.Surface{claude}
	handler := dashboardRouter(d)
	if got := readDashboardExchanges(t, handler, conversation, ""); len(got) != 1 {
		t.Fatalf("first page exchanges=%+v", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		d.sources.mu.Lock()
		running := len(d.sources.sources)
		d.sources.mu.Unlock()
		if running == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("page source did not stop after its handoff lease")
		}
		time.Sleep(5 * time.Millisecond)
	}
	sessionPageHandoffGrace = previousGrace
	const last = 301
	appendToFile(t, path, claudeTurns(conversation, 2, last))

	var appended atomic.Int64
	stopped := make(chan struct{})
	d.sources.prompts = func(surface.Session, sessionJournalPayload) {
		if appended.Add(1) != 5 {
			return
		}
		go func() {
			defer close(stopped)
			d.sources.shutdown()
		}()
		for {
			d.sources.mu.Lock()
			closed := d.sources.closed
			d.sources.mu.Unlock()
			if closed {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}
	paged := make(chan struct{})
	go func() {
		defer close(paged)
		request := httptest.NewRequest(http.MethodGet, "/api/session?id="+conversation+"&timeline=1", nil)
		addBrowserCookie(handler, request)
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}()
	select {
	case <-stopped:
	case <-time.After(20 * time.Second):
		t.Fatal("the daemon did not stop while its catch-up ran")
	}
	<-paged
	if rows := appended.Load(); rows > 20 {
		t.Fatalf("the catch-up appended %d rows after the daemon began to stop", rows-5)
	}

	restarted := newTestDaemon(t, r, []surface.Surface{claude})
	got := readDashboardExchangesWhen(t, dashboardRouter(restarted), conversation, func(got []pageExchange) bool {
		return len(got) > 1 && got[len(got)-1].Assistant == "answer 301"
	})
	if len(got) < 2 || got[len(got)-2].Assistant != "answer 300" || got[len(got)-1].Assistant != "answer 301" {
		t.Fatalf("after a restart the page ends %+v, want answers 300 and 301", got[max(0, len(got)-2):])
	}
}

// stoppingRederive stops its source as the transcript read for a rederive
// returns, the way a daemon shutdown lands between the read and the write.
type stoppingRederive struct {
	*rederiveSurface
	stop func()
}

func (s *stoppingRederive) TranscriptItems(ctx context.Context, session *surface.Session, request surface.TranscriptItemsRequest) (*surface.TranscriptItemsResult, error) {
	read, err := s.rederiveSurface.TranscriptItems(ctx, session, request)
	if request.Identity == "" {
		s.stop()
	}
	return read, err
}

func TestARederiveTheDaemonStopsDuringLeavesTheJournalForTheNextStart(t *testing.T) {
	_, r, fake, from, _ := daemonFixture(t)
	from.Surface, from.RuntimeKind, from.Transport, from.Transcript = surface.KindClaude, surface.RuntimeClaudeCLI, "", "/transcript.jsonl"
	if err := r.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	earlierDerivationJournal(t, r, from.ID)
	inner := &rederiveSurface{daemonSurface: fake, items: transcriptItems()}
	sourceCtx, stop := context.WithCancel(context.Background())
	stopped := &sessionSource{manager: newTestSourceManager(t, r), session: from, adapter: &stoppingRederive{rederiveSurface: inner, stop: stop}, epoch: "stopped", ctx: sourceCtx, appendBodies: map[string]*streamedBody{}, followers: map[uint64]chan struct{}{}}
	if err := stopped.loadSeed(); !errors.Is(err, context.Canceled) || inner.calls != 1 {
		t.Fatalf("a rederive stopped after its read returned err=%v after %d reads", err, inner.calls)
	}
	if turn := journalTurnOf(t, r, from.ID, "100-aaaaaa-0"); turn != "" {
		t.Fatalf("a stopped rederive rewrote the journal: the first reply is in turn %q", turn)
	}
	next := &sessionSource{manager: newTestSourceManager(t, r), session: from, adapter: inner, epoch: "next", ctx: context.Background(), appendBodies: map[string]*streamedBody{}, followers: map[uint64]chan struct{}{}}
	if err := next.loadSeed(); err != nil || inner.calls != 2 {
		t.Fatalf("the next start did not rederive: err=%v reads=%d", err, inner.calls)
	}
	if turn := journalTurnOf(t, r, from.ID, "100-aaaaaa-0"); turn != "u1" {
		t.Fatalf("the next start did not rederive the journal: the first reply is in turn %q", turn)
	}
}

func journalTurnOf(t *testing.T, r *registry.Registry, sessionID, itemID string) string {
	t.Helper()
	page, err := r.ReadSessionJournalPage(sessionID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range page.Entries {
		var payload sessionJournalPayload
		if json.Unmarshal(entry.Payload, &payload) == nil && payload.ItemID == itemID {
			return payload.TurnID
		}
	}
	t.Fatalf("the journal has no row %s", itemID)
	return ""
}
