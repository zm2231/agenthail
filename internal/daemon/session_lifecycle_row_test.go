package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func TestSessionRowReportsTheLifecycleItsRuntimeSupports(t *testing.T) {
	_, registry, _, _, _ := daemonFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	adapter := surfaces.NewClaude(nil, home)
	d := New(registry, []surface.Surface{adapter})
	for runtime, want := range map[surface.RuntimeKind]map[string]bool{
		surface.RuntimeClaudeBackground: {"status": true, "logs": true, "stop": true, "resume": true},
		surface.RuntimeClaudeHost:       {"status": true, "logs": false, "stop": true, "resume": true},
		surface.RuntimeClaudeCLI:        {"status": false, "logs": false, "stop": false, "resume": false},
	} {
		row := d.catalogSessionProjection(adapter, surface.Session{ID: "s", Surface: surface.KindClaude, RuntimeKind: runtime, Transport: "uds"}, catalogIdentity{}, time.Now(), "", 0, false, DashboardConfig{})
		data, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Capabilities struct {
				Lifecycle map[string]bool `json:"lifecycle"`
			} `json:"capabilities"`
		}
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		if len(decoded.Capabilities.Lifecycle) != len(want) {
			t.Fatalf("%s lifecycle=%v, want %v", runtime, decoded.Capabilities.Lifecycle, want)
		}
		for operation, supported := range want {
			if got, ok := decoded.Capabilities.Lifecycle[operation]; !ok || got != supported {
				t.Fatalf("%s lifecycle=%v, want %v", runtime, decoded.Capabilities.Lifecycle, want)
			}
		}
	}
}
