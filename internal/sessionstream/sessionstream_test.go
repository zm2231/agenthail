package sessionstream

import "testing"

func TestDecodePreservesTurnCallAndFinalSeparately(t *testing.T) {
	event, err := DecodePayload(7, []byte(`{"itemId":"item","providerKey":"provider","version":2,"kind":"text","turnId":"turn","callId":"tool-call","role":"assistant","body":"answer","final":true,"status":"completed"}`))
	if err != nil {
		t.Fatal(err)
	}
	if event.TurnID != "turn" || event.CallID != "tool-call" || !event.Final {
		t.Fatalf("event=%+v", event)
	}
	if event.Terminal() {
		t.Fatal("message final bit incorrectly ended the turn")
	}
}

func TestSourceErrorIsTerminalWithoutTurnIdentity(t *testing.T) {
	event, err := DecodePayload(9, []byte(`{"kind":"source-error","reason":"upstream stopped"}`))
	if err != nil {
		t.Fatal(err)
	}
	if event.TurnID != "" || !event.Terminal() || !event.Failed() {
		t.Fatalf("event=%+v", event)
	}
}

func TestSpokenAssistantContentExcludesReasoningAndToolCalls(t *testing.T) {
	for _, event := range []Event{
		{Role: "assistant", Kind: "reasoning"},
		{Role: "assistant", Kind: "tool_call"},
	} {
		if event.SpokenAssistantContent() {
			t.Fatalf("non-spoken event accepted: %+v", event)
		}
	}
	for _, kind := range []string{"message", "text", "assistant"} {
		if !(Event{Role: "assistant", Kind: kind}).SpokenAssistantContent() {
			t.Fatalf("spoken assistant kind rejected: %s", kind)
		}
	}
}
