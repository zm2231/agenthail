package daemon

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func parkedWork(t *testing.T, node *federationNode, text string, park ledger.Park) string {
	t.Helper()
	var added ledger.WorkResult
	mustLedger(t, node, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkAdd, Text: text}, &added)
	mustLedger(t, node, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkPark, ItemID: added.Item.ID, Park: &park}, nil)
	return added.Item.ID
}

func personAsk(t *testing.T, node *federationNode) string {
	t.Helper()
	ask, err := node.reg.RaiseAsk(registry.Ask{SessionID: "local-a", Origin: registry.AskOriginAgent, Title: "Titles only?", RoutedTo: registry.AskToPerson,
		Questions: []registry.AskQuestion{{Question: "Titles only?", Options: []registry.AskOption{{ID: "o0", Label: "Titles"}, {ID: "o1", Label: "Bodies too"}}}}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return ask.ID
}

func parkedInNeedsYou(t *testing.T, node *federationNode) map[string]decisionItem {
	t.Helper()
	page, response := readDecisions(t, node.daemon, "")
	if response.Code != 200 {
		t.Fatalf("decisions status=%d body=%s", response.Code, response.Body)
	}
	parked := map[string]decisionItem{}
	for _, item := range page.Decisions {
		if item.Kind == decisionParked {
			parked[item.WorkItem] = item
		}
	}
	return parked
}

func backlogEntry(t *testing.T, node *federationNode, id string) map[string]any {
	t.Helper()
	result, err := node.daemon.ledgerRead(widgetKey, ledger.ReadRequest{View: "backlog"})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range result["backlog"].([]map[string]any) {
		if row["id"] == id {
			return row
		}
	}
	t.Fatalf("%s is not in the backlog: %+v", id, result["backlog"])
	return nil
}

func TestAPersonParkStaysInNeedsYouUntilItsAskIsAnsweredOrItIsUnparked(t *testing.T) {
	mac, _, _ := ledgerOnTwoHosts(t)
	question := func(ask string) ledger.Park {
		return ledger.Park{Kind: ledger.ParkPerson, Person: &ledger.ParkQuestion{Question: "titles only, or bodies too?", AskID: ask}}
	}
	open, answered, expired := personAsk(t, mac), personAsk(t, mac), personAsk(t, mac)
	bare := parkedWork(t, mac, "search titles", question(""))
	waiting := parkedWork(t, mac, "search bodies", question(open))
	settled := parkedWork(t, mac, "search tags", question(answered))
	lapsed := parkedWork(t, mac, "search notes", question(expired))
	if claimed, err := mac.reg.ClaimAsk(answered, registry.AskAnswer{}, surface.PersonSessionID, "test", time.Now()); err != nil || !claimed {
		t.Fatalf("answer %s: claimed=%v err=%v", answered, claimed, err)
	}
	if _, err := mac.reg.WriteDB().Exec(`UPDATE asks SET state=? WHERE id=?`, registry.AskExpired, expired); err != nil {
		t.Fatal(err)
	}

	parked := parkedInNeedsYou(t, mac)
	if len(parked) != 2 {
		t.Fatalf("Needs you lists %+v, want only %s (no ask) and %s (its ask expired)", parked, bare, lapsed)
	}
	for _, id := range []string{bare, lapsed} {
		item, listed := parked[id]
		if !listed || item.Group != decisionWaiting || item.Answerable || item.SessionID != mac.daemon.publicSessionID("local-a") || !strings.HasPrefix(item.Reason, id+" waits on you: titles only") || item.Since.IsZero() {
			t.Fatalf("%s in Needs you = %+v", id, item)
		}
	}
	if row := backlogEntry(t, mac, settled); row["ready"] != true || row["why"] != "the person answered "+answered {
		t.Fatalf("the answered item reads %+v", row)
	}
	for _, id := range []string{bare, waiting, lapsed} {
		if row := backlogEntry(t, mac, id); row["ready"] != false {
			t.Fatalf("%s reads ready while the person has not answered: %+v", id, row)
		}
	}

	mustLedger(t, mac, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkUnpark, ItemID: bare}, nil)
	if parked := parkedInNeedsYou(t, mac); len(parked) != 1 || parked[lapsed].WorkItem != lapsed {
		t.Fatalf("after unparking %s, Needs you lists %+v", bare, parked)
	}
}

func TestOnlyTheLedgersOwnerListsAPersonParkSoMergedPagesHoldItOnce(t *testing.T) {
	mac, owner, record := ledgerOnTwoHosts(t)
	mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"}, nil)
	id := parkedWork(t, mac, "search titles", ledger.Park{Kind: ledger.ParkPerson, Person: &ledger.ParkQuestion{Question: "titles only?"}})
	testkit.Eventually(t, "the parked item on both hosts", 10*time.Second, func() bool {
		for _, node := range []*federationNode{mac, owner} {
			board, err := node.daemon.ledgerStore().Board(widgetKey)
			if err != nil {
				return false
			}
			if item, ok := board.WorkItem(id); !ok || item.State != ledger.WorkParked {
				if host, ok := mac.daemon.pairedHost(record.ID); ok {
					mac.daemon.syncLedgerHost(t.Context(), host)
				}
				return false
			}
		}
		return true
	})
	if parked := parkedInNeedsYou(t, owner); len(parked) != 1 || parked[id].WorkItem != id {
		t.Fatalf("the owner's Needs you = %+v", parked)
	}
	if parked := parkedInNeedsYou(t, mac); len(parked) != 0 {
		t.Fatalf("a host holding a copy also listed it, so a merged page would hold it twice: %+v", parked)
	}
}

func TestTheCoordinatorIsToldOnceEachTimeABacklogItemBecomesReady(t *testing.T) {
	mac, _, _ := ledgerOnTwoHosts(t)
	revisit := time.Now().Add(time.Hour)
	id := parkedWork(t, mac, "after the release", ledger.Park{Kind: ledger.ParkLater, Why: "after the release", Revisit: revisit})
	lead := &mcpCaller{session: surface.Session{ID: "lead"}, project: &CatalogProject{ID: widgetKey, Key: widgetKey}}
	worker := &mcpCaller{session: surface.Session{ID: "local-a"}, project: lead.project}

	if line := mac.daemon.backlogReadyLine(t.Context(), lead, time.Now()); line != "" {
		t.Fatalf("told %q before the revisit date", line)
	}
	later := revisit.Add(time.Minute)
	if line := mac.daemon.backlogReadyLine(t.Context(), worker, later); line != "" {
		t.Fatalf("a session that is not the coordinator was told %q", line)
	}
	result, err := mac.daemon.mcpEndTurn(t.Context(), &mcpCaller{session: surface.Session{ID: "lead"}, project: lead.project}, mcpEndTurnInput{Headline: "done"})
	if err != nil {
		t.Fatal(err)
	}
	if line, _ := result.(map[string]any)["backlog"].(string); line != "" {
		t.Fatalf("the end of turn told %q before the revisit date", line)
	}
	if line := mac.daemon.backlogReadyLine(t.Context(), lead, later); line != "Backlog: "+id+" ready (later: its revisit date came), parked by local-a." {
		t.Fatalf("the ready line = %q", line)
	}
	if line := mac.daemon.backlogReadyLine(t.Context(), lead, later); line != "" {
		t.Fatalf("told again with no new flip: %q", line)
	}

	mac.daemon.sweepLedgerBacklog(t.Context(), time.Now())
	if line := mac.daemon.backlogReadyLine(t.Context(), lead, later); line != "Backlog: "+id+" ready (later: its revisit date came), parked by local-a." {
		t.Fatalf("after it read not ready and ready again, the line = %q", line)
	}
}

func TestABacklogItemTurningReadyPublishesALedgerChange(t *testing.T) {
	mac, _, _ := ledgerOnTwoHosts(t)
	revisit := time.Now().Add(time.Hour)
	parkedWork(t, mac, "after the release", ledger.Park{Kind: ledger.ParkLater, Why: "after the release", Revisit: revisit})
	hub := mac.daemon.ledgerChanges()
	id, wake, cancel := hub.subscribe()
	defer cancel()
	mac.daemon.sweepLedgerBacklog(t.Context(), time.Now())
	drain := func() map[string]string {
		got := map[string]string{}
		select {
		case <-wake:
		case <-time.After(5 * time.Second):
			return got
		}
		for _, change := range hub.take(id) {
			got[change.ProjectKey] = change.Version
		}
		return got
	}
	drain()

	mac.daemon.sweepLedgerBacklog(t.Context(), time.Now())
	select {
	case <-wake:
		t.Fatalf("a sweep with nothing flipped published %+v", hub.take(id))
	case <-time.After(2 * ledgerChangeWait):
	}

	mac.daemon.sweepLedgerBacklog(t.Context(), revisit.Add(time.Minute))
	changes := drain()
	if _, published := changes[widgetKey]; !published {
		t.Fatalf("turning ready published %+v", changes)
	}
	for at, want := range map[time.Time]string{time.Now(): `{"total":1,"ready":0,"kinds":{"later":{"total":1,"ready":0}}}`, revisit.Add(time.Minute): `{"total":1,"ready":1,"kinds":{"later":{"total":1,"ready":1}}}`} {
		board, err := mac.daemon.ledgerRead(widgetKey, ledger.ReadRequest{Now: at})
		if err != nil {
			t.Fatal(err)
		}
		if counts, _ := json.Marshal(board["backlog"]); string(counts) != want {
			t.Fatalf("the board's backlog at %s = %s, want %s", at, counts, want)
		}
	}
}

func TestAnAskAnsweredOnTheParkingHostMakesItsWorkReadyOnTheLedgersOwner(t *testing.T) {
	mac, owner, record := ledgerOnTwoHosts(t)
	mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"}, nil)
	ask := personAsk(t, mac)
	id := parkedWork(t, mac, "search titles", ledger.Park{Kind: ledger.ParkPerson, Person: &ledger.ParkQuestion{Question: "titles only?", AskID: ask}})
	board, err := owner.daemon.ledgerStore().Board(widgetKey)
	if err != nil {
		t.Fatal(err)
	}
	if item, ok := board.WorkItem(id); !ok || item.Park == nil || item.Park.Person.HostID != mac.daemon.localHost().ID || item.Park.Person.HostID == record.ID {
		t.Fatalf("the owner holds %+v, want the ask's host %q", item.Park, mac.daemon.localHost().ID)
	}

	mac.daemon.sweepLedgerBacklog(t.Context(), time.Now())
	owner.daemon.sweepLedgerBacklog(t.Context(), time.Now())
	if parked := parkedInNeedsYou(t, owner); len(parked) != 0 {
		t.Fatalf("while its ask is open on the parking host, the owner listed %+v", parked)
	}
	if claimed, err := mac.reg.ClaimAsk(ask, registry.AskAnswer{}, surface.PersonSessionID, "test", time.Now()); err != nil || !claimed {
		t.Fatalf("answer %s: claimed=%v err=%v", ask, claimed, err)
	}
	mac.daemon.sweepLedgerBacklog(t.Context(), time.Now())
	owner.daemon.sweepLedgerBacklog(t.Context(), time.Now())
	if row := backlogEntry(t, owner, id); row["ready"] != true || row["why"] != "the person answered "+ask {
		t.Fatalf("on the owner, after the parking host's answer: %+v", row)
	}
	if parked := parkedInNeedsYou(t, owner); len(parked) != 0 {
		t.Fatalf("answered work is still in the owner's Needs you: %+v", parked)
	}

	if _, err := mac.reg.WriteDB().Exec(`UPDATE asks SET state=? WHERE id=?`, registry.AskExpired, ask); err != nil {
		t.Fatal(err)
	}
	mac.daemon.sweepLedgerBacklog(t.Context(), time.Now())
	owner.daemon.sweepLedgerBacklog(t.Context(), time.Now())
	if parked := parkedInNeedsYou(t, owner); len(parked) != 1 || parked[id].WorkItem != id {
		t.Fatalf("with its ask expired on the parking host, the owner's Needs you = %+v", parked)
	}

	if _, err := mac.reg.WriteDB().Exec(`UPDATE asks SET state=? WHERE id=?`, registry.AskAnswered, ask); err != nil {
		t.Fatal(err)
	}
	mac.daemon.sweepLedgerBacklog(t.Context(), time.Now())
	owner.daemon.backlog.mu.Lock()
	owner.daemon.backlog.asks = nil
	owner.daemon.backlog.mu.Unlock()
	keys, err := mac.daemon.ledgerStore().ProjectKeys()
	if err != nil {
		t.Fatal(err)
	}
	mac.daemon.sendAskStates(t.Context(), mac.daemon.ledgerStore(), keys, time.Now())
	if state := owner.daemon.askState(ledger.ParkQuestion{AskID: ask, HostID: mac.daemon.localHost().ID}); state != "" {
		t.Fatalf("unchanged states were sent again at once: %q", state)
	}
	mac.daemon.sendAskStates(t.Context(), mac.daemon.ledgerStore(), keys, time.Now().Add(ledgerAskStatesEvery))
	if state := owner.daemon.askState(ledger.ParkQuestion{AskID: ask, HostID: mac.daemon.localHost().ID}); state != registry.AskAnswered {
		t.Fatalf("an owner that lost its states got %q back, want them resent", state)
	}
}

func TestACoordinatorIsToldOnlyItsOwnProjectsBacklog(t *testing.T) {
	mac, _, _ := ledgerOnTwoHosts(t)
	const other = "repo:github.com/example/other"
	store := mac.daemon.ledgerStore()
	if err := store.Enable(other, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	elsewhere, err := store.Work(ledger.WorkRequest{ProjectKey: other, SessionID: "local-a", Action: ledger.WorkAdd, Text: "other work"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Work(ledger.WorkRequest{ProjectKey: other, SessionID: "local-a", Action: ledger.WorkPark, ItemID: elsewhere.Item.ID, Park: &ledger.Park{Kind: ledger.ParkLater, Why: "x", Revisit: past}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	own := parkedWork(t, mac, "own work", ledger.Park{Kind: ledger.ParkLater, Why: "x", Revisit: past})
	inOther := &mcpCaller{session: surface.Session{ID: "lead"}, project: &CatalogProject{ID: other, Key: other}}
	line := mac.daemon.backlogReadyLine(t.Context(), inOther, time.Now())
	if !strings.Contains(line, own+" ready") || strings.Contains(line, elsewhere.Item.ID) {
		t.Fatalf("the widget coordinator working in another checkout was told %q", line)
	}
	if told, err := mac.reg.BacklogTold(other); err != nil || len(told) != 0 {
		t.Fatalf("the other project's told marks = %v, %v", told, err)
	}
}

func TestEveryAnsweredAskReachesTheOwnerHoweverManyArePending(t *testing.T) {
	mac, owner, _ := ledgerOnTwoHosts(t)
	mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"}, nil)
	var last, lastAsk string
	var asks []string
	for range ledgerAskStatesMost + 1 {
		lastAsk = personAsk(t, mac)
		asks = append(asks, lastAsk)
		last = parkedWork(t, mac, "search "+lastAsk, ledger.Park{Kind: ledger.ParkPerson, Person: &ledger.ParkQuestion{Question: "titles only?", AskID: lastAsk}})
	}
	if claimed, err := mac.reg.ClaimAsk(lastAsk, registry.AskAnswer{}, surface.PersonSessionID, "test", time.Now()); err != nil || !claimed {
		t.Fatalf("answer %s: claimed=%v err=%v", lastAsk, claimed, err)
	}
	mac.daemon.sweepLedgerBacklog(t.Context(), time.Now())
	owner.daemon.sweepLedgerBacklog(t.Context(), time.Now())
	if row := backlogEntry(t, owner, last); row["ready"] != true {
		t.Fatalf("the last of %d parked asks, answered, reads %+v on the owner", ledgerAskStatesMost+1, row)
	}
	for _, ask := range asks {
		if state := owner.daemon.askState(ledger.ParkQuestion{AskID: ask, HostID: mac.daemon.localHost().ID}); state == "" {
			t.Fatalf("the owner does not know %s, so a later report replaced an earlier one", ask)
		}
	}

	mustLedger(t, mac, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkUnpark, ItemID: last}, nil)
	owner.daemon.sweepLedgerBacklog(t.Context(), time.Now())
	if state := owner.daemon.askState(ledger.ParkQuestion{AskID: lastAsk, HostID: mac.daemon.localHost().ID}); state != "" {
		t.Fatalf("the owner still holds %q for %s after its work was unparked", state, lastAsk)
	}
	owner.daemon.backlog.mu.Lock()
	held := len(owner.daemon.backlog.asks[mac.daemon.localHost().ID])
	owner.daemon.backlog.mu.Unlock()
	if held != ledgerAskStatesMost {
		t.Fatalf("the owner holds %d ask states, want the %d still parked", held, ledgerAskStatesMost)
	}
}

func TestOnlyAPairedHostReportsAskStates(t *testing.T) {
	mac, _, _ := ledgerOnTwoHosts(t)
	response := mac.request(t, http.MethodPost, "/api/v1/ledger/sync/asks", ledgerAskStates{States: map[string]string{"ask-000000000001": registry.AskAnswered}})
	if response.Code == http.StatusOK {
		t.Fatalf("a local client reported ask states: %d %s", response.Code, response.Body)
	}
	if state := mac.daemon.askState(ledger.ParkQuestion{AskID: "ask-000000000001", HostID: "elsewhere"}); state != "" {
		t.Fatalf("the refused report was kept: %q", state)
	}
}
