package cli

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestBriefAsksTheLeadOfTheFoldersProjectOrTheNamedOne(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, true)
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"remote", "add", "origin", "https://github.com/example/widget.git"}} {
		if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, output)
		}
	}
	t.Chdir(repo)
	for _, key := range []string{"AGENTHAIL_SESSION_ID", "CODEX_THREAD_ID", "CLAUDE_CODE_SESSION_ID"} {
		t.Setenv(key, "")
	}
	reply := `{"ok":true,"ref":"vps::@widget-lead","result":{"evidence":"delivered","status":"sent"},"since":{"at":"2026-11-14T09:30:00Z","fallback":false}}`
	api.responses = map[string]string{"coordinator-brief": reply}

	output, err := captureStdout(t, func() error { return app.Run([]string{"brief"}) })
	if err != nil || !strings.Contains(output, "Asked vps::@widget-lead for a brief of everything since") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	output, err = captureStdout(t, func() error { return app.Run([]string{"brief", "gadget", "--json"}) })
	var answer struct {
		Ref   string `json:"ref"`
		Since struct {
			At time.Time `json:"at"`
		} `json:"since"`
	}
	if err != nil || json.Unmarshal([]byte(output), &answer) != nil || answer.Ref != "vps::@widget-lead" || !answer.Since.At.Equal(time.Date(2026, 11, 14, 9, 30, 0, 0, time.UTC)) {
		t.Fatalf("json output=%q err=%v", output, err)
	}
	actions, _ := api.recorded()
	if len(actions) != 2 || actions[0]["action"] != "coordinator-brief" || actions[0]["project"] != "repo:github.com/example/widget" || actions[1]["project"] != "gadget" || actions[0]["message"] != nil {
		t.Fatalf("actions=%v", actions)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"brief", "a", "b"}) }); err == nil || !strings.Contains(err.Error(), briefUsage) {
		t.Fatalf("two projects: %v", err)
	}
}
