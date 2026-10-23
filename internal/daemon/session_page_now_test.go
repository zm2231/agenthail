package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

type slowCatchUpClaude struct {
	*surfaces.Claude
	delay time.Duration
}

func (c *slowCatchUpClaude) TranscriptItems(ctx context.Context, session *surface.Session, request surface.TranscriptItemsRequest) (*surface.TranscriptItemsResult, error) {
	if request.Identity != "" {
		time.Sleep(c.delay)
	}
	return c.Claude.TranscriptItems(ctx, session, request)
}

func TestACatchUpThatOutlastsThePageWaitArrivesOverTheStreamInTranscriptOrder(t *testing.T) {
	previousGrace, previousWait := sessionPageHandoffGrace, sessionPageCatchUpWait
	sessionPageHandoffGrace, sessionPageCatchUpWait = 10*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { sessionPageHandoffGrace, sessionPageCatchUpWait = previousGrace, previousWait })
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	d, r, _, _, _ := daemonFixture(t)
	t.Cleanup(d.sources.shutdown)
	fixture := claudetest.New(t)
	const conversation = "page-now"
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
	const catchUpDelay = 1500 * time.Millisecond
	d.Surfaces = []surface.Surface{&slowCatchUpClaude{Claude: surfaces.NewClaude(signin.Fixed{Browser: signin.Chrome}, fixture.Home), delay: catchUpDelay}}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	if got := readDashboardExchanges(t, handler, conversation, ""); len(got) != 2 {
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
	appendToFile(t, path, claudeTurns(conversation, 3, 5))

	request := httptest.NewRequest(http.MethodGet, "/api/session?id="+conversation+"&timeline=1", nil)
	request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	response := httptest.NewRecorder()
	started := time.Now()
	handler.ServeHTTP(response, request)
	elapsed := time.Since(started)
	var page struct {
		Exchanges  []pageExchange `json:"exchanges"`
		JournalSeq uint64         `json:"journalSeq"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || response.Code != http.StatusOK {
		t.Fatalf("status=%d err=%v body=%s", response.Code, err, response.Body.String())
	}
	if elapsed >= catchUpDelay/2 || len(page.Exchanges) != 2 || page.JournalSeq == 0 {
		t.Fatalf("page took %v with %d exchanges at seq %d; want the journal's page once the wait runs out", elapsed, len(page.Exchanges), page.JournalSeq)
	}

	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	streamRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/session-stream?id="+conversation+"&after="+strconvUint(page.JournalSeq), nil)
	if err != nil {
		t.Fatal(err)
	}
	streamRequest.Header.Set("Authorization", "Bearer secret")
	stream, err := http.DefaultClient.Do(streamRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if stream.StatusCode != http.StatusOK {
		t.Fatalf("stream status %d", stream.StatusCode)
	}
	answer := regexp.MustCompile(`answer (\d+)`)
	var answers []string
	reader := bufio.NewReader(stream.Body)
	live := false
	for len(answers) < 4 {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended after answers %v: %v", answers, err)
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var envelope sessionStreamEnvelope
		var payload sessionJournalPayload
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &envelope) != nil || json.Unmarshal(envelope.Data, &payload) != nil || payload.Kind != "message" {
			continue
		}
		if envelope.Seq <= page.JournalSeq {
			t.Fatalf("stream sent seq %d at or before the page's %d", envelope.Seq, page.JournalSeq)
		}
		if match := answer.FindStringSubmatch(payload.Body); match != nil {
			answers = append(answers, match[1])
		}
		if len(answers) == 3 && !live {
			live = true
			appendToFile(t, path, claudeTurns(conversation, 6, 6))
		}
	}
	if strings.Join(answers, ",") != "3,4,5,6" {
		t.Fatalf("stream answers %v, want 3,4,5 caught up then 6 live", answers)
	}
	got := readDashboardExchangesWhen(t, handler, conversation, func(got []pageExchange) bool { return len(got) == 6 && got[5].Assistant == "answer 6" })
	for index, exchange := range got {
		if exchange.Assistant != "answer "+strconvUint(uint64(index+1)) {
			t.Fatalf("page after catch-up and live: %+v", got)
		}
	}
	if len(got) != 6 {
		t.Fatalf("page after catch-up and live: %+v", got)
	}
}

func strconvUint(value uint64) string {
	return strconv.FormatUint(value, 10)
}

func TestAJournalAwaitingRederivationIsRebuiltBeforeItsPage(t *testing.T) {
	d, r, fake, from, _ := daemonFixture(t)
	t.Cleanup(d.sources.shutdown)
	fake.kind = surface.KindClaude
	fake.caps.Stream = true
	from.Surface, from.RuntimeKind, from.Transport, from.Transcript = surface.KindClaude, surface.RuntimeClaudeCLI, "", "/transcript.jsonl"
	if err := r.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	earlierDerivationJournal(t, r, from.ID)
	adapter := &rederiveSurface{daemonSurface: fake, items: transcriptItems()}
	d.Surfaces = []surface.Surface{adapter}
	if !surface.EffectiveCapabilities(adapter, &from).Stream {
		t.Fatal("the fixture session does not stream, so the page would wait regardless")
	}
	request := httptest.NewRequest(http.MethodGet, "/api/session?id="+from.ID+"&timeline=1", nil)
	request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	response := httptest.NewRecorder()
	d.dashboardHandler(&dashboardServer{token: "secret"}).ServeHTTP(response, request)
	var page struct {
		Timeline struct {
			Items []surface.TimelineItem `json:"items"`
		} `json:"timeline"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || response.Code != http.StatusOK {
		t.Fatalf("status=%d err=%v body=%s", response.Code, err, response.Body.String())
	}
	var ids []string
	for _, item := range page.Timeline.Items {
		ids = append(ids, item.ID)
	}
	got := strings.Join(ids, " ")
	if strings.Contains(" "+got+" ", " task-notification:monitor: ") || !strings.Contains(got, "task-notification:monitor::bbbb") || adapter.calls != 1 {
		t.Fatalf("page before the journal was rebuilt (%d rebuilds): %s", adapter.calls, got)
	}
}
