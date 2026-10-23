package daemon

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func TestCodexUsageAnswersTheAccountsLimitsFromItsRollouts(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	d.Surfaces = []surface.Surface{surfaces.NewCodex("")}
	api := signinAPIClient{t: t, handler: d.dashboardHandler(&dashboardServer{token: "secret"}), token: "secret"}
	var usage struct {
		Usage        *surface.Usage `json:"usage"`
		UsageProblem string         `json:"usageProblem"`
	}
	if code := api.do(http.MethodGet, "/api/v1/session-options/codex-usage", "", &usage); code != http.StatusOK || usage.Usage != nil || usage.UsageProblem != "No Codex usage recorded yet" {
		t.Fatalf("an empty Codex home: status=%d usage=%+v", code, usage)
	}

	path := filepath.Join(home, "sessions", "2026", "10", "23", "rollout.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	line := `{"timestamp":"2026-10-23T13:00:00Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":19.0,"window_minutes":10080,"resets_at":1793293773},"secondary":null,"credits":{"balance":"35057.13"}}}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if code := api.do(http.MethodGet, "/api/v1/session-options/codex-usage", "", &raw); code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	body, _ := raw["usage"].(map[string]any)
	limits, _ := body["limits"].([]any)
	first, _ := limits[0].(map[string]any)
	if len(limits) != 1 || first["kind"] != "weekly" || first["percent"] != 19.0 || body["fetchedAt"] != "2026-10-23T13:00:00Z" {
		t.Fatalf("usage=%+v", raw)
	}
	if _, leaked := body["credits"]; leaked {
		t.Fatalf("credits reached the wire: %+v", body)
	}
}
