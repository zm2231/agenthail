package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type sorterStub struct {
	mu      sync.Mutex
	asked   []sorterAsk
	answer  func(message string) (int, string)
	release chan struct{}
}

type sorterAsk struct {
	Message string
	Reply   string
	Goals   []string
}

func (s *sorterStub) serve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		State struct {
			Message string `json:"message"`
			Reply   string `json:"agent_previous_reply"`
		} `json:"state"`
		Questions struct {
			Goal struct {
				Criteria map[string]json.RawMessage `json:"criteria"`
			} `json:"goal"`
		} `json:"questions"`
	}
	data, _ := io.ReadAll(r.Body)
	json.Unmarshal(data, &body)
	if s.release != nil {
		<-s.release
	}
	ask := sorterAsk{Message: body.State.Message, Reply: body.State.Reply}
	for id := range body.Questions.Goal.Criteria {
		ask.Goals = append(ask.Goals, id)
	}
	s.mu.Lock()
	s.asked = append(s.asked, ask)
	answer := s.answer
	s.mu.Unlock()
	status, reply := answer(ask.Message)
	w.WriteHeader(status)
	io.WriteString(w, reply)
}

func (s *sorterStub) setAnswer(answer func(string) (int, string)) {
	s.mu.Lock()
	s.answer = answer
	s.mu.Unlock()
}

func (s *sorterStub) asks() []sorterAsk {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sorterAsk{}, s.asked...)
}

func fileTo(goalID string) func(string) (int, string) {
	return func(string) (int, string) {
		return http.StatusOK, fmt.Sprintf(`{"answers":{"goal":{"choice":%q,"confidence":0.95}}}`, goalID)
	}
}

func sorterFixture(t *testing.T, stub *sorterStub) (*Daemon, *registry.Registry, http.Handler, ledgerProject, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	server := httptest.NewServer(http.HandlerFunc(stub.serve))
	t.Cleanup(server.Close)
	if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`{"ledgerSorter": {"url": %q, "model": "d1-3b"}}`, server.URL)
	if err := os.WriteFile(filepath.Join(home, ".agenthail", "config.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	d, r, handler := ledgerDaemon(t)
	d.sorter.http = server.Client()
	p := addLedgerProject(t, r, "proj-sorter", "sorter-key")
	if err := p.store.Enable(p.key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	g, _, err := p.store.EnsureGoal(p.key, "search", "lead")
	if err != nil {
		t.Fatal(err)
	}
	return d, r, handler, p, g.ID
}

func runSorter(t *testing.T, d *Daemon) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.runLedgerSorter(ctx)
	}()
	stop = func() {
		cancel()
		<-done
	}
	t.Cleanup(stop)
	return stop
}

func TestAfterARestartOwedMessagesAreSortedOldestFirstAcrossProjects(t *testing.T) {
	stub := &sorterStub{answer: fileTo("new")}
	d, r, handler, p, _ := sorterFixture(t, stub)
	early := addLedgerProject(t, r, "proj-a", "a-sorter-key")
	if err := early.store.Enable(early.key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	if _, _, err := early.store.EnsureGoal(early.key, "billing", "lead"); err != nil {
		t.Fatal(err)
	}
	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: p.session(t, r, "claude-later-key").ID, PromptID: "older", Prompt: "the older message"})
	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: early.session(t, r, "claude-earlier-key").ID, PromptID: "newer", Prompt: "the newer message"})
	restarted := newTestDaemon(t, r, []surface.Surface{&daemonSurface{sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}, accepted: true}})
	restarted.sorter.http = d.sorter.http
	runSorter(t, restarted)
	eventually(t, "both owed messages to be asked about", func() bool { return len(stub.asks()) == 2 })
	if asked := stub.asks(); asked[0].Message != "the older message" || asked[1].Message != "the newer message" {
		t.Fatalf("asked %q then %q, want the older message first whatever its project", asked[0].Message, asked[1].Message)
	}
}

