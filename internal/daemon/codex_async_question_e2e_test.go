package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface/codexsurface"
	"github.com/zm2231/agenthail/internal/testkit"
)

const (
	recordedQuestionID    = "call_4cce0c11ea09430dbc982c98f3887520"
	recordedQuestionTitle = "How should the CLI display notes with no title? I recommend using the input filename so you can identify each note."
	recordedOptionOne     = "Use the input filename (recommended)."
)

// recordedQuestionItem is the item codex-cli 0.169.0 recorded when the model
// called request_user_input_async in a trial, as the app-server lists it: an
// agent message delivered "async" that carries the question, under the tool
// call's id. The tool itself returned {"accepted":true} at once.
func recordedQuestionItem() map[string]any {
	return map[string]any{
		"type": "agentMessage", "id": recordedQuestionID, "phase": "final_answer", "memoryCitation": nil, "delivery": "async",
		"text":      recordedQuestionTitle + "\n- " + recordedOptionOne + "\n- Use “Untitled” for every note without a title.",
		"questions": []any{map[string]any{"title": recordedQuestionTitle, "options": []any{recordedOptionOne, "Use “Untitled” for every note without a title."}}},
	}
}

// A title Codex echoes clipped to 512 bytes on one line, longer than a card shows.
var longQuestionTitle = "What should the migration be called?\n" + strings.Repeat("ß", 300)

const twoQuestionDatabase = "Which database should the service use?"

func twoQuestionItem(itemID string) map[string]any {
	return map[string]any{
		"type": "agentMessage", "id": itemID, "phase": "final_answer", "delivery": "async", "text": twoQuestionDatabase,
		"questions": []any{
			map[string]any{"title": twoQuestionDatabase, "options": []any{"Postgres", "Keep both"}},
			map[string]any{"title": longQuestionTitle},
		},
	}
}

func userMessageItem(id, text string) map[string]any {
	return map[string]any{"type": "userMessage", "id": id, "content": []any{map[string]any{"type": "text", "text": text}}}
}

type codexAnswer struct {
	index            int
	question, answer string
}

func codexReplyFor(itemID string, answers ...codexAnswer) string {
	var entries []string
	for _, answer := range answers {
		var entry bytes.Buffer
		encoder := json.NewEncoder(&entry)
		encoder.SetEscapeHTML(false)
		_ = encoder.Encode(map[string]string{"answer": answer.answer, "question": answer.question, "questionItemId": fmt.Sprintf(`["request_user_input_async","%s",%d]`, itemID, answer.index)})
		entries = append(entries, strings.TrimSuffix(entry.String(), "\n"))
	}
	return "<send_user_message_question_reply>\n[" + strings.Join(entries, ",") + "]\n</send_user_message_question_reply>"
}

// codexThreadItems answers thread/items/list for one thread with the items a
// test set, newest first, as the app-server lists them.
type codexThreadItems struct {
	mu       sync.Mutex
	threadID string
	entries  []any
}

type threadItem struct {
	turn string
	item map[string]any
}

func inTurn(turn string, item map[string]any) threadItem { return threadItem{turn: turn, item: item} }

func (i *codexThreadItems) set(threadID string, newestFirst ...threadItem) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.threadID, i.entries = threadID, nil
	for _, entry := range newestFirst {
		i.entries = append(i.entries, map[string]any{"turnId": entry.turn, "item": entry.item})
	}
}

