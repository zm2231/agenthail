package ledger

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry/registrytest"
)

type wordsLog struct {
	events    []Event
	retracted map[string]string
}

func randomWordsLog(rng *rand.Rand) wordsLog {
	sessions := []string{"lead", "b", "c", "d"}
	refs := []string{"claude-prompt:u0", "claude-prompt:u1", "claude-prompt:u2", "claude-prompt:u3", "claude-prompt:u4", "claude-prompt:u5"}
	l := wordsLog{retracted: map[string]string{}}
	add := func(t EventType, p any) {
		data, _ := json.Marshal(p)
		l.events = append(l.events, Event{ProjectKey: lockProject, Type: t, Payload: data, CreatedAt: time.Now()})
	}
	for _, ref := range refs {
		add(EventUtterance, UtterancePayload{UtteranceID: ref, Text: ref, SessionID: "person", TargetID: "lead", CapturedBy: CapturedByHook})
		if rng.Intn(2) == 0 {
			l.retracted[ref] = "scheduled"
		}
	}
	var asks, clauses, decisions []string
	versions := map[string]int{}
	items := map[string]WorkState{}
	var itemIDs []string
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	ref := func() string { return pick(refs) }
	newClause := func(ask string, src string, source ClauseSource) {
		id := fmt.Sprintf("C%d", len(clauses)+1)
		clauses = append(clauses, id)
		add(EventClause, ClausePayload{ClauseID: id, AskID: ask, Text: id, State: StateOwned, Source: source, OwnerSessionID: pick(sessions), SourceUtteranceID: src})
	}
	for range 40 {
		switch rng.Intn(10) {
		case 0:
			id := fmt.Sprintf("A%d", len(asks)+1)
			asks = append(asks, id)
			src := ref()
			add(EventAsk, AskPayload{AskID: id, Type: AskBuild, Principal: "person", SourceUtterance: src})
			newClause(id, src, SourceStated)
		case 1:
			if len(asks) > 0 {
				newClause(pick(asks), ref(), SourceStated)
			}
		case 2:
			if len(asks) > 0 {
				newClause(pick(asks), "", SourceProposed)
			}
		case 3:
			if len(asks) > 0 {
				add(EventDelegation, DelegationPayload{AskID: pick(asks), DelegatorSessionID: pick(sessions), DelegateSessionID: pick(sessions), SourceUtteranceID: ref()})
			}
		case 4:
			if len(clauses) > 0 {
				add(EventSupersession, SupersessionPayload{ClauseID: pick(clauses), NewVersion: fmt.Sprintf("text %d", rng.Intn(100)), SourceUtteranceID: ref()})
			}
		case 5:
			if len(clauses) > 0 {
				add(EventClauseSource, ClauseSourcePayload{ClauseID: pick(clauses), FromSource: SourceProposed, ToSource: SourceAdopted, UtteranceID: ref(), OwnerSessionID: pick(sessions)})
			}
		case 6:
			id := fmt.Sprintf("D%d", len(decisions)+1)
			decisions = append(decisions, id)
			versions[id] = 1
			add(EventDecision, DecisionPayload{DecisionID: id, Version: 1, Text: id + " v1", Principal: "person", SourceUtterance: ref()})
		case 7:
			if len(decisions) > 0 {
				id := pick(decisions)
				versions[id]++
				add(EventDecisionVer, DecisionPayload{DecisionID: id, Version: versions[id], Text: fmt.Sprintf("%s v%d", id, versions[id]), Principal: "person", SourceUtterance: ref()})
			}
		case 8:
			if len(clauses) > 0 {
				id := fmt.Sprintf("W%d", len(itemIDs)+1)
				itemIDs = append(itemIDs, id)
				items[id] = WorkOpen
				add(EventWorkItem, WorkItemPayload{ItemID: id, ClauseID: pick(clauses), Text: id, CreatedBy: pick(sessions)})
			}
		case 9:
			if len(itemIDs) == 0 {
				continue
			}
			id := pick(itemIDs)
			p := WorkItemStatePayload{ItemID: id, FromState: items[id], Actor: pick(sessions)}
			switch items[id] {
			case WorkOpen:
				p.ToState, p.Assignee = WorkInProgress, p.Actor
			case WorkInProgress:
				p.ToState = WorkInReview
			case WorkInReview:
				p.ToState, p.Reviewer, p.UtteranceID = WorkDone, "person", ref()
			case WorkDone:
				p.ToState, p.Reason = WorkOpen, "reopened"
			}
			items[id] = p.ToState
			add(EventWorkItemState, p)
		}
	}
	for ref, reason := range l.retracted {
		data, _ := json.Marshal(ReclassifiedPayload{UtteranceID: ref, Reason: reason})
		at := len(refs) + rng.Intn(len(l.events)-len(refs)+1)
		e := Event{ProjectKey: lockProject, Type: EventReclassified, Payload: data, Ref: reclassifiedRef(ref), CreatedAt: time.Now()}
		l.events = slices.Insert(l.events, at, e)
	}
	for i := range l.events {
		l.events[i].Seq = int64(i + 1)
	}
	return l
}

