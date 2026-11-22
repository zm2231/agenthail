package cli

import (
	"fmt"
	"strings"
	"testing"
)

func TestThreadCleanSendsTheIgnoredFilesThePersonNamed(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, true)
	api.responses = map[string]string{"work-clean": `{"ok":true,"worktree":{"path":"/src/.worktrees/fix","branch":"fix","cleanup":"kept","cleanupReason":"ignored","keptPaths":["local.env","my notes.txt","extra.env"],"hint":{"reason":"It holds ignored files outside build and package folders.","command":"git -C /src/.worktrees/fix status --ignored"}}}`}
	output, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "clean", "vps::@builder", "--remove-ignored", "local.env", "--remove-ignored", "my notes.txt"})
	})
	if err != nil {
		t.Fatal(err)
	}
	actions, _ := api.recorded()
	if len(actions) != 1 || actions[0]["action"] != "work-clean" || fmt.Sprint(actions[0]["removeIgnored"]) != "[local.env my notes.txt]" {
		t.Fatalf("actions=%v", actions)
	}
	for _, want := range []string{"kept /src/.worktrees/fix: It holds ignored files outside build and package folders.", "  extra.env", "see it with: git -C /src/.worktrees/fix status --ignored", "--remove-ignored <path>"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output lacks %q:\n%s", want, output)
		}
	}
}
