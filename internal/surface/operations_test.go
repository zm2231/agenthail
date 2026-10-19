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