type oracleSource struct {
	ref     string
	created string
	built   []string
}

func oracleSourceOf(e Event, askOf map[string]string) oracleSource {
	switch e.Type {
	case EventUtterance:
		var p UtterancePayload
		_ = json.Unmarshal(e.Payload, &p)
		return oracleSource{ref: p.UtteranceID}
	case EventAsk:
		var p AskPayload
		_ = json.Unmarshal(e.Payload, &p)
		return oracleSource{ref: p.SourceUtterance, created: "ask:" + p.AskID}
	case EventClause:
		var p ClausePayload
		_ = json.Unmarshal(e.Payload, &p)
		askOf[p.ClauseID] = p.AskID
		return oracleSource{ref: p.SourceUtteranceID, created: "clause:" + p.ClauseID, built: []string{"ask:" + p.AskID}}
	case EventDelegation:
		var p DelegationPayload
		_ = json.Unmarshal(e.Payload, &p)
		return oracleSource{ref: p.SourceUtteranceID, built: []string{"ask:" + p.AskID}}
	case EventSupersession:
		var p SupersessionPayload
		_ = json.Unmarshal(e.Payload, &p)
		return oracleSource{ref: p.SourceUtteranceID, built: []string{"clause:" + p.ClauseID, "ask:" + askOf[p.ClauseID]}}
	case EventClauseSource:
		var p ClauseSourcePayload
		_ = json.Unmarshal(e.Payload, &p)
		return oracleSource{ref: p.UtteranceID, built: []string{"clause:" + p.ClauseID, "ask:" + askOf[p.ClauseID]}}
	case EventDecision:
		var p DecisionPayload
		_ = json.Unmarshal(e.Payload, &p)
		return oracleSource{ref: p.SourceUtterance, created: "decision:" + p.DecisionID}
	case EventDecisionVer:
		var p DecisionPayload
		_ = json.Unmarshal(e.Payload, &p)
		return oracleSource{ref: p.SourceUtterance, built: []string{"decision:" + p.DecisionID}}
	case EventWorkItemState:
		var p WorkItemStatePayload
		_ = json.Unmarshal(e.Payload, &p)
		return oracleSource{ref: p.UtteranceID}
	}
	return oracleSource{}
}

// neverCaptured is the oracle: the log as if the retracted words had never been captured, except that what the person built on stays, sourced to the person's words.
func neverCaptured(events []Event, retracted map[string]string) ([]Event, map[string]string, int) {
	kept := map[string]string{}
	askOf := map[string]string{}
	for _, e := range events {
		src := oracleSourceOf(e, askOf)
		if _, gone := retracted[src.ref]; src.ref == "" || gone {
			continue
		}
		for _, key := range src.built {
			if _, ok := kept[key]; !ok {
				kept[key] = src.ref
			}
		}
	}
	var out []Event
	lines := 0
	for _, e := range events {
		if e.Type == EventReclassified {
			continue
		}
		src := oracleSourceOf(e, askOf)
		if _, gone := retracted[src.ref]; !gone || src.ref == "" {
			out = append(out, e)
			continue
		}
		if e.Type != EventUtterance {
			lines++
		}
		to, ok := kept[src.created]
		if src.created == "" || !ok {
			continue
		}
		var p map[string]any
		_ = json.Unmarshal(e.Payload, &p)
		if e.Type == EventClause {
			delete(p, "sourceUtteranceId")
		} else {
			p["sourceUtteranceId"] = to
		}
		e.Payload, _ = json.Marshal(p)
		out = append(out, e)
	}
	resourced := map[string]string{}
	for _, e := range events {
		src := oracleSourceOf(e, askOf)
		if _, gone := retracted[src.ref]; gone && src.created != "" && kept[src.created] != "" {
			resourced[src.created] = kept[src.created]
		}
	}
	return out, resourced, lines
}

