package voice

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func peerTarget(t *testing.T, briefer Briefer, perTurn bool) (*voiceFixture, *targetFixture) {
	t.Helper()
	target := &targetFixture{session: surface.Session{ID: "peer-a", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "Peer", Transport: "uds"}, quiet: true, turnID: "peer-message", perTurn: perTurn}
	f := newFixture(t, fixtureOptions{target: target})
	f.target = target
	if briefer != nil {
		f.service.SetBriefer(briefer)
	}
	selectAndConnect(t, f, target)
	testkit.Eventually(t, "the call to follow the peer", 5*time.Second, func() bool {
		prepared, _ := target.journal.subscriptions()
		return prepared == 1
	})
	return f, target
}

func delegateToPeer(t *testing.T, f *voiceFixture, messageID, text string) {
	t.Helper()
	state := apply(t, f, Action{Action: "delegate", AttemptID: "call-a", MessageID: messageID, Text: text})
	if !hasEventWith(state, "voice/delegation/dispatched", "messageId", messageID) || hasEvent(state, "voice/delegation/held") {
		t.Fatalf("the peer delegation was not followed: %+v", state.Events)
	}
}

func peerPrompt(j *targetJournal, turnID, frame, body string) {
	j.emit(sessionstream.Event{ItemID: "prompt-" + frame, Version: 1, Kind: "message", Role: "peer", TurnID: turnID, MessageID: frame, Body: body})
}

func peerReply(j *targetJournal, turnID string, version uint64, body string, final bool) {
	j.emit(sessionstream.Event{ItemID: "reply-" + turnID, Version: version, Kind: "text", Role: "assistant", TurnID: turnID, Body: body, Final: final})
}

func endPeerTurn(j *targetJournal, turnID string) {
	j.emit(sessionstream.Event{ItemID: "done-" + turnID, Version: 1, Kind: "done", TurnID: turnID})
}

func delegationEvents(f *voiceFixture, messageID, turnID string) (stages []string) {
	for _, event := range f.View().Events {
		if strings.HasPrefix(event.Method, "voice/delegation/") && event.Params["messageId"] == messageID && event.Params["turnId"] == turnID {
			stages = append(stages, strings.TrimPrefix(event.Method, "voice/delegation/"))
		}
	}
	return stages
}

func (f *voiceFixture) followedTurns() int {
	f.service.mu.Lock()
	defer f.service.mu.Unlock()
	if f.service.follow == nil {
		return 0
	}
	return len(f.service.follow.voice) + len(f.service.follow.typed)
}

func assertDrained(t *testing.T, f *voiceFixture) {
	t.Helper()
	testkit.Eventually(t, "the requests to be done", 5*time.Second, func() bool {
		return len(f.keptRequests()) == 0 && f.followedTurns() == 0
	})
}

func TestAPeerReplyIsSpokenOnceFromTheTurnThatTookItsFrame(t *testing.T) {
	f, target := peerTarget(t, nil, false)
	delegateToPeer(t, f, "voice-request-a", "Run the tests")
	peerPrompt(target.journal, "peer-turn", "peer-message", "[from Voice] Run the tests")
	peerReply(target.journal, "peer-turn", 1, "Running the tests.", false)
	peerReply(target.journal, "peer-turn", 2, "Running the tests. All tests passed.", true)
	endPeerTurn(target.journal, "peer-turn")
	testkit.Eventually(t, "the peer's reply", 5*time.Second, func() bool {
		return len(delegationEvents(f, "voice-request-a", "peer-turn")) == 2
	})
	assertDrained(t, f)
	typeTurn(target.journal, "typed-1", "Check the build", "Running the tests. All tests passed.")
	testkit.Eventually(t, "the unrelated turn", 5*time.Second, func() bool {
		return spokenWith(f, "The session replied to what the person typed there") == 1
	})
	if stages := delegationEvents(f, "voice-request-a", "peer-turn"); strings.Join(stages, ",") != "interim,final" || spokenWith(f, "Running the tests.") != 2 || spokenWith(f, " All tests passed.") != 2 {
		t.Fatalf("stages=%v spoken=%q", stages, f.provider.spoken())
	}
	if stages := delegationEvents(f, "voice-request-a", "typed-1"); len(stages) != 0 {
		t.Fatalf("an unrelated turn was spoken as the request's reply: %v", stages)
	}
}

