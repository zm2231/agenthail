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
	"strings"
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
	done := d.startLedgerSorter(ctx)
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

func TestARecoveryThatFailsPartwayOwesOnlyWhatItHadNotAsked(t *testing.T) {
	stub := &sorterStub{}
	d, r, handler, p, search := sorterFixture(t, stub)
	var mu sync.Mutex
	second := 0
	stub.setAnswer(func(message string) (int, string) {
		if message != "owed second" {
			return fileTo(search)(message)
		}
		mu.Lock()
		defer mu.Unlock()
		if second++; second == 1 {
			return http.StatusServiceUnavailable, "down"
		}
		return fileTo(search)(message)
	})
	s := p.session(t, r, "claude-partway")
	for _, words := range []string{"owed first", "owed second", "owed third"} {
		ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: words, Prompt: words})
	}
	d.sorter.retry = 50 * time.Millisecond
	runSorter(t, d)
	for _, id := range []string{"owed first", "owed second", "owed third"} {
		eventually(t, id+" to be filed", func() bool { return sortedGoal(t, p, ledger.PromptRef(id)) == search })
	}
	counts := map[string]int{}
	for _, a := range stub.asks() {
		counts[a.Message]++
	}
	if counts["owed first"] != 1 || counts["owed second"] != 2 || counts["owed third"] != 1 {
		t.Fatalf("asks per message = %v, want the first and third once and the failed one twice", counts)
	}
}

func TestAMessageAnsweredWithoutFilingDuringRecoveryIsNotAskedAgain(t *testing.T) {
	stub := &sorterStub{answer: func(string) (int, string) { return http.StatusServiceUnavailable, "down" }}
	d, r, handler, p, search := sorterFixture(t, stub)
	s := p.session(t, r, "claude-unfiled")
	runSorter(t, d)
	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "failed", Prompt: "search words"})
	eventually(t, "the failure", func() bool { return d.sorterHealth().Failures == 1 })
	stub.setAnswer(func(message string) (int, string) {
		if strings.HasPrefix(message, "search words") {
			return fileTo(search)(message)
		}
		return http.StatusOK, `{"answers":{"goal":{"choice":"new","confidence":0.99}}}`
	})
	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "unsure", Prompt: "something else entirely"})
	eventually(t, "the failed message to be filed", func() bool { return sortedGoal(t, p, ledger.PromptRef("failed")) == search })
	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "marker", Prompt: "search words again"})
	eventually(t, "the marker to be filed", func() bool { return sortedGoal(t, p, ledger.PromptRef("marker")) == search })
	counts := map[string]int{}
	for _, a := range stub.asks() {
		counts[a.Message]++
	}
	if counts["something else entirely"] != 1 || counts["search words"] != 2 {
		t.Fatalf("asks per message = %v, want the unfiled message once and the failed one twice", counts)
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
	owed := d.sorter.owedJobs()
	if d.sorter.dropped != 2 || len(owed) != 2 || owed[0].utteranceID != fmt.Sprintf("claude-prompt:%d", sorterQueue) || owed[1].seq != 101+sorterQueue {
		t.Fatalf("dropped %d, owed %+v, want exactly the two that did not fit", d.sorter.dropped, owed)
	}
}

