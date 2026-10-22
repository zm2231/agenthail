package cli

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tidwall/gjson"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// Claude runs the hook from every config root that has it at once, each in
// its own process with its own registry connection.
func rootHooks(t *testing.T, store *registry.Registry, fake *cliSurface, roots int) func(input string) []string {
	t.Helper()
	apps := make([]*App, roots)
	for i := range apps {
		r, err := registry.Open(store.Path())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { r.Close() })
		apps[i] = &App{Registry: r, Surfaces: []SurfaceEntry{{Name: string(fake.kind), Surface: fake}}, DefaultTimeout: time.Second, catalogDaemonRunning: func() bool { return false }, daemonService: &fakeService{}}
	}
	return func(input string) []string {
		var mu sync.Mutex
		var printed []string
		var wg sync.WaitGroup
		for _, app := range apps {
			wg.Go(func() {
				output := app.sessionContext(strings.NewReader(input), surface.KindClaude, 4242, time.Now())
				if output == nil {
					return
				}
				mu.Lock()
				defer mu.Unlock()
				printed = append(printed, output["hookSpecificOutput"].(map[string]any)["additionalContext"].(string))
			})
		}
		wg.Wait()
		return printed
	}
}

func TestAClaudeSessionIsToldItsCardOncePerStartAndOncePerChange(t *testing.T) {
	fake := &cliSurface{kind: surface.KindClaude}
	app, store := cliFixture(t, fake)
	if err := store.RegisterSession(surface.Session{ID: "s1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAlias("builder", "s1"); err != nil {
		t.Fatal(err)
	}
	hooks := rootHooks(t, store, fake, 3)
	start := `{"session_id":"s1","hook_event_name":"SessionStart","source":"startup"}`
	prompt := `{"session_id":"s1","hook_event_name":"UserPromptSubmit","prompt":"hi"}`

	if printed := hooks(start); len(printed) != 1 || !strings.HasPrefix(printed[0], "[Agenthail] You are @builder (") || !strings.Contains(printed[0], "agenthail whoami") {
		t.Fatalf("a start printed %q, want one card", printed)
	}
	if printed := hooks(prompt); len(printed) != 0 {
		t.Fatalf("a prompt after the start printed %q, want nothing", printed)
	}
	if err := store.RegisterSession(surface.Session{ID: "s1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if printed := hooks(prompt); len(printed) != 0 {
		t.Fatalf("a prompt after a move to another folder the card doesn't show printed %q, want nothing", printed)
	}
	if err := store.SetAlias("reviewer", "s1"); err != nil {
		t.Fatal(err)
	}
	if printed := hooks(prompt); len(printed) != 1 || !strings.HasPrefix(printed[0], "[Agenthail] Your Agenthail context changed. You are @reviewer (") || strings.Contains(printed[0], "agenthail whoami") {
		t.Fatalf("a prompt after a rename printed %q, want one change line", printed)
	}
	if printed := hooks(prompt); len(printed) != 0 {
		t.Fatalf("the next prompt printed %q, want nothing", printed)
	}

	stdin, err := os.CreateTemp(t.TempDir(), "hook-input")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.WriteString(`{"session_id":"s1","hook_event_name":"SessionStart","source":"compact"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	previous := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() { os.Stdin = previous })
	output, err := captureStdout(t, func() error { return app.Run([]string{"hook", "session-context", "--claude-pid", "4242"}) })
	if err != nil {
		t.Fatal(err)
	}
	if gjson.Get(output, "hookSpecificOutput.hookEventName").String() != "SessionStart" || !strings.HasPrefix(gjson.Get(output, "hookSpecificOutput.additionalContext").String(), "[Agenthail] You are @reviewer (") {
		t.Fatalf("a compaction printed %s, want the card for Claude", output)
	}
}

func TestAClaudeSessionFirstSeenAtAPromptGetsItsCardAndAnUnknownOneNothing(t *testing.T) {
	fake := &cliSurface{kind: surface.KindClaude}
	_, store := cliFixture(t, fake)
	if err := store.RegisterSession(surface.Session{ID: "s2", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	hooks := rootHooks(t, store, fake, 2)
	if printed := hooks(`{"session_id":"s2","hook_event_name":"UserPromptSubmit","prompt":"hi"}`); len(printed) != 1 || !strings.HasPrefix(printed[0], "[Agenthail] You are session s2 (") {
		t.Fatalf("the first prompt printed %q, want one card", printed)
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit"} {
		if printed := hooks(`{"session_id":"elsewhere","hook_event_name":"` + event + `","source":"startup"}`); len(printed) != 0 {
			t.Fatalf("%s for an unknown session printed %q", event, printed)
		}
	}
}
