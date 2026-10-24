package voice

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

type scriptedBriefer struct {
	mu    sync.Mutex
	reads []map[string]TurnOutcome
}

func (b *scriptedBriefer) BriefSource(_ context.Context, session *surface.Session) BriefSource {
	return BriefSource{Session: *session}
}

func (b *scriptedBriefer) TurnOutcomes(context.Context, *surface.Session, []string) (map[string]TurnOutcome, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	next := b.reads[0]
	if len(b.reads) > 1 {
		b.reads = b.reads[1:]
	}
	return next, nil
}

func TestAJournalThatEndsBeforeTheReplyKeepsTheRequestForTheNextCall(t *testing.T) {
	target := workingTarget()
	f := newFixture(t, fixtureOptions{target: target})
	f.target = target
	f.service.SetBriefer(&fixedBriefer{outcomes: map[string]TurnOutcome{"target-turn": {Found: true, Done: true, Reply: "All 40 tests pass."}}})
	selectAndConnect(t, f, target)
	apply(t, f, Action{Action: "delegate", AttemptID: "call-a", MessageID: "m", Text: "Run the tests"})
	target.journal.end()
	testkit.Eventually(t, "the follower to end with the journal", 5*time.Second, func() bool {
		return hasEventWith(f.View(), "voice/delegation/failed", "error", "the session journal ended")
	})
	if kept := f.keptRequests(); len(kept) != 1 {
		t.Fatalf("the journal's end dropped the request: %+v", kept)
	}
	callAgain(t, f, "call-a", "call-b")
	if brief := lastStartBrief(t, f); !strings.Contains(brief, `- "Run the tests": the session replied: All 40 tests pass.`) {
		t.Fatalf("brief:\n%s", brief)
	}
	if kept := f.keptRequests(); len(kept) != 0 {
		t.Fatalf("a reported request was kept: %+v", kept)
	}
}

func TestARequestDeliveredAcrossASourceErrorIsFollowedByTheNewFollower(t *testing.T) {
	target := workingTarget()
	f := newFixture(t, fixtureOptions{target: target})
	f.target = target
	selectAndConnect(t, f, target)
	testkit.Eventually(t, "the call to follow its session", 5*time.Second, func() bool {
		prepared, _ := target.journal.subscriptions()
		return prepared == 1
	})
	f.gate.hold("Send")
	delegated := make(chan error, 1)
	go func() {
		_, err := f.Apply(Action{Action: "delegate", AttemptID: "call-a", MessageID: "m", Text: "Run the tests"})
		delegated <- err
	}()
	f.gate.reached(t, "Send")
	target.journal.emit(sessionstream.Event{Kind: "source-error", Reason: "transcript read failed"})
	testkit.Eventually(t, "voice to follow the session again", 5*time.Second, func() bool {
		prepared, _ := target.journal.subscriptions()
		return prepared == 2 && hasEventWith(f.View(), "voice/target/refollowed", "error", "transcript read failed")
	})
	f.gate.release("Send")
	if err := <-delegated; err != nil {
		t.Fatal(err)
	}
	target.journal.emit(sessionstream.Event{ItemID: "reply", Version: 1, Kind: "text", Role: "assistant", TurnID: "target-turn", Body: "All 40 tests pass.", Final: true})
	target.journal.emit(sessionstream.Event{ItemID: "done", Version: 1, Kind: "done", TurnID: "target-turn"})
	testkit.Eventually(t, "the reply as the request's own", 5*time.Second, func() bool {
		return spokenWith(f, "All 40 tests pass.") == 1 && hasEventWith(f.View(), "voice/delegation/final", "messageId", "m") && len(f.keptRequests()) == 0
	})
	if hasEventWith(f.View(), "voice/target/typed", "turnId", "target-turn") {
		t.Fatalf("the request's turn was taken as typed: %+v", f.View().Events)
	}
}

