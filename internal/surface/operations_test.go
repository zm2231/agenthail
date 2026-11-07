package surface

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestTurnOptionValidationRejectsSilentDowngrades(t *testing.T) {
	for _, options := range []TurnOptions{{OutputSchema: json.RawMessage(`[]`)}, {OutputSchema: json.RawMessage(`null`)}, {ServiceTier: "unknown"}} {
		if options.Validate(KindCodex) == nil {
			t.Fatalf("accepted %+v", options)
		}
	}
	if (TurnOptions{ServiceTier: "fast"}).Validate(KindNotion) == nil {
		t.Fatal("Notion silently accepted Codex options")
	}
	for _, settings := range []LaunchSettings{{Effort: "nonsense"}, {Mode: "random"}} {
		if settings.ValidateCodex() == nil {
			t.Fatalf("accepted %+v", settings)
		}
	}
	if err := (LaunchSettings{Effort: "xhigh", Mode: "plan"}).ValidateCodex(); err != nil {
		t.Fatal(err)
	}
}

func TestDeliveryRefusedIsUnavailableAndDistinct(t *testing.T) {
	refused := DeliveryRefused(errors.New("Claude refused to resume s: Workspace not trusted"))
	if !IsDeliveryUnavailable(refused) || !IsDeliveryRefused(refused) || IsDeliveryHeld(refused) || !strings.Contains(refused.Error(), "Workspace not trusted") {
		t.Fatalf("refused=%v", refused)
	}
	if IsDeliveryRefused(DeliveryUnavailable(errors.New("offline"))) || DeliveryRefused(nil) != nil {
		t.Fatal("an ordinary unavailable delivery reads as refused")
	}
}
