package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestClaudeBackgroundCreationUsesReturnedIdentityAndLifecycle(t *testing.T) {
	home := t.TempDir()
	binary := filepath.Join(home, "claude")
	script := `#!/bin/sh
printf '%s\n' "$@" >> "$HOME/argv"
if [ "$1" = agents ]; then
 printf '[{"id":"ab12cd34","sessionId":"real-session-id","kind":"background","name":"builder","cwd":"%s","state":"done"}]\n' "$HOME"
elif [ "$1" = --bg ]; then
 printf '\033[32mbackgrounded · ab12cd34\033[0m\n'
else
 printf 'operation complete\n'
fi
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_BIN", binary)
	c := NewClaude("Default", home)
	session, turn, err := c.StartSession(context.Background(), surface.SessionStartOptions{Message: "--literal prompt", Cwd: home, Name: "builder", Worktree: "feature", TurnOptions: surface.TurnOptions{Effort: "high"}})
	if err != nil || session == nil || session.ID != "real-session-id" || turn != nil {
		t.Fatalf("session=%+v turn=%+v err=%v", session, turn, err)
	}
	for _, action := range []string{"status", "logs", "stop", "resume"} {
		if _, err := c.SessionAction(context.Background(), session, action); err != nil {
			t.Fatal(action, err)
		}
	}
	args, _ := os.ReadFile(filepath.Join(home, "argv"))
	if strings.Contains(string(args), "--session-id") || !strings.Contains(string(args), "--\n--literal prompt") || !strings.Contains(string(args), "--resume\nreal-session-id") {
		t.Fatalf("args=%s", args)
	}
	if _, err := c.SessionAction(context.Background(), &surface.Session{ID: "other"}, "stop"); err == nil {
		t.Fatal("unowned background session accepted")
	}
}

func TestClaudeMalformedBackgroundOutputIsUnknown(t *testing.T) {
	home := t.TempDir()
	binary := filepath.Join(home, "claude")
	os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'unexpected output\\n'\n"), 0700)
	t.Setenv("AGENTHAIL_CLAUDE_BIN", binary)
	session, _, err := NewClaude("", home).StartSession(context.Background(), surface.SessionStartOptions{Message: "hello", Cwd: home})
	if session != nil || !surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("session=%+v err=%v", session, err)
	}
}

type turnOptionsClient struct {
	methods []string
	params  []map[string]any
}

func (c *turnOptionsClient) Request(_ context.Context, method string, params map[string]any, _ time.Duration) (map[string]any, error) {
	c.methods = append(c.methods, method)
	c.params = append(c.params, params)
	if method == "thread/read" {
		return map[string]any{"result": map[string]any{"thread": map[string]any{"model": "active-model"}}}, nil
	}
	if method == "thread/start" {
		return map[string]any{"result": map[string]any{"thread": map[string]any{"id": "new"}, "cwd": "/tmp"}}, nil
	}
	return map[string]any{"result": map[string]any{"turn": map[string]any{"id": "turn"}}}, nil
}
func (*turnOptionsClient) Close() error { return nil }
func TestCodexTurnOptionsReachCreationAndPreserveActiveModel(t *testing.T) {
	client := &turnOptionsClient{}
	options := surface.TurnOptions{Effort: "high", Mode: "plan", ServiceTier: "fast", OutputSchema: json.RawMessage(`{"type":"object"}`)}
	_, _, err := NewCodex("").startSession(context.Background(), client, surface.SessionStartOptions{Message: "hello", TurnOptions: options})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(client.methods, ",") != "thread/start,thread/read,turn/start" {
		t.Fatal(client.methods)
	}
	params := client.params[2]
	mode := params["collaborationMode"].(map[string]any)
	settings := mode["settings"].(map[string]any)
	if settings["model"] != "active-model" || settings["reasoning_effort"] != "high" || mode["mode"] != "plan" || params["serviceTierForTurn"] != "fast" {
		t.Fatal(params)
	}
	encoded, _ := json.Marshal(params)
	if !strings.Contains(string(encoded), `"outputSchema":{"type":"object"}`) {
		t.Fatalf("schema was encoded as text: %s", encoded)
	}
}
func TestNativeQueueContract(t *testing.T) {
	session := &surface.Session{ID: "thread"}
	for _, action := range []string{"list", "add", "update", "delete", "reorder", "start"} {
		method, params, err := nativeQueueParams(session, surface.NativeQueueRequest{Action: action, Message: "hello", ID: "q1", IDs: []string{"q1"}, ClientID: "retry-key", Cursor: "page-2"})
		if err != nil || method != "thread/queue/"+action || params["threadId"] != "thread" {
			t.Fatalf("%s %v %v", method, params, err)
		}
	}
	for _, request := range []surface.NativeQueueRequest{{Action: "add", Message: "hello"}, {Action: "update", Message: "hello"}, {Action: "delete"}, {Action: "reorder"}, {Action: "bogus"}} {
		if _, _, err := nativeQueueParams(session, request); err == nil {
			t.Fatalf("invalid request accepted: %+v", request)
		}
	}
}
