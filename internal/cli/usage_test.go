package cli

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

type usageAccount struct {
	title string
	rows  []string
}

// usageTree reads writeUsage as it nests: a host unindented, its accounts at two spaces, their rows deeper.
func usageTree(out string) ([]string, map[string][]usageAccount) {
	var hosts []string
	accounts := map[string][]usageAccount{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		switch {
		case !strings.HasPrefix(line, " "):
			hosts = append(hosts, line)
		case len(hosts) == 0:
		case !strings.HasPrefix(line, "   "):
			host := hosts[len(hosts)-1]
			accounts[host] = append(accounts[host], usageAccount{title: strings.TrimSpace(line)})
		default:
			host := hosts[len(hosts)-1]
			if n := len(accounts[host]); n > 0 {
				accounts[host][n-1].rows = append(accounts[host][n-1].rows, strings.TrimSpace(line))
			}
		}
	}
	return hosts, accounts
}

func containsAll(line string, parts []string) bool {
	for _, part := range parts {
		if !strings.Contains(line, part) {
			return false
		}
	}
	return true
}

func requireAccount(t *testing.T, out, host string, title []string, rows ...[]string) {
	t.Helper()
	_, accounts := usageTree(out)
	var matched []usageAccount
	for _, account := range accounts[host] {
		if containsAll(account.title, title) {
			matched = append(matched, account)
		}
	}
	if len(matched) != 1 {
		t.Errorf("%s has %d accounts titled with %q, want 1:\n%s", host, len(matched), title, out)
		return
	}
	next := 0
	for _, parts := range rows {
		for next < len(matched[0].rows) && !containsAll(matched[0].rows[next], parts) {
			next++
		}
		if next == len(matched[0].rows) {
			t.Errorf("%s's account %q shows no row with %q after the ones before it:\n%s", host, title, parts, out)
			return
		}
		next++
	}
}

func accountTitles(out, host string) []string {
	_, accounts := usageTree(out)
	var titles []string
	for _, account := range accounts[host] {
		titles = append(titles, account.title)
	}
	return titles
}

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
	if hosts, _ := usageTree(out.String()); !slices.Equal(hosts, []string{"studio", "vps"}) {
		t.Fatalf("hosts %q, want studio then vps:\n%s", hosts, out.String())
	}
	if titles := accountTitles(out.String(), "studio"); len(titles) != 2 || !strings.HasPrefix(titles[0], "work@example.com") || titles[1] != "Codex" {
		t.Fatalf("studio's accounts %q, want the Claude account then Codex", titles)
	}
	requireAccount(t, out.String(), "studio", []string{"work@example.com (default)", "read 2m ago"},
		[]string{"session", "40%", "resets in 2h 12m"},
		[]string{"week", "93%", "resets in 3d 2h", "near its limit"},
		[]string{"sessions: @fixer (working)"})
	requireAccount(t, out.String(), "studio", []string{"Codex"}, []string{"sessions: Codex run"})
	requireAccount(t, out.String(), "vps", []string{"API key (default)"},
		[]string{"week, Fable", "0%", "resets in 3d 2h"},
		[]string{"Signed in with an API key; no usage reported."})
	if strings.Count(out.String(), "near its limit") != 1 {
		t.Fatalf("only the 93%% meter is near its limit:\n%s", out.String())
	}
	if strings.Contains(out.String(), "5%") {
		t.Fatal("a meter past its window is not printed")
	}
}

func TestUsageNamesARootByWhatItSignsInWith(t *testing.T) {
	var sets []usageSetView
	if err := json.Unmarshal([]byte(`[{"host":"cloud","hostId":"h3","provider":"claude","name":"default","login":"api_key","loginSource":"bedrock","root":"/c","meters":[],"problem":"Signed in with Amazon Bedrock; no usage reported."}]`), &sets); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	writeUsage(&out, sets, time.Now())
	requireAccount(t, out.String(), "cloud", []string{"Bedrock (default)"}, []string{"Signed in with Amazon Bedrock; no usage reported."})
}

func TestUsageListsWorkingSessionsFirstWithShortTitles(t *testing.T) {
	sessions := []usageSessionView{
		{Name: "Review every open pull request and summarise what is left to do"},
		{Name: "idle-2"}, {Name: "idle-3"}, {Name: "idle-4"}, {Name: "idle-5"}, {Name: "idle-6"},
		{Name: "@builder", Working: true},
	}
	want := "@builder (working), Review every open pull request and summ…, idle-2, idle-3, idle-4, +2 more"
	if got := usageSessionLine(sessions); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}
