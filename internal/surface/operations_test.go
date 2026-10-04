package surface

import (
	"encoding/json"
	"testing"
)

func TestTurnOptionValidationRejectsSilentDowngrades(t *testing.T) {
	for _, options := range []TurnOptions{{Effort: "nonsense"}, {Mode: "random"}, {OutputSchema: json.RawMessage(`[]`)}, {OutputSchema: json.RawMessage(`null`)}, {ServiceTier: "unknown"}} {
		if options.Validate(KindCodex) == nil {
			t.Fatalf("accepted %+v", options)
		}
	}
	if (TurnOptions{Effort: "high"}).Validate(KindNotion) == nil {
		t.Fatal("Notion silently accepted Codex options")
	}
}

func TestEffectiveCapabilitiesKeepsClaudeTranscriptStream(t *testing.T) {
	base := Capabilities{Stream: true, Send: true}
	if got := EffectiveCapabilities(&Session{Surface: KindClaude, Transport: "uds", Transcript: "/tmp/session.jsonl", HasLocal: true}, base); !got.Stream {
		t.Fatal("reliable local Claude transcript should remain streamable")
	}
	if got := EffectiveCapabilities(&Session{Surface: KindClaude, Transport: "uds", Transcript: "/tmp/session.jsonl"}, base); got.Stream {
		t.Fatal("Claude UDS session without a local transcript should not claim stream support")
	}
}
