package cliproxyapi

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/proxyacct"
)

func TestDrainReadsWhatTheProxySelectorReads(t *testing.T) {
	var files authFiles
	if err := json.Unmarshal([]byte(`{"files":[
 {"name":"a.json","provider":"claude","email":"a@example.com","status":"active","quota_ceiling":{"5h":60,"7d":60,"7d_oi":0},
  "quota":{"observed_at":"2026-11-19T11:00:00Z","signals":{"Anthropic-Ratelimit-Unified-7d-Utilization":"0.30","Anthropic-Ratelimit-Unified-7d-Reset":"1795000000","Anthropic-Ratelimit-Unified-5h-Reset":"1794000000"}},
  "model_quotas":{
   "claude-opus":{"quota":{"observed_at":"2026-11-19T11:59:00Z","signals":{"anthropic-ratelimit-unified-7d-utilization":"0.95","anthropic-ratelimit-unified-7d-reset":"1793000000"}}},
   "claude-fable-old":{"quota":{"observed_at":"2026-11-19T10:00:00Z","signals":{"anthropic-ratelimit-unified-7d_oi-utilization":"0.10","anthropic-ratelimit-unified-7d_oi-reset":"1796000000"}}},
   "claude-fable":{"quota":{"observed_at":"2026-11-19T11:30:00Z","signals":{"anthropic-ratelimit-unified-7d_oi-utilization":"0.40","anthropic-ratelimit-unified-7d_oi-reset":"1797000000"}}}}},
 {"name":"c.json","provider":"codex","email":"c@example.com","status":"active",
  "quota":{"observed_at":"2026-11-19T11:00:00Z","signals":{"x-codex-secondary-used-percent":"23","x-codex-secondary-reset-at":"1795000000"}}}]}`), &files); err != nil {
		t.Fatal(err)
	}
	accounts := files.accounts(time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC))
	got := map[string]proxyacct.Drain{}
	for _, reading := range accounts[0].Drain {
		got[reading.Kind] = reading
	}
	if week := got[proxyacct.WindowWeek]; week.Percent != 30 || !week.HasPercent || week.ResetsAt.Unix() != 1795000000 || week.Ceiling != 60 {
		t.Fatalf("the week comes from the credential-wide headers, not a newer model reading: %+v", week)
	}
	if session := got[proxyacct.WindowSession]; session.HasPercent || session.ResetsAt.Unix() != 1794000000 || session.Ceiling != 60 {
		t.Fatalf("a reset with no percent is still a reading: %+v", session)
	}
	if fable := got[proxyacct.WindowWeekModel]; fable.Percent != 40 || fable.ResetsAt.Unix() != 1797000000 || fable.Ceiling != 0 {
		t.Fatalf("the per-model week comes from the newest model state that has it, and a ceiling of 0 is none: %+v", fable)
	}
	if len(accounts[1].Drain) != 0 {
		t.Fatalf("a Codex credential has no Anthropic headers for the selector to read: %+v", accounts[1].Drain)
	}
}