func TestAPeerFrameSeenAgainInALaterTurnIsPlacedOnce(t *testing.T) {
	f, target := peerTarget(t, nil, false)
	delegateToPeer(t, f, "voice-request-a", "Run the tests")
	peerPrompt(target.journal, "peer-turn", "peer-message", "Run the tests")
	peerReply(target.journal, "peer-turn", 1, "All tests passed.", true)
	endPeerTurn(target.journal, "peer-turn")
	target.journal.emit(sessionstream.Event{ItemID: "again", Version: 1, Kind: "message", Role: "peer", TurnID: "later-turn", MessageID: "peer-message", Body: "Run the tests"})
	peerReply(target.journal, "later-turn", 1, "Nothing new.", true)
	endPeerTurn(target.journal, "later-turn")
	testkit.Eventually(t, "the later turn", 5*time.Second, func() bool {
		return spokenWith(f, "The session finished the turn it was working on: Nothing new.") == 1
	})
	if stages := delegationEvents(f, "voice-request-a", "peer-turn"); strings.Join(stages, ",") != "final" {
		t.Fatalf("stages=%v", stages)
	}
	if stages := delegationEvents(f, "voice-request-a", "later-turn"); len(stages) != 0 {
		t.Fatalf("the frame was placed twice: %v", stages)
	}
	assertDrained(t, f)
}

func TestTwoRequestsToOnePeerEachGetTheirOwnReply(t *testing.T) {
	f, target := peerTarget(t, nil, true)
	delegateToPeer(t, f, "voice-request-a", "Run the tests")
	delegateToPeer(t, f, "voice-request-b", "Run the build")
	peerPrompt(target.journal, "turn-a", "frame-1", "Run the tests")
	peerReply(target.journal, "turn-a", 1, "Tests passed.", true)
	endPeerTurn(target.journal, "turn-a")
	peerPrompt(target.journal, "turn-b", "frame-2", "Run the build")
	peerReply(target.journal, "turn-b", 1, "Build passed.", true)
	endPeerTurn(target.journal, "turn-b")
	testkit.Eventually(t, "both replies", 5*time.Second, func() bool {
		return len(delegationEvents(f, "voice-request-a", "turn-a")) == 1 && len(delegationEvents(f, "voice-request-b", "turn-b")) == 1
	})
	assertDrained(t, f)
	if spokenWith(f, "Tests passed.") != 1 || spokenWith(f, "Build passed.") != 1 {
		t.Fatalf("spoken=%q", f.provider.spoken())
	}
}

func TestAPeerFrameAbsorbedIntoARunningVoiceTurnResolvesFromThatTurnsReply(t *testing.T) {
	f, target := peerTarget(t, nil, true)
	delegateToPeer(t, f, "voice-request-a", "Run the tests")
	delegateToPeer(t, f, "voice-request-b", "Also run the build")
	peerPrompt(target.journal, "turn-a", "frame-1", "Run the tests")
	peerReply(target.journal, "turn-a", 1, "Running the tests.", false)
	target.journal.emit(sessionstream.Event{ItemID: "queued-frame-2", Version: 1, Kind: "message", Role: "user", TurnID: "turn-a", MessageID: "frame-2", Body: "Also run the build"})
	peerReply(target.journal, "turn-a", 2, "Running the tests. Tests and build passed.", true)
	endPeerTurn(target.journal, "turn-a")
	testkit.Eventually(t, "the shared turn's reply", 5*time.Second, func() bool {
		return len(delegationEvents(f, "voice-request-a", "turn-a")) == 2
	})
	assertDrained(t, f)
	if spokenWith(f, " Tests and build passed.") != 1 || hasEvent(f.View(), "voice/target/typed") || hasEvent(f.View(), "voice/target/turn-unattributed") {
		t.Fatalf("spoken=%q events=%+v", f.provider.spoken(), f.View().Events)
	}
}

func TestATypedTurnThatAbsorbsAPeerFrameBecomesTheRequestsTurnOnce(t *testing.T) {
	f, target := peerTarget(t, nil, false)
	target.journal.emit(sessionstream.Event{ItemID: "prompt-typed", Version: 1, Kind: "message", Role: "user", TurnID: "typed-turn", MessageID: "typed-turn", Body: "Check the build"})
	testkit.Eventually(t, "the typed turn's note", 5*time.Second, func() bool {
		return spokenWith(f, `The person typed in the session, not by voice: "Check the build"`) == 1
	})
	delegateToPeer(t, f, "voice-request-a", "Run the tests too")
	target.journal.emit(sessionstream.Event{ItemID: "queued-peer", Version: 1, Kind: "message", Role: "user", TurnID: "typed-turn", MessageID: "peer-message", Body: "Run the tests too"})
	target.journal.emit(sessionstream.Event{ItemID: "queued-again", Version: 1, Kind: "message", Role: "user", TurnID: "typed-turn", MessageID: "peer-message", Body: "Run the tests too"})
	peerReply(target.journal, "typed-turn", 1, "Build is green and tests pass.", true)
	endPeerTurn(target.journal, "typed-turn")
	testkit.Eventually(t, "the absorbed request's reply", 5*time.Second, func() bool {
		return len(delegationEvents(f, "voice-request-a", "typed-turn")) == 1
	})
	assertDrained(t, f)
	if spokenWith(f, "Build is green and tests pass.") != 1 || f.queuedReplies() != 0 {
		t.Fatalf("spoken=%q", f.provider.spoken())
	}
}

