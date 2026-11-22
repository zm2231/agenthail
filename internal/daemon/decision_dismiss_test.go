package daemon

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

var inboxPerson = principal{ID: localDashboardPrincipal, Kind: principalPerson}

func dismiss(t *testing.T, d *Daemon, p principal, request dashboardActionRequest) actionReply {
	t.Helper()
	request.Action = dismissDecisionAction
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return d.performAction(t.Context(), p, actionFormatAPIv1, body, "")
}

func mustDismiss(t *testing.T, d *Daemon, request dashboardActionRequest) {
	t.Helper()
	reply := dismiss(t, d, inboxPerson, request)
	if reply.Status != http.StatusOK || !strings.Contains(string(reply.Body), `"outcome":"dismissed"`) {
		t.Fatalf("dismiss %+v: status=%d body=%s", request, reply.Status, reply.Body)
	}
}

func notesTo(t *testing.T, r *registry.Registry, sessionID, text string) []string {
	t.Helper()
	rows, err := r.ListQueue(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	var notes []string
	for _, row := range rows {
		if row.SessionID == sessionID && strings.Contains(row.Message, text) {
			notes = append(notes, row.Message)
		}
	}
	return notes
}

func stateOfAsk(t *testing.T, r *registry.Registry, id string) string {
	t.Helper()
	ask, found, err := r.Ask(id)
	if err != nil || !found {
		t.Fatalf("ask %s found=%v err=%v", id, found, err)
	}
	return ask.State
}

func TestOnlyThePersonDismissesADecision(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	ask, err := r.RaiseAsk(registry.Ask{SessionID: "from", Origin: registry.AskOriginAgent, Title: "Ship it?", RoutedTo: registry.AskToPerson,
		Questions: []registry.AskQuestion{{Question: "Ship it?", Options: []registry.AskOption{{ID: "o0", Label: "Yes"}}}}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	request := dashboardActionRequest{SessionID: "from", RequestID: ask.ID}
	for name, p := range map[string]principal{"a session": sessionPrincipal("to"), "a session through a paired host": {Kind: principalHost, HostName: "laptop", Claimed: "session to"}} {
		reply := dismiss(t, d, p, request)
		if reply.Status != http.StatusForbidden || !strings.Contains(string(reply.Body), "only the person dismisses a decision") {
			t.Fatalf("%s dismissed: status=%d body=%s", name, reply.Status, reply.Body)
		}
		if result, err := d.doDismissDecision(t.Context(), p, request); err == nil {
			t.Fatalf("%s dismissed past routing: %+v", name, result)
		}
	}
	if state := stateOfAsk(t, r, ask.ID); state != registry.AskOpen {
		t.Fatalf("a refused dismissal left the ask %s", state)
	}
}

func TestDismissingAnAskWithdrawsItAndTellsTheAskerOnce(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	ask, err := r.RaiseAsk(registry.Ask{SessionID: "from", Origin: registry.AskOriginAgent, Title: "Ship it?", RoutedTo: registry.AskToPerson,
		Questions: []registry.AskQuestion{{Question: "Ship it?", Options: []registry.AskOption{{ID: "o0", Label: "Yes"}}}}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	mustDismiss(t, d, dashboardActionRequest{SessionID: "from", RequestID: ask.ID})
	if state := stateOfAsk(t, r, ask.ID); state != registry.AskWithdrawn {
		t.Fatalf("ask state=%s", state)
	}
	if notes := notesTo(t, r, "from", "dismissed"); len(notes) != 1 || notes[0] != `[agenthail] The person dismissed your ask "Ship it?" without answering.` {
		t.Fatalf("notes to the asker = %q", notes)
	}
	if again := dismiss(t, d, inboxPerson, dashboardActionRequest{SessionID: "from", RequestID: ask.ID}); again.Status != http.StatusConflict || !strings.Contains(string(again.Body), "stale") {
		t.Fatalf("a settled ask dismissed again: status=%d body=%s", again.Status, again.Body)
	}
	if notes := notesTo(t, r, "from", "dismissed"); len(notes) != 1 {
		t.Fatalf("notes after a repeat = %q", notes)
	}
}

func TestDismissingAnUndeliveredMessageAnswersItsDismissChoice(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	queueID, _, err := r.QueueDeliveryWithIntent("to", "never arrives", "", surface.SendOptions{SourceSessionID: surface.PersonSessionID})
	if err != nil {
		t.Fatal(err)
	}
	item, err := r.ClaimNextMessage("to", time.Now())
	if err != nil || item == nil || item.ID != queueID {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	if err := r.DeadLetterMessage(item.ID, errors.New("delivery rejected [target missing]: gone")); err != nil {
		t.Fatal(err)
	}
	asks := openDeliveryAsks(t, r)
	if len(asks) != 1 || !offersDeliveryDismiss(asks[0]) {
		t.Fatalf("asks=%+v", asks)
	}
	mustDismiss(t, d, dashboardActionRequest{SessionID: "to", RequestID: asks[0].ID})
	settled, _, err := r.Ask(asks[0].ID)
	if err != nil || settled.State != registry.AskAnswered || settled.Answer.Answers[settled.Questions[0].Question] != "Dismiss" {
		t.Fatalf("ask=%+v err=%v, want it answered with Dismiss", settled, err)
	}
	if notes := notesTo(t, r, "to", "dismissed"); len(notes) != 0 {
		t.Fatalf("a delivery dismissal sent notes %q", notes)
	}
}

func TestDismissingAProviderRequestDeniesItForTheAgent(t *testing.T) {
	f := newMCPAnswerFixture(t)
	f.waitOnPrompt(t, "worker")
	askID := askIDOfRequest(t, f.r, "worker", "worker-prompt")
	mustDismiss(t, f.d, dashboardActionRequest{SessionID: "worker", RequestID: askID})
	f.surface.mu.Lock()
	decision, answered := f.surface.answered["worker/worker-prompt"]
	f.surface.mu.Unlock()
	if !answered || decision.Behavior != "deny" || decision.Message != "The person dismissed this without answering" {
		t.Fatalf("the provider got %+v answered=%v", decision, answered)
	}
	if state := stateOfAsk(t, f.r, askID); state != registry.AskAnswered {
		t.Fatalf("ask state=%s", state)
	}
}

func TestDismissingAParkedCardParksItAsLaterAndTellsItsParker(t *testing.T) {
	mac, _, _ := ledgerOnTwoHosts(t)
	ask := personAsk(t, mac)
	id := parkedWork(t, mac, "search bodies", ledger.Park{Kind: ledger.ParkPerson, Person: &ledger.ParkQuestion{Question: "titles only, or bodies too?", AskID: ask}})
	bare := parkedWork(t, mac, "search titles", ledger.Park{Kind: ledger.ParkPerson, Person: &ledger.ParkQuestion{Question: "which index?"}})
	if parked := parkedInNeedsYou(t, mac); len(parked) != 1 || parked[bare].WorkItem != bare {
		t.Fatalf("Needs you before = %+v", parked)
	}
	before, err := mac.daemon.ownDecisionsRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	mustDismiss(t, mac.daemon, dashboardActionRequest{SessionID: mac.daemon.publicSessionID("local-a"), WorkItem: id})
	mustDismiss(t, mac.daemon, dashboardActionRequest{SessionID: mac.daemon.publicSessionID("local-a"), WorkItem: bare})
	board, err := mac.daemon.ledgerStore().Board(widgetKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, each := range []string{id, bare} {
		item, ok := board.WorkItem(each)
		if !ok || item.State != ledger.WorkParked || item.Park == nil || item.Park.Kind != ledger.ParkLater || item.Park.Why != "dismissed by the person" || item.Park.Session != "local-a" {
			t.Fatalf("%s after dismissal = %+v park=%+v", each, item, item.Park)
		}
		if last := item.Timeline[len(item.Timeline)-1]; last.Actor != "local-a" {
			t.Fatalf("%s re-park attributed to %q", each, last.Actor)
		}
	}
	if state := stateOfAsk(t, mac.reg, ask); state != registry.AskWithdrawn {
		t.Fatalf("the park's ask is %s", state)
	}
	want := map[string]bool{
		"[agenthail] The person dismissed " + id + " (titles only, or bodies too?) without answering. It is parked as later.": true,
		"[agenthail] The person dismissed " + bare + " (which index?) without answering. It is parked as later.":              true,
	}
	notes := notesTo(t, mac.reg, "local-a", "The person dismissed")
	if len(notes) != 2 || !want[notes[0]] || !want[notes[1]] || notes[0] == notes[1] {
		t.Fatalf("notes to the parker = %q", notes)
	}
	if parked := parkedInNeedsYou(t, mac); len(parked) != 0 {
		t.Fatalf("Needs you after = %+v", parked)
	}
	after, err := mac.daemon.ownDecisionsRevision(t.Context())
	if err != nil || after == before {
		t.Fatalf("decisions revision %s -> %s err=%v", before, after, err)
	}
	if again := dismiss(t, mac.daemon, inboxPerson, dashboardActionRequest{SessionID: mac.daemon.publicSessionID("local-a"), WorkItem: bare}); again.Status != http.StatusNotFound {
		t.Fatalf("a dismissed item dismissed again: status=%d body=%s", again.Status, again.Body)
	}
	if missing := dismiss(t, mac.daemon, inboxPerson, dashboardActionRequest{SessionID: mac.daemon.publicSessionID("local-a"), WorkItem: "widget-W999"}); missing.Status != http.StatusNotFound {
		t.Fatalf("an unknown item: status=%d body=%s", missing.Status, missing.Body)
	}
}

func TestAnAskOnAPairedHostIsDismissedOnThatHost(t *testing.T) {
	local, remote, record := pairedFederation(t)
	ask, err := remote.reg.RaiseAsk(registry.Ask{SessionID: "remote-a", Origin: registry.AskOriginAgent, Title: "Deploy it now?", RoutedTo: registry.AskToPerson,
		Questions: []registry.AskQuestion{{Question: "Deploy it now?", Options: []registry.AskOption{{ID: "o0", Label: "Deploy"}}}}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	response := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": dismissDecisionAction, "sessionId": hosts.Qualify(record.ID, "remote-a"), "requestId": ask.ID})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"outcome":"dismissed"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if state := stateOfAsk(t, remote.reg, ask.ID); state != registry.AskWithdrawn {
		t.Fatalf("the paired host's ask is %s", state)
	}
	if notes := notesTo(t, remote.reg, "remote-a", "dismissed your ask"); len(notes) != 1 {
		t.Fatalf("notes on the paired host = %q", notes)
	}
}

func stoppedCards(t *testing.T, d *Daemon) map[string]decisionItem {
	t.Helper()
	page, response := readDecisions(t, d, "")
	if response.Code != http.StatusOK {
		t.Fatalf("decisions status=%d body=%s", response.Code, response.Body)
	}
	cards := map[string]decisionItem{}
	for _, item := range page.Decisions {
		if item.Group == decisionStopped && !item.Answerable && item.WorkItem == "" {
			cards[item.SessionID] = item
		}
	}
	return cards
}

func (f *mcpAnswerFixture) stopTurn(t *testing.T, id, turn, reason string) {
	t.Helper()
	session := f.surface.sessions[id]
	f.surface.observations[id] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: turn}
	f.d.observeSession(t.Context(), f.surface, &session)
	stop := &surface.TurnStop{TurnID: turn, At: time.Now(), Reason: reason}
	f.surface.observations[id] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: turn, Reply: &surface.ReplyResult{Done: true, Error: "failed"}, TurnStop: stop}
	f.d.observeSession(t.Context(), f.surface, &session)
	session.TurnStop = stop
	f.surface.sessions[id] = session
	testkit.Eventually(t, "the stopped card for "+turn, 5*time.Second, func() bool {
		f.d.discoverCatalog(t.Context())
		card, listed := stoppedCards(t, f.d)[id]
		return listed && strings.Contains(card.Reason, reason)
	})
}

func TestADismissedStoppedCardStaysHiddenUntilItsStateChanges(t *testing.T) {
	f := newMCPAnswerFixture(t)
	f.stopTurn(t, "worker", "turn-1", "Claude API error: overloaded")
	if none := dismiss(t, f.d, inboxPerson, dashboardActionRequest{SessionID: "lead"}); none.Status != http.StatusConflict {
		t.Fatalf("a session with no stopped card: status=%d body=%s", none.Status, none.Body)
	}
	before, err := f.d.ownDecisionsRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	mustDismiss(t, f.d, dashboardActionRequest{SessionID: "worker"})
	if card, listed := stoppedCards(t, f.d)["worker"]; listed {
		t.Fatalf("the dismissed card is still listed: %+v", card)
	}
	if after, err := f.d.ownDecisionsRevision(t.Context()); err != nil || after == before {
		t.Fatalf("decisions revision %s -> %s err=%v", before, after, err)
	}
	if notes := notesTo(t, f.r, "worker", "dismissed"); len(notes) != 0 {
		t.Fatalf("a stopped card's dismissal sent notes %q", notes)
	}
	if again := dismiss(t, f.d, inboxPerson, dashboardActionRequest{SessionID: "worker"}); again.Status != http.StatusOK {
		t.Fatalf("dismissing the same state twice: status=%d body=%s", again.Status, again.Body)
	}
	f.stopTurn(t, "worker", "turn-2", "Codex error: stream disconnected")
}

func TestBuildDecisionsSkipsAStoppedCardOnlyWhileItsDismissedStateHolds(t *testing.T) {
	row := decisionRow("s1", "stopped", 3)
	key := func(row dashboardSession) string {
		return decisionStateKey(decisionItem{SessionID: row.ID, Kind: row.Attention.Kind, Reason: row.Attention.Reason, Since: decisionSince(row)})
	}
	page := func(dismissed map[string]string) decisionsPage {
		return buildDecisions(decisionSources{rows: []dashboardSession{row}, hearer: func(dashboardSession) (decisionHearer, bool) { return decisionHearer{}, false }, dismissed: dismissed})
	}
	if got := page(map[string]string{"s1": key(row)}); len(got.Decisions) != 0 {
		t.Fatalf("dismissed state listed: %+v", got.Decisions)
	}
	moved := row
	moved.LastActive = moved.LastActive.Add(time.Minute)
	if got := page(map[string]string{"s1": key(moved)}); len(got.Decisions) != 1 {
		t.Fatalf("a changed state stayed hidden: %+v", got.Decisions)
	}
}

func TestAParkedCardIsDismissedOnTheComputerWhoseLedgerReportedIt(t *testing.T) {
	local, remote, record := pairedFederation(t)
	store := remote.daemon.ledgerStore()
	if err := store.Enable(widgetKey, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	added, err := store.Work(ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkAdd, Text: "search bodies"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	park := ledger.Park{Kind: ledger.ParkPerson, Person: &ledger.ParkQuestion{Question: "titles only?"}}
	if _, err := store.Work(ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkPark, ItemID: added.Item.ID, Park: &park}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if parked := parkedInNeedsYou(t, remote); parked[added.Item.ID].WorkItem != added.Item.ID {
		t.Fatalf("the paired host does not report the card: %+v", parked)
	}
	body := map[string]string{"action": dismissDecisionAction, "sessionId": "local-a", "workItem": added.Item.ID}
	if bySession := local.request(t, http.MethodPost, "/api/v1/actions", body); bySession.Code != http.StatusNotFound {
		t.Fatalf("without a host the parker's computer answered %d: %s", bySession.Code, bySession.Body)
	}
	body["host"] = record.ID
	if reported := local.request(t, http.MethodPost, "/api/v1/actions", body); reported.Code != http.StatusOK || !strings.Contains(reported.Body.String(), `"outcome":"dismissed"`) {
		t.Fatalf("dismiss through the reporting host: status=%d body=%s", reported.Code, reported.Body)
	}
	board, err := store.Board(widgetKey)
	if err != nil {
		t.Fatal(err)
	}
	if item, ok := board.WorkItem(added.Item.ID); !ok || item.Park == nil || item.Park.Kind != ledger.ParkLater || item.Park.Session != "local-a" {
		t.Fatalf("the paired host's item = %+v park=%+v", item, item.Park)
	}
	if parked := parkedInNeedsYou(t, remote); len(parked) != 0 {
		t.Fatalf("the paired host still reports %+v", parked)
	}
	body["host"] = ""
	if here := local.request(t, http.MethodPost, "/api/v1/actions", body); here.Code != http.StatusNotFound {
		t.Fatalf("host \"\" did not stay on this computer: status=%d body=%s", here.Code, here.Body)
	}
}

func openLedgerAsk(t *testing.T, r *registry.Registry, questionID string) (registry.Ask, bool) {
	t.Helper()
	open, err := r.OpenAsks()
	if err != nil {
		t.Fatal(err)
	}
	for _, ask := range open {
		if ask.Origin == registry.AskOriginLedger && ask.Ref == registry.LedgerAskRef(widgetKey, questionID) {
			return ask, true
		}
	}
	return registry.Ask{}, false
}

func askLedgerQuestion(t *testing.T, node *federationNode, text string) string {
	t.Helper()
	q, err := node.daemon.ledgerStore().AskQuestion(ledger.QuestionRequest{ProjectKey: widgetKey, SessionID: "local-a", To: ledger.ProjectTarget, Text: text, Recommendation: "yes", DefaultAnswer: "no", Kind: ledger.QuestionScope, DeadlineMs: time.Now().Add(time.Hour).UnixMilli()}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	node.daemon.syncLedgerAsks(widgetKey)
	if _, open := openLedgerAsk(t, node.reg, q.ID); !open {
		t.Fatalf("%s raised no ask", q.ID)
	}
	return q.ID
}

func TestADismissedLedgerQuestionIsNotAskedAgainButANewOneIs(t *testing.T) {
	mac, _, _ := ledgerOnTwoHosts(t)
	dismissed := askLedgerQuestion(t, mac, "index bodies too?")
	ask, _ := openLedgerAsk(t, mac.reg, dismissed)
	mustDismiss(t, mac.daemon, dashboardActionRequest{SessionID: "local-a", RequestID: ask.ID})
	for range 2 {
		mac.daemon.syncLedgerAsks(widgetKey)
		mac.daemon.syncAllLedgerAsks()
	}
	if again, open := openLedgerAsk(t, mac.reg, dismissed); open {
		t.Fatalf("the dismissed question was asked again: %+v", again)
	}
	page, _ := readDecisions(t, mac.daemon, "")
	for _, item := range page.Decisions {
		for _, request := range item.Requests {
			if strings.Contains(request.Prompt, "index bodies too?") || request.ID == ask.ID {
				t.Fatalf("the dismissed question is back in Needs you: %+v", item)
			}
		}
	}
	if notes := notesTo(t, mac.reg, "local-a", "The person dismissed your ask"); len(notes) != 1 {
		t.Fatalf("notes to the asker = %q", notes)
	}

	withdrawn := askLedgerQuestion(t, mac, "sort by date first?")
	own, _ := openLedgerAsk(t, mac.reg, withdrawn)
	if ok, err := mac.reg.WithdrawAsk(own.ID, time.Now()); err != nil || !ok {
		t.Fatalf("withdraw ok=%v err=%v", ok, err)
	}
	mac.daemon.syncLedgerAsks(widgetKey)
	if _, open := openLedgerAsk(t, mac.reg, withdrawn); !open {
		t.Fatal("a question whose ask was withdrawn by anyone but the person was not asked again")
	}
	askLedgerQuestion(t, mac, "add a search box?")
}

func TestDismissingAParksAskReparksItAsLaterWithOneNote(t *testing.T) {
	mac, _, _ := ledgerOnTwoHosts(t)
	ask := personAsk(t, mac)
	id := parkedWork(t, mac, "search bodies", ledger.Park{Kind: ledger.ParkPerson, Person: &ledger.ParkQuestion{Question: "titles only, or bodies too?", AskID: ask}})
	if parked := parkedInNeedsYou(t, mac); len(parked) != 0 {
		t.Fatalf("a park with an open ask is listed: %+v", parked)
	}
	mustDismiss(t, mac.daemon, dashboardActionRequest{SessionID: "local-a", RequestID: ask})
	board, err := mac.daemon.ledgerStore().Board(widgetKey)
	if err != nil {
		t.Fatal(err)
	}
	if item, ok := board.WorkItem(id); !ok || item.Park == nil || item.Park.Kind != ledger.ParkLater || item.Park.Why != "dismissed by the person" || item.Park.Session != "local-a" {
		t.Fatalf("%s after its ask was dismissed = %+v park=%+v", id, item, item.Park)
	}
	if state := stateOfAsk(t, mac.reg, ask); state != registry.AskWithdrawn {
		t.Fatalf("ask state=%s", state)
	}
	if parked := parkedInNeedsYou(t, mac); len(parked) != 0 {
		t.Fatalf("the parked card came back: %+v", parked)
	}
	notes := notesTo(t, mac.reg, "local-a", "The person dismissed")
	if len(notes) != 1 || notes[0] != "[agenthail] The person dismissed "+id+" (titles only, or bodies too?) without answering. It is parked as later." {
		t.Fatalf("notes to the parker = %q", notes)
	}
}

func TestAStoppedCardFromAFailedRequestReadCanBeDismissed(t *testing.T) {
	d, _, fake, from, _ := daemonFixture(t)
	waiting := &surface.Waiting{Kind: surface.WaitingApproval, ID: "from-prompt", Answerable: true, Since: time.Now()}
	from.Status, from.WaitingOn = surface.StatusBusy, waiting
	fake.sessions[from.ID] = from
	fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "t-from", WaitingOn: waiting}
	testkit.Eventually(t, "the failed read's stopped card", 5*time.Second, func() bool {
		d.scanAndRelay(t.Context())
		d.discoverCatalog(t.Context())
		card, listed := stoppedCards(t, d)[from.ID]
		return listed && card.RequestError != ""
	})
	mustDismiss(t, d, dashboardActionRequest{SessionID: from.ID})
	if card, listed := stoppedCards(t, d)[from.ID]; listed {
		t.Fatalf("the dismissed card is still listed: %+v", card)
	}
}

func TestDismissingAnApprovalDeniesItAndTellsTheAsker(t *testing.T) {
	f := newApprovalFixture(t)
	askID := f.askPerson(t, `{"action":"channel-create","channel":"dismissed"}`)
	mustDismiss(t, f.d, dashboardActionRequest{SessionID: f.worker.ID, RequestID: askID})
	approval, found, err := f.r.Approval(askID)
	if err != nil || !found || approval.Status != registry.ApprovalDenied {
		t.Fatalf("approval=%+v found=%v err=%v, want it denied", approval, found, err)
	}
	if exists, _ := f.r.ChannelExists("dismissed"); exists {
		t.Fatal("a dismissed approval ran")
	}
	if notes := notesTo(t, f.r, f.worker.ID, "[agenthail] The person d"); len(notes) != 1 || notes[0] != "[agenthail] The person denied: Create channel #dismissed (ask "+askID+")." {
		t.Fatalf("notes to the asker = %q, want only the denial", notes)
	}
}

func TestDismissingAnAskHeldHereForAParkInAnotherHostsLedgerReparksItThere(t *testing.T) {
	mac, owner, _ := ledgerOnTwoHosts(t)
	mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"}, nil)
	ask := personAsk(t, mac)
	id := parkedWork(t, mac, "search titles", ledger.Park{Kind: ledger.ParkPerson, Person: &ledger.ParkQuestion{Question: "titles only?", AskID: ask}})

	mustDismiss(t, mac.daemon, dashboardActionRequest{SessionID: "local-a", RequestID: ask})
	if state := stateOfAsk(t, mac.reg, ask); state != registry.AskWithdrawn {
		t.Fatalf("the held ask is %s", state)
	}
	board, err := owner.daemon.ledgerStore().Board(widgetKey)
	if err != nil {
		t.Fatal(err)
	}
	if item, ok := board.WorkItem(id); !ok || item.Park == nil || item.Park.Kind != ledger.ParkLater || item.Park.Why != "dismissed by the person" {
		t.Fatalf("the owner's %s = %+v park=%+v", id, item, item.Park)
	}
	mac.daemon.sweepLedgerBacklog(t.Context(), time.Now())
	owner.daemon.sweepLedgerBacklog(t.Context(), time.Now())
	for name, node := range map[string]*federationNode{"owner": owner, "parking host": mac} {
		if parked := parkedInNeedsYou(t, node); len(parked) != 0 {
			t.Fatalf("the %s still lists %+v", name, parked)
		}
	}
	notes := append(notesTo(t, mac.reg, "local-a", "The person dismissed"), notesTo(t, owner.reg, "local-a", "The person dismissed")...)
	if len(notes) != 1 || notes[0] != "[agenthail] The person dismissed "+id+" (titles only?) without answering. It is parked as later." {
		t.Fatalf("notes to the parker = %q", notes)
	}
}
