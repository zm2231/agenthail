package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestPreferNamesAnAccountByLabelOrIDAndRefusesAnAmbiguousOne(t *testing.T) {
	var sets []usageSetView
	json.Unmarshal([]byte(`[
	  {"host":"server","hostId":"h2","provider":"proxy","name":"shared","proxy":{"name":"shared","id":"http://proxy.example:8317"},"accounts":[
	    {"id":"claude-ops.json","provider":"claude","label":"ops@example.com"},
	    {"id":"claude-lead.json","provider":"claude","label":"lead@example.com"},
	    {"id":"codex-ops.json","provider":"codex","label":"ops@example.com"}]}
	]`), &sets)
	views := proxyViews(sets)
	if proxy, account, err := resolvePreferTarget(views, "shared", "lead"); err != nil || proxy != "http://proxy.example:8317" || account != "claude-lead.json" {
		t.Fatalf("%s %s %v", proxy, account, err)
	}
	if _, account, err := resolvePreferTarget(views, "http://proxy.example:8317", "codex-ops.json"); err != nil || account != "codex-ops.json" {
		t.Fatalf("%s %v", account, err)
	}
	if _, _, err := resolvePreferTarget(views, "shared", "ops@example.com"); err == nil || !strings.Contains(err.Error(), "claude-ops.json (claude), codex-ops.json (codex)") {
		t.Fatalf("err=%v", err)
	}
	if _, _, err := resolvePreferTarget(views, "other", "lead"); err == nil {
		t.Fatal("an unknown proxy was accepted")
	}
}

func TestThePreferPlanShowsEveryChangeAndWhatItReaches(t *testing.T) {
	var plan preferPlanView
	json.Unmarshal([]byte(`{"proxy":{"name":"shared","id":"p"},"host":"server","account":{"id":"claude-spare.json","label":"spare@example.com"},"set":"only","now":true,
	  "changes":[{"id":"claude-spare.json","label":"spare@example.com","before":0,"after":10},{"id":"claude-lead.json","label":"lead@example.com","before":10,"after":0}],
	  "release":[{"id":"claude-lead.json","label":"lead@example.com"}],
	  "hosts":[{"host":"laptop","roots":["/home/dev/.work/.claude"],"sessions":[{"name":"migrator"}]}],
	  "warnings":["The proxy does not say which account each session is on."],"confirm":"Prefer only spare@example.com (turns off lead@example.com) and move up to 1 running session","planId":"x"}`), &plan)
	var out bytes.Buffer
	writePreferPlan(&out, plan)
	want := `shared proxy, read from server
  spare@example.com        not preferred -> preferred
  lead@example.com         preferred -> not preferred
  releases the sessions on lead@example.com, so they move on their next request
affects
  laptop: /home/dev/.work/.claude; running: migrator
The proxy does not say which account each session is on.
`
	if out.String() != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out.String(), want)
	}
	if !confirmLine(strings.NewReader("y\n"), &out, "") || confirmLine(strings.NewReader("\n"), &out, "") {
		t.Fatal("only y confirms")
	}
}
