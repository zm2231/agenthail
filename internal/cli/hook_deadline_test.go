package cli

import (
	"os"
	"testing"
	"time"
)

func TestEveryHookEndsAtItsLimitWhileTheRegistryIsBusy(t *testing.T) {
	const limit = 300 * time.Millisecond
	previous := claudeSessionContextLimit
	claudeSessionContextLimit = limit
	t.Cleanup(func() { claudeSessionContextLimit = previous })
	for name, hook := range map[string]struct {
		args  []string
		input string
	}{
		"session start": {[]string{"hook", "session-context", "--claude-pid", "4242"}, `{"session_id":"planner-conversation","hook_event_name":"SessionStart","source":"startup"}`},
		"prompt submit": {[]string{"hook", "session-context", "--claude-pid", "4242"}, `{"session_id":"planner-conversation","hook_event_name":"UserPromptSubmit"}`},
		"stop":          {[]string{"hook", "stop", "--surface", "claude"}, `{"session_id":"planner-conversation","hook_event_name":"Stop","turn_id":"turn-1"}`},
	} {
		t.Run(name, func(t *testing.T) {
			app, _, _ := cardFixture(t)
			if err := app.Registry.OpenTurn("planner-conversation", time.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := app.Registry.IssueSessionCredential("planner-conversation", time.Now()); err != nil {
				t.Fatal(err)
			}
			stdin, err := os.CreateTemp(t.TempDir(), "hook-input")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := stdin.WriteString(hook.input); err != nil {
				t.Fatal(err)
			}
			if _, err := stdin.Seek(0, 0); err != nil {
				t.Fatal(err)
			}
			previousStdin := os.Stdin
			os.Stdin = stdin
			t.Cleanup(func() { os.Stdin = previousStdin })
			holdRegistryWriteLock(t, app.Registry.Path())
			started := time.Now()
			if _, err := captureStdout(t, func() error { return app.Run(hook.args) }); err != nil {
				t.Fatal(err)
			}
			app.Close()
			elapsed := time.Since(started)
			if elapsed < limit-50*time.Millisecond {
				t.Fatalf("the hook ended after %s, before its registry write met the lock", elapsed)
			}
			if elapsed > limit+time.Second {
				t.Fatalf("the hook and its close took %s past a %s limit while the registry was busy", elapsed, limit)
			}
		})
	}
}