func TestAPeerTurnFollowedAgainMidTurnKeepsItsRequest(t *testing.T) {
	f, target := peerTarget(t, nil, false)
	delegateToPeer(t, f, "voice-request-a", "Run the tests")
	peerPrompt(target.journal, "peer-turn", "peer-message", "Run the tests")
	peerReply(target.journal, "peer-turn", 1, "Running the tests.", false)
	testkit.Eventually(t, "the interim reply", 5*time.Second, func() bool {
		return len(delegationEvents(f, "voice-request-a", "peer-turn")) == 1
	})
	target.journal.emit(sessionstream.Event{Kind: "source-error", Reason: "transcript read failed"})
	testkit.Eventually(t, "the peer to be followed again", 5*time.Second, func() bool {
		prepared, _ := target.journal.subscriptions()
		return prepared == 2
	})
	if kept := f.keptRequests(); len(kept) != 1 || kept[0].TurnID != "peer-turn" {
		t.Fatalf("kept=%+v", kept)
	}
	peerReply(target.journal, "peer-turn", 2, "Running the tests. All tests passed.", true)
	endPeerTurn(target.journal, "peer-turn")
	testkit.Eventually(t, "the final reply", 5*time.Second, func() bool {
		return strings.Join(delegationEvents(f, "voice-request-a", "peer-turn"), ",") == "interim,final"
	})
	assertDrained(t, f)
}

func TestTwoKeptPeerRequestsThatOneTurnAnsweredAreToldOnceInTheNextCall(t *testing.T) {
	running := TurnOutcome{TurnID: "turn-a", Found: true}
	done := TurnOutcome{TurnID: "turn-a", Found: true, Done: true, Reply: "Tests and build passed."}
	briefer := &scriptedBriefer{reads: []map[string]TurnOutcome{
		{"frame-1": running, "frame-2": running},
		{"frame-1": done, "frame-2": done},
	}}
	f, target := peerTarget(t, briefer, true)
	delegateToPeer(t, f, "voice-request-a", "Run the tests")
	delegateToPeer(t, f, "voice-request-b", "Also run the build")
	target.journal.end()
	testkit.Eventually(t, "the follower to end with the journal", 5*time.Second, func() bool {
		return hasEventWith(f.View(), "voice/delegation/failed", "error", "the session journal ended")
	})
	callAgain(t, f, "call-a", "call-b")
	testkit.Eventually(t, "the shared turn's reply", 5*time.Second, func() bool {
		return spokenWith(f, "Tests and build passed.") > 0 && len(f.keptRequests()) == 0
	})
	typeTurn(target.journal, "typed-1", "Check the lint", "Lint is clean.")
	testkit.Eventually(t, "a later typed turn, read after catch-up", 5*time.Second, func() bool {
		return spokenWith(f, "Lint is clean.") == 1
	})
	if spokenWith(f, "Tests and build passed.") != 1 {
		t.Fatalf("spoken=%q", f.provider.spoken())
	}
}

func TestAPeerRequestStillRunningAfterADroppedCallIsFollowedByTheNextCall(t *testing.T) {
	briefer := fixedBriefer{outcomes: map[string]TurnOutcome{"peer-message": {TurnID: "peer-turn", Found: true}}}
	f, target := peerTarget(t, briefer, false)
	delegateToPeer(t, f, "voice-request-a", "Run the tests")
	target.journal.end()
	testkit.Eventually(t, "the follower to end with the journal", 5*time.Second, func() bool {
		return hasEventWith(f.View(), "voice/delegation/failed", "error", "the session journal ended")
	})
	callAgain(t, f, "call-a", "call-b")
	testkit.Eventually(t, "the next call to follow the peer", 5*time.Second, func() bool {
		kept := f.keptRequests()
		return len(kept) == 1 && kept[0].TurnID == "peer-turn" && f.followedTurns() == 1
	})
	peerReply(target.journal, "peer-turn", 1, "All tests passed.", true)
	endPeerTurn(target.journal, "peer-turn")
	testkit.Eventually(t, "the reply in the next call", 5*time.Second, func() bool {
		return len(delegationEvents(f, "voice-request-a", "peer-turn")) == 1
	})
	assertDrained(t, f)
}
