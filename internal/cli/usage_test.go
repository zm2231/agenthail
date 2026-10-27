package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestUsagePrintsEveryAccountWithItsSessions(t *testing.T) {
	now := time.Date(2026, 10, 23, 14, 0, 0, 0, time.UTC)
	var sets []usageSetView
	if err := json.Unmarshal([]byte(`[
	  {"host":"studio","hostId":"h1","local":true,"provider":"claude","name":"default","account":"work@example.com","root":"/a","primary":true,"asOf":"2026-10-23T13:58:00Z","meters":[
	    {"kind":"session","label":"Claude","percent":40,"resetsAt":"2026-10-23T16:12:00Z","until":"2026-10-23T16:12:00Z"},
	    {"kind":"weekly_all","label":"Claude week","percent":93,"resetsAt":"2026-10-26T16:00:00Z"}]},
	  {"host":"studio","hostId":"h1","local":true,"provider":"codex","meters":[{"kind":"weekly","label":"Codex week","percent":5,"until":"2026-10-23T13:00:00Z"}]},
	  {"host":"vps","hostId":"h2","provider":"claude","name":"default","login":"api_key","root":"/srv/claude","meters":[{"kind":"weekly_scoped","label":"Claude week, Fable","scope":"Fable","percent":0,"resetsAt":"2026-10-26T16:00:00Z"}],"problem":"Signed in with an API key; no usage reported."}
	]`), &sets); err != nil {
		t.Fatal(err)
	}
	var sessions []usageSnapshotSession
	if err := json.Unmarshal([]byte(`[
	  {"id":"s1","surface":"claude","name":"Fix it","alias":"fixer","status":"busy","host":{"id":"h1"},"claudeConfigRoot":{"root":"/a"}},
	  {"id":"s2","surface":"claude","name":"Old","status":"idle","host":{"id":"h1"},"claudeConfigRoot":{"root":"/a"}},
	  {"id":"s3","surface":"claude","name":"There","status":"idle","current":true,"host":{"id":"h2"},"claudeConfigRoot":{"root":"/a"}},
	  {"id":"s4","surface":"codex","name":"Codex run","status":"idle","current":true,"host":{"id":"h1"}}
	]`), &sessions); err != nil {
		t.Fatal(err)
	}
	for i := range sets {
		sets[i].Sessions = usageSessions(sets[i], sessions)
	}
	if got := len(sets[0].Sessions); got != 1 || sets[0].Sessions[0].Name != "@fixer" || !sets[0].Sessions[0].Working {
		t.Fatalf("a root lists its working or current sessions on its own host only: %+v", sets[0].Sessions)
	}
	if len(sets[1].Sessions) != 1 || len(sets[2].Sessions) != 0 {
		t.Fatalf("codex and other-host sessions: %+v %+v", sets[1].Sessions, sets[2].Sessions)
	}
	var out bytes.Buffer
	writeUsage(&out, sets, now)
	want := `studio
  work@example.com (default)  read 2m ago
    session       40%  resets in 2h 12m
    week          93%  resets in 3d 2h  near its limit
    sessions: @fixer (working)
  Codex
    sessions: Codex run
vps
  API key (default)
    week, Fable    0%  resets in 3d 2h
    Signed in with an API key; no usage reported.
`
	if out.String() != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out.String(), want)
	}
	if strings.Contains(out.String(), "5%") {
		t.Fatal("a meter past its window is not printed")
	}
}
