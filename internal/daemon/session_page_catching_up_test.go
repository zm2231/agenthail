package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

type heldCatchUpClaude struct {
	*surfaces.Claude
	gate chan struct{}
}

func (c *heldCatchUpClaude) TranscriptItems(ctx context.Context, session *surface.Session, request surface.TranscriptItemsRequest) (*surface.TranscriptItemsResult, error) {
	select {
	case <-c.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return c.Claude.TranscriptItems(ctx, session, request)
}

func TestASeededSessionsPageAnswersFromTheJournalWhileItCatchesUp(t *testing.T) {
	previousGrace := sessionPageHandoffGrace
	sessionPageHandoffGrace = 10 * time.Millisecond
	t.Cleanup(func() { sessionPageHandoffGrace = previousGrace })
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	d, r, _, _, _ := daemonFixture(t)
	t.Cleanup(d.sources.shutdown)
	fixture := claudetest.New(t)
	const conversation = "page-catching-up"
	path := filepath.Join(fixture.Home, ".claude", "projects", "fixture-project", conversation+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(claudeTurns(conversation, 1, 2)), 0600); err != nil {
		t.Fatal(err)
	}
	session := surface.Session{ID: conversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle, Transcript: path, Cwd: fixture.Home}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	adapter := &heldCatchUpClaude{Claude: surfaces.NewClaude(signin.Fixed{Browser: signin.Chrome}, fixture.Home), gate: make(chan struct{})}
	d.Surfaces = []surface.Surface{adapter}
	handler := dashboardRouter(d)
	if got := readDashboardExchanges(t, handler, conversation, ""); len(got) != 2 {
		t.Fatalf("first page exchanges=%+v", got)
	}
	waitForNoSources(t, d)
	appendToFile(t, path, claudeTurns(conversation, 3, 5))
	started := time.Now()
	request := httptest.NewRequest(http.MethodGet, "/api/session?id="+conversation+"&timeline=1", nil)
	addBrowserCookie(handler, request)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var page timelinePage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || response.Code != http.StatusOK {
		t.Fatalf("status=%d err=%v body=%s", response.Code, err, response.Body.String())
	}
	if elapsed := time.Since(started); elapsed > sessionPageCatchUpWait/2 {
		t.Fatalf("the page took %v; a seeded journal answers without waiting for catch-up", elapsed)
	}
	if !page.CatchingUp || len(page.exchanges(t)) != 2 || page.JournalSeq == 0 {
		t.Fatalf("page while catching up: catchingUp=%v exchanges=%d journalSeq=%d", page.CatchingUp, len(page.exchanges(t)), page.JournalSeq)
	}
	close(adapter.gate)
	if got := readDashboardExchanges(t, handler, conversation, "&wait=1"); len(got) != 5 || got[4].Assistant != "answer 5" {
		t.Fatalf("page that waits after catch-up: %+v, want answers 1-5", got)
	}
}