func TestARestartDuringAnOutageSortsWhatWasOwedOnceTheSorterIsBack(t *testing.T) {
	stub := &sorterStub{answer: func(string) (int, string) { return http.StatusServiceUnavailable, "down" }}
	d, r, handler, p, search := sorterFixture(t, stub)
	s := p.session(t, r, "claude-restart")
	stop := runSorter(t, d)
	for _, id := range []string{"down-1", "down-2"} {
		ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: id, Prompt: "search words " + id})
	}
	eventually(t, "two failures", func() bool { return d.sorterHealth().Failures == 2 })
	stop()
	restarted := newTestDaemon(t, r, []surface.Surface{&daemonSurface{sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}, accepted: true}})
	restarted.sorter.http, restarted.sorter.retry = d.sorter.http, 50*time.Millisecond
	asked := len(stub.asks())
	runSorter(t, restarted)
	eventually(t, "the restarted daemon to try what it owes", func() bool { return len(stub.asks()) > asked })
	if got := sortedGoal(t, p, ledger.PromptRef("down-1")); got != "" {
		t.Fatalf("filed under %q while the sorter was down", got)
	}
	stub.setAnswer(fileTo(search))
	for _, id := range []string{"down-1", "down-2"} {
		eventually(t, id+" to be filed after the restart", func() bool { return sortedGoal(t, p, ledger.PromptRef(id)) == search })
	}
	if h := restarted.sorterHealth(); h.Failures != 0 {
		t.Fatalf("health after recovery = %+v", h)
	}
}

func sortedGoal(t *testing.T, p ledgerProject, ref string) string {
	t.Helper()
	board, err := p.store.Board(p.key)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range board.Utterances() {
		if u.ID == ref && u.Sorted != nil {
			return u.Sorted.GoalID
		}
	}
	return ""
}

func TestCaptureNeverWaitsForTheSorterAndAConfidentAnswerFilesTheMessage(t *testing.T) {
	stub := &sorterStub{answer: fileTo(""), release: make(chan struct{})}
	d, r, handler, p, search := sorterFixture(t, stub)
	stub.setAnswer(fileTo(search))
	s := p.session(t, r, "claude-sorted")
	reply := `{"itemId":"r1","version":1,"op":"upsert","kind":"message","role":"assistant","body":"I added the search box.","ts":"` + time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano) + `"}`
	if _, _, err := r.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: s.ID, Kind: "message", ProviderKey: "r1", Payload: []byte(reply), ObservedAt: time.Now()}, registry.SessionJournalRetention{Count: 8, Bytes: 4096}); err != nil {
		t.Fatal(err)
	}
	runSorter(t, d)
	start := time.Now()
	resp := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "p-1", Prompt: "rank exact matches first"})
	if waited := time.Since(start); waited > 2*time.Second || !resp.Captured {
		t.Fatalf("capture took %s while the sorter was busy (captured=%v)", waited, resp.Captured)
	}
	if got := sortedGoal(t, p, ledger.PromptRef("p-1")); got != "" {
		t.Fatalf("filed under %q before the sorter answered", got)
	}
	close(stub.release)
	eventually(t, "the message to be filed", func() bool { return sortedGoal(t, p, ledger.PromptRef("p-1")) == search })
	asked := stub.asks()
	if len(asked) != 1 || asked[0].Message != "rank exact matches first" || asked[0].Reply != "I added the search box." || len(asked[0].Goals) != 3 {
		t.Fatalf("asked = %+v, want the words, the agent's previous reply and search, new and none", asked)
	}
	words, err := p.store.ProvisionalFor(p.key, "")
	if err != nil || len(words) != 0 {
		t.Fatalf("provisional after filing = %v (%v)", words, err)
	}
}

