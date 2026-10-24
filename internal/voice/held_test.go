package voice

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func heldTarget(t *testing.T, target *targetFixture) *voiceFixture {
	t.Helper()
	target.quiet = true
	f := newFixture(t, fixtureOptions{target: target})
	f.target = target
	selectAndConnect(t, f, target)
	apply(t, f, Action{Action: "delegate", AttemptID: "call-a", MessageID: "voice-request-a", Text: "Run the tests"})
	if !hasEvent(f.View(), "voice/delegation/held") {
		t.Fatalf("the delegation was not held: %+v", f.View().Events)
	}
	return f
}

func heldTurn(j *targetJournal, turnID string, prompt sessionstream.Event) {
	prompt.ItemID, prompt.Version, prompt.Kind, prompt.TurnID = "prompt-"+turnID, 1, "message", turnID
	j.emit(prompt)
	j.emit(sessionstream.Event{ItemID: "reply-" + turnID, Version: 1, Kind: "text", Role: "assistant", TurnID: turnID, Body: "All tests passed.", Final: true})
	j.emit(sessionstream.Event{ItemID: "done-" + turnID, Version: 1, Kind: "done", TurnID: turnID})
}

// A typed turn after the held one is noted only once the follower has read past the held turn.
func assertOnlyTypedSpoken(t *testing.T, f *voiceFixture, j *targetJournal) {
	t.Helper()
	heldTurn(j, "typed-1", sessionstream.Event{Role: "user", MessageID: "someone-else", Body: "Check the build"})
	testkit.Eventually(t, "the typed turn's reply", 5*time.Second, func() bool {
		return spokenWith(f, "The session replied to what the person typed there") == 1
	})
	if spoken := f.provider.spoken(); len(spoken) != 2 || !strings.Contains(spoken[0], `"Check the build"`) {
		t.Fatalf("the held delegation was spoken or noted: %q", spoken)
	}
}

func TestAPeerHeldDelegationWhoseJournalWrapsThePromptStaysSilent(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "Peer", Transport: "uds"}, turnID: "peer-message"}
	f := heldTarget(t, target)
	heldTurn(target.journal, "held-turn", sessionstream.Event{Role: "peer", MessageID: "peer-message", Body: "[from Voice] Run the tests (reply with agenthail send)"})
	assertOnlyTypedSpoken(t, f, target.journal)
}

func TestAHeldDelegationThatLandsInAnotherSessionStaysSilentThere(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "Codex", Transport: "desktop"}, turnID: "moved-turn", movesTo: "target-b"}
	f := heldTarget(t, target)
	testkit.Eventually(t, "the follower for the session the request reached", 5*time.Second, func() bool {
		prepared, _ := target.journal.subscriptions()
		return prepared == 2
	})
	heldTurn(target.journal, "moved-turn", sessionstream.Event{Role: "user", Body: "<context>\nRun the tests\n</context>"})
	assertOnlyTypedSpoken(t, f, target.journal)
}

func TestAHeldDelegationStaysSilentAfterTheJournalIsFollowedAgain(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "Peer", Transport: "uds"}, turnID: "peer-message"}
	f := heldTarget(t, target)
	target.journal.emit(sessionstream.Event{Kind: "source-error", Reason: "upstream stopped"})
	testkit.Eventually(t, "the journal to be followed again", 5*time.Second, func() bool {
		prepared, _ := target.journal.subscriptions()
		return prepared == 2
	})
	heldTurn(target.journal, "held-turn", sessionstream.Event{Role: "peer", MessageID: "peer-message", Body: "Run the tests, from Voice"})
	assertOnlyTypedSpoken(t, f, target.journal)
}