func recordIn(t *testing.T, p ledgerProject, ref, text string) int64 {
	t.Helper()
	e, _, err := p.store.RecordUtterance(ledger.Utterance{ProjectKey: p.key, Ref: ref, SpeakerID: "person", TargetID: "s1", Text: text, CapturedBy: ledger.CapturedByHook, At: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	return e.Seq
}

func TestOneMessageInTwoProjectsIsOwedSeparatelyInEach(t *testing.T) {
	d, r, _, p, search := sorterFixture(t, &sorterStub{})
	other := addLedgerProject(t, r, "proj-other", "other-sorter-key")
	billing, _, err := other.store.EnsureGoal(other.key, "billing", "lead")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	otherAsks := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		data, _ := io.ReadAll(req.Body)
		if strings.Contains(string(data), billing.ID) {
			mu.Lock()
			otherAsks++
			first := otherAsks == 1
			mu.Unlock()
			if first {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			io.WriteString(w, fmt.Sprintf(`{"answers":{"goal":{"choice":%q,"confidence":0.95}}}`, billing.ID))
			return
		}
		io.WriteString(w, fmt.Sprintf(`{"answers":{"goal":{"choice":%q,"confidence":0.95}}}`, search))
	}))
	defer server.Close()
	config := fmt.Sprintf(`{"ledgerSorter": {"url": %q, "model": "d1-3b"}}`, server.URL)
	home, _ := os.UserHomeDir()
	if err := os.WriteFile(filepath.Join(home, ".agenthail", "config.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	d.sorter.http, d.sorter.retry = server.Client(), 50*time.Millisecond
	ref := ledger.PromptRef("both")
	d.queueSort(p.key, ref, recordIn(t, p, ref, "one message, two projects"), time.Now())
	d.queueSort(other.key, ref, recordIn(t, other, ref, "one message, two projects"), time.Now())
	runSorter(t, d)
	eventually(t, "the message to be filed in both projects", func() bool {
		return sortedGoal(t, p, ref) == search && sortedGoal(t, other, ref) == billing.ID
	})
}

func TestAMessageRecordedFirstButQueuedSecondIsNotAskedTwice(t *testing.T) {
	stub := &sorterStub{}
	d, _, _, p, _ := sorterFixture(t, stub)
	var mu sync.Mutex
	down := true
	stub.setAnswer(func(message string) (int, string) {
		mu.Lock()
		defer mu.Unlock()
		if message == "recorded first" && down {
			down = false
			return http.StatusServiceUnavailable, "down"
		}
		return http.StatusOK, `{"answers":{"goal":{"choice":"new","confidence":0.99}}}`
	})
	d.sorter.retry = 50 * time.Millisecond
	runSorter(t, d)
	first, second := ledger.PromptRef("first"), ledger.PromptRef("second")
	firstSeq := recordIn(t, p, first, "recorded first")
	secondSeq := recordIn(t, p, second, "recorded second")
	d.queueSort(p.key, second, secondSeq, time.Now())
	d.queueSort(p.key, first, firstSeq, time.Now())
	eventually(t, "the failed message to be asked again", func() bool {
		counts := map[string]int{}
		for _, a := range stub.asks() {
			counts[a.Message]++
		}
		return counts["recorded first"] == 2
	})
	eventually(t, "nothing left owed", func() bool { return len(d.sorter.owedJobs()) == 0 })
	counts := map[string]int{}
	for _, a := range stub.asks() {
		counts[a.Message]++
	}
	if counts["recorded second"] != 1 {
		t.Fatalf("asks per message = %v, want the message answered while healthy asked once", counts)
	}
}

func TestWhatIsOwedIsOnlyWhatFailed(t *testing.T) {
	stub := &sorterStub{}
	d, r, handler, p, _ := sorterFixture(t, stub)
	stub.setAnswer(func(message string) (int, string) {
		if message == "always failing" {
			return http.StatusServiceUnavailable, "down"
		}
		return http.StatusOK, `{"answers":{"goal":{"choice":"new","confidence":0.99}}}`
	})
	s := p.session(t, r, "claude-bounded")
	runSorter(t, d)
	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "failing", Prompt: "always failing"})
	for i := range 20 {
		ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: fmt.Sprintf("ok-%d", i), Prompt: fmt.Sprintf("answered %d", i)})
	}
	eventually(t, "every message to be asked", func() bool { return len(stub.asks()) >= 21 })
	eventually(t, "the worker to settle", func() bool { return len(d.sorter.jobs) == 0 })
	if owed := d.sorter.owedJobs(); len(owed) != 1 || owed[0].utteranceID != ledger.PromptRef("failing") {
		t.Fatalf("owed = %+v, want only the message that failed", owed)
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
