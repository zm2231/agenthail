package voice

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func workingTarget() *targetFixture {
	return &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "Fieldnotes build", Transport: "desktop", Status: surface.StatusIdle}, streaming: make(chan struct{})}
}

func callAgain(t *testing.T, f *voiceFixture, from, attemptID string) {
	t.Helper()
	apply(t, f, Action{Action: "stop", AttemptID: from})
	f.observe(Event{Method: "thread/realtime/closed", Params: map[string]any{}})
	apply(t, f, Action{Action: "start", AttemptID: attemptID, SDP: "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\n"})
	f.observe(Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": attemptID}}, Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 answer"}})
	apply(t, f, Action{Action: "connected", AttemptID: attemptID})
}

func lastStartBrief(t *testing.T, f *voiceFixture) string {
	t.Helper()
	starts := requestParams(f.provider, "thread/realtime/start")
	items, _ := starts[len(starts)-1]["initialItems"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("start items=%v", items)
	}
	return items[0]["text"].(string)
}

func (f *voiceFixture) keptRequests() []requestRecord {
	f.service.mu.Lock()
	defer f.service.mu.Unlock()
	return append([]requestRecord(nil), f.service.state.Requests...)
}

func TestAReplyThatFinishesAfterTheCallEndedIsReportedOnceInTheNextCallAndNeverSentAgain(t *testing.T) {
	target := workingTarget()
	f := newFixture(t, fixtureOptions{target: target})
	f.target = target
	briefer := &fixedBriefer{outcomes: map[string]TurnOutcome{"target-turn": {Found: true, Done: true, Reply: "All 40 tests pass. The flaky one was a timeout."}}}
	f.service.SetBriefer(briefer)
	selectAndConnect(t, f, target)
	apply(t, f, Action{Action: "delegate", AttemptID: "call-a", MessageID: "m", Text: "Run the tests"})
	apply(t, f, Action{Action: "stop", AttemptID: "call-a"})
	if kept := f.keptRequests(); len(kept) != 1 || kept[0].TurnID != "target-turn" || kept[0].Prompt != "Run the tests" {
		t.Fatalf("kept=%+v", kept)
	}
	if data, err := os.ReadFile(f.path); err != nil || !strings.Contains(string(data), `"requests"`) {
		t.Fatalf("the request was not saved for the next call: %v %s", err, data)
	}
	f.observe(Event{Method: "thread/realtime/closed", Params: map[string]any{}})
	apply(t, f, Action{Action: "start", AttemptID: "call-b", SDP: "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\n"})
	brief := lastStartBrief(t, f)
	for _, want := range []string{"Requests the person made by voice in an earlier call, not reported to them yet:", `- "Run the tests": the session replied: All 40 tests pass. The flaky one was a timeout.`} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief lacks %q:\n%s", want, brief)
		}
	}
	if kept := f.keptRequests(); len(kept) != 0 {
		t.Fatalf("a reported request was kept: %+v", kept)
	}
	f.observe(Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-b"}}, Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 answer"}})
	apply(t, f, Action{Action: "connected", AttemptID: "call-b"})
	callAgain(t, f, "call-b", "call-c")
	if brief := lastStartBrief(t, f); strings.Contains(brief, "Run the tests") {
		t.Fatalf("the outcome was reported twice:\n%s", brief)
	}
	if sends := target.sends(); len(sends) != 1 {
		t.Fatalf("the request was sent again: %q", sends)
	}
}

func TestARequestStillWorkingAtTheNextCallIsSaidSoAndItsReplySpokenWhenItComes(t *testing.T) {
	target := workingTarget()
	f := newFixture(t, fixtureOptions{target: target})
	f.target = target
	f.service.SetBriefer(&fixedBriefer{outcomes: map[string]TurnOutcome{"target-turn": {Found: true, Reply: "Reading the logs."}}})
	selectAndConnect(t, f, target)
	apply(t, f, Action{Action: "delegate", AttemptID: "call-a", MessageID: "m", Text: "Run the tests"})
	callAgain(t, f, "call-a", "call-b")
	if brief := lastStartBrief(t, f); !strings.Contains(brief, `- "Run the tests": the session is still working on it; its reply will follow.`) {
		t.Fatalf("brief:\n%s", brief)
	}
	testkit.Eventually(t, "the new call to follow its session", 5*time.Second, func() bool {
		prepared, _ := target.journal.subscriptions()
		return prepared == 2
	})
	target.journal.emit(sessionstream.Event{ItemID: "reply", Version: 1, Kind: "text", Role: "assistant", TurnID: "target-turn", Body: "All 40 tests pass.", Final: true})
	target.journal.emit(sessionstream.Event{ItemID: "done", Version: 1, Kind: "done", TurnID: "target-turn"})
	testkit.Eventually(t, "the earlier request's reply in the new call", 5*time.Second, func() bool {
		return spokenWith(f, "All 40 tests pass.") == 1 && len(f.keptRequests()) == 0
	})
	if spokenWith(f, "typed") != 0 || !hasEventWith(f.View(), "voice/delegation/final", "messageId", "m") {
		t.Fatalf("the reply was not spoken as the request's own: %q %+v", f.provider.spoken(), f.View().Events)
	}
}

func TestACallToAnotherTargetClearsTheEarlierRequests(t *testing.T) {
	target := workingTarget()
	f := newFixture(t, fixtureOptions{target: target})
	f.target = target
	f.service.SetBriefer(&fixedBriefer{outcomes: map[string]TurnOutcome{"target-turn": {Found: true, Done: true, Reply: "Done."}}})
	selectAndConnect(t, f, target)
	apply(t, f, Action{Action: "delegate", AttemptID: "call-a", MessageID: "m", Text: "Run the tests"})
	apply(t, f, Action{Action: "transfer", AttemptID: "call-a", TargetID: ""})
	if kept := f.keptRequests(); len(kept) != 0 {
		t.Fatalf("moving to the orchestrator kept the requests: %+v", kept)
	}
	apply(t, f, Action{Action: "transfer", AttemptID: "call-a", TargetID: target.session.ID})
	notes := requestParams(f.provider, "thread/realtime/appendText")
	if text := notes[len(notes)-1]["text"].(string); strings.Contains(text, "Run the tests") {
		t.Fatalf("a cleared request was reported:\n%s", text)
	}
}

func TestEarlierRequestsWhoseOutcomeCannotBeReadAreKeptAndSaidSo(t *testing.T) {
	target := workingTarget()
	f := newFixture(t, fixtureOptions{target: target})
	f.target = target
	f.service.SetBriefer(&fixedBriefer{err: errFixture("journal busy")})
	selectAndConnect(t, f, target)
	apply(t, f, Action{Action: "delegate", AttemptID: "call-a", MessageID: "m", Text: "Run the tests"})
	callAgain(t, f, "call-a", "call-b")
	brief := lastStartBrief(t, f)
	if !strings.Contains(brief, `- "Run the tests": its outcome could not be read.`) || !strings.Contains(brief, "earlier requests' outcomes: journal busy") {
		t.Fatalf("brief:\n%s", brief)
	}
	if kept := f.keptRequests(); len(kept) != 1 {
		t.Fatalf("an unread request was dropped: %+v", kept)
	}
}

type errFixture string

func (e errFixture) Error() string { return string(e) }
