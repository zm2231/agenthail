package daemon

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLauncherOptionsListAgentsAsAnArrayWhenALauncherHasNone(t *testing.T) {
	resolver := NewSessionTransportResolver(nil)
	resolver.launchers["external"] = &launcherFixture{id: "external"}
	encoded, err := json.Marshal(resolver.Options(t.Context()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"agents":[]`) {
		t.Fatalf("options = %s, want agents encoded as an empty array", encoded)
	}
}