func boardState(t *testing.T, b *Board) string {
	t.Helper()
	asks := b.AllAsks()
	slices.SortFunc(asks, func(x, y Ask) int { return strings.Compare(x.ID, y.ID) })
	decisions := b.Decisions()
	slices.SortFunc(decisions, func(x, y Decision) int { return strings.Compare(x.ID, y.ID) })
	items := b.WorkItems()
	slices.SortFunc(items, func(x, y WorkItem) int { return strings.Compare(x.ID, y.ID) })
	var utterances []string
	for _, u := range b.Utterances() {
		utterances = append(utterances, u.ID)
	}
	data, err := json.Marshal(struct {
		Asks       []Ask
		Decisions  []Decision
		Items      []WorkItem
		Utterances []string
		Applied    int64
	}{asks, decisions, items, utterances, b.AppliedSeq()})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func checkAgainstOracle(t *testing.T, name string, b *Board, events []Event, retracted map[string]string) {
	t.Helper()
	filtered, resourced, lines := neverCaptured(events, retracted)
	oracle := NewBoard()
	if err := oracle.Rebuild(filtered); err != nil {
		t.Fatal(err)
	}
	oracle.applied = events[len(events)-1].Seq
	if got, want := boardState(t, b), boardState(t, oracle); got != want {
		t.Fatalf("%s board differs from the log without the retracted words\n got %s\nwant %s", name, got, want)
	}
	retractions := b.Retractions()
	if len(retractions) != lines {
		t.Fatalf("%s board: %d retraction lines, want one per change built from retracted words (%d): %+v", name, len(retractions), lines, retractions)
	}
	gotResourced := map[string]string{}
	for _, r := range retractions {
		if _, ok := retracted[r.UtteranceID]; !ok {
			t.Fatalf("%s board: retraction cites words that were not retracted: %+v", name, r)
		}
		if r.ReSourcedTo != "" {
			gotResourced[r.Kind+":"+r.ID] = r.ReSourcedTo
			if !strings.Contains(r.String(), "re-sourced to "+r.ReSourcedTo) {
				t.Fatalf("%s board: re-sourcing is not shown: %s", name, r)
			}
		}
	}
	if fmt.Sprint(gotResourced) != fmt.Sprint(resourced) {
		t.Fatalf("%s board: re-sourced %v, want %v", name, gotResourced, resourced)
	}
}

func TestACascadeOfDelegationsRetractsInEitherOrder(t *testing.T) {
	for _, order := range [][]string{{"claude-prompt:c", "claude-prompt:lead"}, {"claude-prompt:lead", "claude-prompt:c"}} {
		db := openRegistry(t, registrytest.Path(t)).WriteDB()
		s := NewCachedStore(db, db)
		insert := func(typ EventType, ref string, p any) {
			data, _ := json.Marshal(p)
			if _, err := insertEvent(db, `INSERT`, Event{ProjectKey: lockProject, Type: typ, Payload: data, Ref: ref, CreatedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Board(lockProject); err != nil {
				t.Fatal(err)
			}
		}
		for _, ref := range []string{"claude-prompt:lead", "claude-prompt:c"} {
			insert(EventUtterance, "", UtterancePayload{UtteranceID: ref, Text: ref, CapturedBy: CapturedByHook})
		}
		insert(EventAsk, "", AskPayload{AskID: "A1", Type: AskBuild, Principal: "person"})
		insert(EventClause, "", ClausePayload{ClauseID: "C1", AskID: "A1", Text: "build", State: StateOwned, OwnerSessionID: "lead"})
		insert(EventDelegation, "", DelegationPayload{AskID: "A1", DelegatorSessionID: "lead", DelegateSessionID: "b", SourceUtteranceID: "claude-prompt:lead"})
		insert(EventDelegation, "", DelegationPayload{AskID: "A1", DelegatorSessionID: "b", DelegateSessionID: "c", SourceUtteranceID: "claude-prompt:c"})
		for _, ref := range order {
			insert(EventReclassified, reclassifiedRef(ref), ReclassifiedPayload{UtteranceID: ref, Reason: "scheduled"})
		}
		cached, err := s.Board(lockProject)
		if err != nil {
			t.Fatal(err)
		}
		rebuilt, err := NewStore(db, db).Board(lockProject)
		if err != nil {
			t.Fatal(err)
		}
		for name, b := range map[string]*Board{"cached": cached, "rebuilt": rebuilt} {
			asks := b.AllAsks()
			if len(asks) != 1 || len(asks[0].Delegates) != 0 || asks[0].Clauses[0].OwnerSessionID != "lead" {
				t.Fatalf("order %v, %s board: want lead owning with no delegates, got %+v", order, name, asks)
			}
		}
	}
}

func TestRetractionEqualsTheLogWithoutTheRetractedWords(t *testing.T) {
	for seed := int64(1); seed <= 500; seed++ {
		l := randomWordsLog(rand.New(rand.NewSource(seed)))
		b := NewBoard()
		if err := b.Rebuild(l.events); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		t.Run(fmt.Sprint(seed), func(t *testing.T) { checkAgainstOracle(t, "rebuilt", b, l.events, l.retracted) })
	}
}

func TestACachedBoardRetractsInAnyOrderLikeARebuild(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		l := randomWordsLog(rng)
		db := openRegistry(t, registrytest.Path(t)).WriteDB()
		s := NewCachedStore(db, db)
		for _, e := range l.events {
			if _, err := insertEvent(db, `INSERT`, e); err != nil {
				t.Fatalf("seed %d: %v", seed, err)
			}
			if rng.Intn(2) == 0 {
				if _, err := s.Board(lockProject); err != nil {
					t.Fatalf("seed %d: %v", seed, err)
				}
			}
		}
		stored, err := loadEvents(db, lockProject)
		if err != nil {
			t.Fatal(err)
		}
		cached, err := s.Board(lockProject)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		rebuilt, err := NewStore(db, db).Board(lockProject)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			checkAgainstOracle(t, "cached", cached, stored, l.retracted)
			checkAgainstOracle(t, "rebuilt", rebuilt, stored, l.retracted)
		})
	}
}
