package voice

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestOnlyTheFinalAnswerOfACodexTurnIsSpoken(t *testing.T) {
	f, target := peerTarget(t, nil, false)
	delegateToPeer(t, f, "voice-request-a", "Run the tests")
	peerPrompt(target.journal, "peer-turn", "peer-message", "Run the tests")
	target.journal.emit(sessionstream.Event{ItemID: "commentary", Version: 1, Kind: "message", Role: "assistant", Phase: "commentary", TurnID: "peer-turn", Body: "Checking the test runner first.", Final: true})
	target.journal.emit(sessionstream.Event{ItemID: "answer", Version: 1, Kind: "message", Role: "assistant", Phase: "final_answer", TurnID: "peer-turn", Body: "All tests passed.", Final: true})
	endPeerTurn(target.journal, "peer-turn")
	testkit.Eventually(t, "the final answer", 5*time.Second, func() bool {
		return spokenWith(f, "All tests passed.") > 0
	})
	assertDrained(t, f)
	if spokenWith(f, "Checking the test runner") != 0 {
		t.Fatalf("commentary was spoken: %q", f.provider.spoken())
	}
	if stages := delegationEvents(f, "voice-request-a", "peer-turn"); strings.Join(stages, ",") != "final" {
		t.Fatalf("stages=%v spoken=%q", stages, f.provider.spoken())
	}
}

func TestATypedCodexTurnIsSummarizedFromItsFinalAnswer(t *testing.T) {
	f, target := peerTarget(t, nil, false)
	target.journal.emit(sessionstream.Event{ItemID: "prompt-typed", Version: 1, Kind: "message", Role: "user", TurnID: "typed-turn", Body: "Check the build"})
	target.journal.emit(sessionstream.Event{ItemID: "answer", Version: 1, Kind: "message", Role: "assistant", Phase: "final_answer", TurnID: "typed-turn", Body: "The build is green.", Final: true})
	target.journal.emit(sessionstream.Event{ItemID: "commentary", Version: 1, Kind: "message", Role: "assistant", Phase: "commentary", TurnID: "typed-turn", Body: "Looking at one more log.", Final: true})
	target.journal.emit(sessionstream.Event{ItemID: "done-typed", Version: 1, Kind: "done", TurnID: "typed-turn"})
	testkit.Eventually(t, "the typed turn's reply", 5*time.Second, func() bool {
		return spokenWith(f, "The build is green.") > 0
	})
	if spokenWith(f, "Looking at one more log") != 0 {
		t.Fatalf("commentary was spoken as the typed turn's reply: %q", f.provider.spoken())
	}
}