func TestATurnThatEndsBetweenTheBriefAndTheFollowerIsSpokenAsItsRequest(t *testing.T) {
	target := workingTarget()
	f := newFixture(t, fixtureOptions{target: target})
	f.target = target
	f.service.SetBriefer(&scriptedBriefer{reads: []map[string]TurnOutcome{
		{"target-turn": {Found: true, Reply: "Reading the logs."}},
		{"target-turn": {Found: true, Done: true, Reply: "All 40 tests pass."}},
	}})
	selectAndConnect(t, f, target)
	apply(t, f, Action{Action: "delegate", AttemptID: "call-a", MessageID: "m", Text: "Run the tests"})
	callAgain(t, f, "call-a", "call-b")
	if brief := lastStartBrief(t, f); !strings.Contains(brief, `- "Run the tests": the session is still working on it; its reply will follow.`) {
		t.Fatalf("brief:\n%s", brief)
	}
	testkit.Eventually(t, "the reply that ended before the follower subscribed", 5*time.Second, func() bool {
		return spokenWith(f, "All 40 tests pass.") == 1 && len(f.keptRequests()) == 0
	})
	if !hasEventWith(f.View(), "voice/delegation/final", "messageId", "m") || spokenWith(f, "typed") != 0 {
		t.Fatalf("the reply was not spoken as the request's own: %q %+v", f.provider.spoken(), f.View().Events)
	}
}

func TestRequestsPastTheBriefCapAreKeptForALaterCall(t *testing.T) {
	target := workingTarget()
	target.perTurn = true
	f := newFixture(t, fixtureOptions{target: target})
	f.target = target
	outcomes := map[string]TurnOutcome{}
	for i := 1; i <= briefRequestsMax+1; i++ {
		outcomes[fmt.Sprintf("turn-%d", i)] = TurnOutcome{Found: true, Done: true, Reply: fmt.Sprintf("Done %d.", i)}
	}
	f.service.SetBriefer(&fixedBriefer{outcomes: outcomes})
	selectAndConnect(t, f, target)
	for i := 1; i <= briefRequestsMax+1; i++ {
		apply(t, f, Action{Action: "delegate", AttemptID: "call-a", MessageID: fmt.Sprintf("m%d", i), Text: fmt.Sprintf("Request %d", i)})
	}
	callAgain(t, f, "call-a", "call-b")
	brief := lastStartBrief(t, f)
	for i := 1; i <= briefRequestsMax; i++ {
		if want := fmt.Sprintf(`- "Request %d": the session replied: Done %d.`, i, i); !strings.Contains(brief, want) {
			t.Fatalf("brief lacks %q:\n%s", want, brief)
		}
	}
	last := fmt.Sprintf("Request %d", briefRequestsMax+1)
	if strings.Contains(brief, last) || !strings.Contains(brief, "1 more earlier request(s) kept for a later call") {
		t.Fatalf("the request past the cap was not left out and labelled:\n%s", brief)
	}
	if kept := f.keptRequests(); len(kept) != 1 || kept[0].Prompt != last {
		t.Fatalf("kept=%+v", kept)
	}
	callAgain(t, f, "call-b", "call-c")
	if brief := lastStartBrief(t, f); !strings.Contains(brief, fmt.Sprintf(`- "%s": the session replied: Done %d.`, last, briefRequestsMax+1)) {
		t.Fatalf("the kept request was not reported later:\n%s", brief)
	}
	if kept := f.keptRequests(); len(kept) != 0 {
		t.Fatalf("kept=%+v", kept)
	}
}

func TestKeptRequestsSurviveARestart(t *testing.T) {
	target := workingTarget()
	f := newFixture(t, fixtureOptions{target: target})
	f.target = target
	selectAndConnect(t, f, target)
	apply(t, f, Action{Action: "delegate", AttemptID: "call-a", MessageID: "m", Text: "Run the tests"})
	apply(t, f, Action{Action: "stop", AttemptID: "call-a"})
	restarted := newFixture(t, fixtureOptions{path: f.path, target: target})
	if kept := restarted.keptRequests(); len(kept) != 1 || kept[0].TurnID != "target-turn" || kept[0].MessageID != "m" {
		t.Fatalf("kept after restart=%+v", kept)
	}
}