func (i *codexThreadItems) respond(method string, params map[string]any) map[string]any {
	if method != "thread/items/list" || params["turnId"] != nil {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if params["threadId"] != i.threadID {
		return map[string]any{"data": []any{}}
	}
	return map[string]any{"data": append([]any(nil), i.entries...)}
}

type questionDecision struct {
	SessionID  string `json:"sessionId"`
	Answerable bool   `json:"answerable"`
	Requests   []struct {
		ID        string `json:"id"`
		Questions []struct {
			Question string `json:"question"`
			Options  []struct {
				ID    string `json:"id"`
				Label string `json:"label"`
			} `json:"options"`
		} `json:"questions"`
	} `json:"requests"`
}

func (h *runningDaemon) questionDecisions(sessionID string) []questionDecision {
	status, payload := h.request(http.MethodGet, "/api/v1/decisions", nil)
	var page struct {
		Decisions []questionDecision `json:"decisions"`
	}
	if status != http.StatusOK || json.Unmarshal(payload, &page) != nil {
		return nil
	}
	var mine []questionDecision
	for _, item := range page.Decisions {
		if item.SessionID == sessionID {
			mine = append(mine, item)
		}
	}
	return mine
}

func (h *runningDaemon) awaitQuestionDecision(t *testing.T, sessionID string) questionDecision {
	t.Helper()
	var card questionDecision
	testkit.Eventually(t, "the question to be an answerable decision", 10*time.Second, func() bool {
		found := h.questionDecisions(sessionID)
		if len(found) != 1 || !found[0].Answerable || len(found[0].Requests) != 1 {
			return false
		}
		card = found[0]
		return true
	})
	return card
}

type codexQuestionHarness struct {
	h         *runningDaemon
	lifecycle *codextest.Lifecycle
	items     *codexThreadItems
	store     *registry.Registry
}

func newCodexQuestionHarness(t *testing.T) codexQuestionHarness {
	t.Helper()
	items := &codexThreadItems{}
	lifecycle := codextest.InstallLifecycle(t, codextest.Home(t), items.respond)
	store, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	h, _ := runDaemon(t, store, codexsurface.NewCodex(unreachableDesktop))
	return codexQuestionHarness{h: h, lifecycle: lifecycle, items: items, store: store}
}

func (q codexQuestionHarness) create(t *testing.T, message string) string {
	t.Helper()
	return q.createBy(t, "", message)
}

func (q codexQuestionHarness) createBy(t *testing.T, creator, message string) string {
	t.Helper()
	body := map[string]any{"action": "session-create", "surface": "codex", "message": message, "cwd": t.TempDir()}
	if creator != "" {
		body["sourceSessionId"] = creator
	}
	status, payload := q.h.request(http.MethodPost, "/api/v1/actions", body)
	var created struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.Unmarshal(payload, &created); status != http.StatusCreated || err != nil || created.Session.ID == "" {
		t.Fatalf("create status=%d body=%s err=%v", status, payload, err)
	}
	return created.Session.ID
}

func (q codexQuestionHarness) inputs(method, threadID string) []string {
	var texts []string
	for _, call := range q.lifecycle.Daemon().Calls(method) {
		if call["threadId"] != threadID {
			continue
		}
		input, _ := call["input"].([]any)
		first, _ := input[0].(map[string]any)
		text, _ := first["text"].(string)
		texts = append(texts, text)
	}
	return texts
}

func (q codexQuestionHarness) respond(t *testing.T, sessionID string, body map[string]any) {
	t.Helper()
	body["action"], body["sessionId"] = "respond", sessionID
	if status, payload := q.h.request(http.MethodPost, "/api/v1/actions", body); status != http.StatusOK {
		t.Fatalf("respond status=%d body=%s", status, payload)
	}
}

func TestACodexAsyncQuestionWaitsOnThePersonAndItsAnswerReachesCodexAsAReply(t *testing.T) {
	q := newCodexQuestionHarness(t)
	id := q.create(t, "Plan the notes command")
	q.items.set(id, inTurn("turn-1", recordedQuestionItem()))
	q.lifecycle.Daemon().CompleteTurn(id, recordedQuestionTitle)

	card := q.h.awaitQuestionDecision(t, id)
	request := card.Requests[0]
	if askRef(q.store, request.ID) != id+":"+recordedQuestionID || len(request.Questions) != 1 || request.Questions[0].Question != recordedQuestionTitle ||
		len(request.Questions[0].Options) != 2 || request.Questions[0].Options[0].Label != recordedOptionOne {
		t.Fatalf("decision %+v", card)
	}
	testkit.Eventually(t, "the row to wait on the person", 10*time.Second, func() bool {
		row := q.h.catalogSession(id)
		return row != nil && row.Ask != nil && row.Ask.ID == request.ID && row.TurnState != nil && row.TurnState.State == turnWaitingPerson &&
			row.Display != nil && row.Display.Kind == "waiting" && row.Display.Label == "Waiting on you"
	})

	starts := len(q.inputs("turn/start", id))
	q.respond(t, id, map[string]any{"requestId": request.ID, "decision": map[string]any{"choices": []string{request.Questions[0].Options[0].ID}}})
	sent := q.inputs("turn/start", id)[starts:]
	if want := codexReplyFor(recordedQuestionID, codexAnswer{0, recordedQuestionTitle, recordedOptionOne}); len(sent) != 1 || sent[0] != want {
		t.Fatalf("Codex was sent %q, want the reply %q", sent, want)
	}
	testkit.Eventually(t, "the decision to leave the feed", 10*time.Second, func() bool { return len(q.h.questionDecisions(id)) == 0 })
}

func TestACodexQuestionIsSettledByTheTurnThePersonStartsAfterItButNotByASteer(t *testing.T) {
	q := newCodexQuestionHarness(t)
	id := q.create(t, "Plan the notes command")
	asked := inTurn("turn-1", recordedQuestionItem())
	q.items.set(id, asked)
	q.lifecycle.Daemon().CompleteTurn(id, recordedQuestionTitle)
	askID := q.h.awaitQuestionDecision(t, id).Requests[0].ID

	q.items.set(id, inTurn("turn-2", userMessageItem("notice", "[agenthail] Your message to @docs was delivered.")), inTurn("turn-1", userMessageItem("steer", "Also keep the README short.")), asked)
	time.Sleep(300 * time.Millisecond)
	if ask, _, _ := q.store.Ask(askID); ask.State != registry.AskOpen {
		t.Fatalf("a steer into the asking turn or an Agenthail notice settled the person's question: %s", ask.State)
	}

	q.items.set(id, inTurn("turn-3", userMessageItem("typed", "Option 1: use the filename, as you recommended.")), inTurn("turn-1", userMessageItem("steer", "Also keep the README short.")), asked)
	testkit.Eventually(t, "the question the person answered in Codex to leave Needs you", 10*time.Second, func() bool {
		ask, found, err := q.store.Ask(askID)
		if err != nil || !found || ask.State != registry.AskWithdrawn || len(q.h.questionDecisions(id)) != 0 {
			return false
		}
		row := q.h.catalogSession(id)
		return row != nil && row.Display != nil && row.Display.Kind != "waiting"
	})
}

func TestACodexQuestionPastTheItemsReadIsSettledByATurnThePersonStartedSince(t *testing.T) {
	q := newCodexQuestionHarness(t)
	id := q.create(t, "Plan the notes command")
	q.items.set(id, inTurn("turn-1", recordedQuestionItem()))
	q.lifecycle.Daemon().CompleteTurn(id, recordedQuestionTitle)
	askID := q.h.awaitQuestionDecision(t, id).Requests[0].ID

	work := []threadItem{inTurn("turn-1", userMessageItem("steer", "Keep going."))}
	for i := len(work); i < 100; i++ {
		work = append(work, inTurn("turn-1", map[string]any{"type": "agentMessage", "id": fmt.Sprintf("work-%d", i), "text": "Working."}))
	}
	q.items.set(id, work...)
	time.Sleep(300 * time.Millisecond)
	if ask, _, _ := q.store.Ask(askID); ask.State != registry.AskOpen {
		t.Fatalf("a steer in the turn that scrolled the question out settled it: %s", ask.State)
	}

	q.items.set(id, append([]threadItem{inTurn("turn-2", userMessageItem("typed", "Use the filename."))}, work[:99]...)...)
	testkit.Eventually(t, "the question older than the items read to leave Needs you", 10*time.Second, func() bool {
		ask, found, err := q.store.Ask(askID)
		return err == nil && found && ask.State == registry.AskWithdrawn
	})
}

func TestACodexQuestionAnsweredInPartInCodexStaysOpenUntilEveryQuestionIs(t *testing.T) {
	q := newCodexQuestionHarness(t)
	id := q.create(t, "Plan the storage move")
	q.items.set(id, inTurn("turn-1", twoQuestionItem("call_q2")))
	q.lifecycle.Daemon().CompleteTurn(id, twoQuestionDatabase)
	askID := q.h.awaitQuestionDecision(t, id).Requests[0].ID

	first := codexReplyFor("call_q2", codexAnswer{0, twoQuestionDatabase, "Postgres"})
	unread := []string{
		strings.Replace(codexReplyFor("call_q2", codexAnswer{1, "x", "y"}), `\"call_q2\",1]`, `\"call_q2\",1.0]`, 1),
		strings.Replace(codexReplyFor("call_q2", codexAnswer{1, "x", "y"}), `"answer":"y",`, "", 1),
	}
	q.items.set(id, inTurn("turn-1", userMessageItem("unread-1", unread[1])), inTurn("turn-1", userMessageItem("unread-0", unread[0])), inTurn("turn-1", userMessageItem("reply-0", first)), inTurn("turn-1", twoQuestionItem("call_q2")))
	time.Sleep(300 * time.Millisecond)
	if ask, _, _ := q.store.Ask(askID); ask.State != registry.AskOpen {
		t.Fatalf("an answer to the first question and replies Codex does not read settled both: %s", ask.State)
	}

	second := codexReplyFor("call_q2", codexAnswer{1, "What should the migration be called?", "add_notes_title"})
	q.items.set(id, inTurn("turn-1", userMessageItem("reply-1", second)), inTurn("turn-1", userMessageItem("reply-0", first)), inTurn("turn-1", twoQuestionItem("call_q2")))
	testkit.Eventually(t, "both answers to settle the question", 10*time.Second, func() bool {
		ask, found, err := q.store.Ask(askID)
		return err == nil && found && ask.State == registry.AskWithdrawn
	})
}

func TestAnswersToCodexQuestionsAskedMidTurnSteerTheRunningTurnInOneReply(t *testing.T) {
	q := newCodexQuestionHarness(t)
	id := q.create(t, "Plan the storage move")
	q.items.set(id, inTurn("turn-1", twoQuestionItem("call_q2")))
	q.lifecycle.Daemon().Notify("thread/status/changed", map[string]any{"threadId": id, "status": map[string]any{"type": "active", "activeFlags": []any{}}})
	request := q.h.awaitQuestionDecision(t, id).Requests[0]
	if len(request.Questions) != 2 || request.Questions[1].Question == longQuestionTitle {
		t.Fatalf("the ask shows %+v, want both questions with the long one cut to fit", request.Questions)
	}

	starts := len(q.inputs("turn/start", id))
	q.respond(t, id, map[string]any{"requestId": request.ID, "decision": map[string]any{
		"choices": []string{request.Questions[0].Options[1].ID},
		"answers": map[string]string{request.Questions[1].Question: "add_notes_title"},
		"message": "Keep the old table until Friday.",
	}})
	steers := q.inputs("turn/steer", id)
	echoed := "What should the migration be called? " + strings.Repeat("ß", 237)
	want := codexReplyFor("call_q2", codexAnswer{0, twoQuestionDatabase, "Keep both"}, codexAnswer{1, echoed, "add_notes_title\n\nKeep the old table until Friday."})
	if len(steers) != 1 || len(q.inputs("turn/start", id)) != starts || steers[0] != want {
		t.Fatalf("the running turn was steered with %q (and %d new turns), want %q", steers, len(q.inputs("turn/start", id))-starts, want)
	}
}

func TestALeadsAnswerToAWorkersCodexQuestionReachesCodexAsAPlainReply(t *testing.T) {
	q := newCodexQuestionHarness(t)
	lead := q.create(t, "Lead the notes work")
	worker := q.createBy(t, lead, "Plan the notes command")
	q.items.set(worker, inTurn("turn-1", recordedQuestionItem()))
	q.lifecycle.Daemon().CompleteTurn(worker, recordedQuestionTitle)
	var askID string
	testkit.Eventually(t, "the worker's question to reach its lead", 10*time.Second, func() bool {
		asks, err := q.store.SessionAsks(worker)
		if err != nil || len(asks) != 1 || asks[0].RoutedTo != lead {
			return false
		}
		askID = asks[0].ID
		return true
	})

	starts := len(q.inputs("turn/start", worker))
	q.respond(t, worker, map[string]any{"requestId": askID, "sourceSessionId": lead, "decision": map[string]any{"answers": map[string]string{recordedQuestionTitle: recordedOptionOne}}})
	if ask, _, _ := q.store.Ask(askID); ask.State != registry.AskAnswered || ask.AnsweredBy != lead {
		t.Fatalf("ask %+v, want answered by the lead", ask)
	}
	sent := q.inputs("turn/start", worker)[starts:]
	if want := codexReplyFor(recordedQuestionID, codexAnswer{0, recordedQuestionTitle, recordedOptionOne}); len(sent) != 1 || sent[0] != want {
		t.Fatalf("Codex was sent %q, want the reply alone %q", sent, want)
	}
}