func TestAnUnsureOrOffTopicAnswerLeavesTheMessageProvisional(t *testing.T) {
	stub := &sorterStub{}
	d, r, handler, p, search := sorterFixture(t, stub)
	stub.setAnswer(func(message string) (int, string) {
		switch message {
		case "thanks":
			return http.StatusOK, `{"answers":{"goal":{"choice":"none","confidence":0.99}}}`
		case "maybe search":
			return http.StatusOK, fmt.Sprintf(`{"answers":{"goal":{"choice":%q,"confidence":0.85}}}`, search)
		case "surely search":
			return fileTo(search)(message)
		}
		return http.StatusOK, `{"answers":{"goal":{"choice":"other-G7","confidence":0.97}}}`
	})
	s := p.session(t, r, "claude-unsure")
	runSorter(t, d)
	for i, words := range []string{"thanks", "maybe search", "a goal from another project", "surely search"} {
		ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: fmt.Sprintf("u-%d", i), Prompt: words})
	}
	eventually(t, "the last message to be filed", func() bool { return sortedGoal(t, p, ledger.PromptRef("u-3")) == search })
	words, err := p.store.ProvisionalFor(p.key, "")
	if err != nil || len(words) != 3 {
		t.Fatalf("provisional = %d (%v), want all three", len(words), err)
	}
}

func TestAnUnavailableSorterRecoversOnlyWhatWasCapturedWhileItFailed(t *testing.T) {
	stub := &sorterStub{answer: fileTo("new")}
	d, r, handler, p, search := sorterFixture(t, stub)
	s := p.session(t, r, "claude-outage")
	runSorter(t, d)
	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "before", Prompt: "an unrelated topic"})
	eventually(t, "the first answer", func() bool { return len(stub.asks()) == 1 })
	stub.setAnswer(func(string) (int, string) { return http.StatusServiceUnavailable, "down" })
	for _, id := range []string{"down-1", "down-2"} {
		ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: id, Prompt: "search words " + id})
	}
	eventually(t, "two failures", func() bool {
		h := d.sorterHealth()
		return h != nil && h.Failures == 2
	})
	health := d.sorterHealth()
	if health.Model != "d1-3b" || health.Since.IsZero() || health.Error == "" {
		t.Fatalf("health while failing = %+v", health)
	}
	read, err := d.ledgerRead(p.key, ledger.ReadRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := read["sorter"]; !ok {
		t.Fatal("the board read has no sorter health line")
	}
	stub.setAnswer(fileTo(search))
	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "after", Prompt: "search words after"})
	for _, id := range []string{"after", "down-1", "down-2"} {
		eventually(t, id+" to be filed", func() bool { return sortedGoal(t, p, ledger.PromptRef(id)) == search })
	}
	if got := sortedGoal(t, p, ledger.PromptRef("before")); got != "" {
		t.Fatalf("a message answered before the outage was sorted again, to %q", got)
	}
	asked := 0
	for _, a := range stub.asks() {
		if a.Message == "an unrelated topic" {
			asked++
		}
	}
	if asked != 1 {
		t.Fatalf("the message from before the outage was asked about %d times, want once", asked)
	}
	if h := d.sorterHealth(); h.Failures != 0 || !h.Since.IsZero() || h.Error != "" {
		t.Fatalf("health after recovery = %+v", h)
	}
}

func TestAFullQueueLeavesTheMessageProvisionalAndOwesItASort(t *testing.T) {
	d, _, _ := ledgerDaemon(t)
	for i := range sorterQueue + 2 {
		d.queueSort("k", fmt.Sprintf("claude-prompt:%d", i), int64(100+i), time.Now())
	}
	d.sorter.mu.Lock()
	defer d.sorter.mu.Unlock()
	if d.sorter.dropped != 2 || d.sorter.backlog["k"] != 100+sorterQueue {
		t.Fatalf("dropped %d, owed from %v", d.sorter.dropped, d.sorter.backlog["k"])
	}
}

func TestWithoutTheConfigKeyNothingIsAskedAndNoHealthIsShown(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stub := &sorterStub{answer: fileTo("new")}
	server := httptest.NewServer(http.HandlerFunc(stub.serve))
	defer server.Close()
	d, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-off", "off-key")
	if err := p.store.Enable(p.key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.store.EnsureGoal(p.key, "search", "lead"); err != nil {
		t.Fatal(err)
	}
	runSorter(t, d)
	s := p.session(t, r, "claude-off")
	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "off", Prompt: "search words"})
	eventually(t, "the queue to drain", func() bool { return len(d.sorter.jobs) == 0 })
	if len(stub.asks()) != 0 || d.sorterHealth() != nil {
		t.Fatalf("sorting is off yet asked %d times, health %+v", len(stub.asks()), d.sorterHealth())
	}
}
