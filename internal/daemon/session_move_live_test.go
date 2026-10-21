package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	liveFirstTurn = "This is a throwaway test conversation. Remember the codeword PLUM-4417. " +
		"Create one task with the subject probe-task-alpha using your task tool (TaskCreate, or TodoWrite if that is the one you have). " +
		"Do not read or change any files. Then reply with STORED-7Q and the name of the task tool you used."
	liveSecondTurn = "Call TaskList now (load it with ToolSearch first if you need to), and read or change no files. " +
		"Then answer in one line as CODEWORD=<the codeword I gave you> TASKS=<the subjects TaskList returned>."
)

// TestALiveClaudeConversationKeepsItsHistoryAndTasksAcrossAMove moves a
// conversation the real Claude CLI ran from one daemon to another and runs a
// turn there that needs the conversation's history and tasks. It runs only
// when AGENTHAIL_MOVE_LIVE_CLAUDE names the CLI and
// AGENTHAIL_MOVE_LIVE_SETTINGS names the settings.json both throwaway config
// roots start from.
func TestALiveClaudeConversationKeepsItsHistoryAndTasksAcrossAMove(t *testing.T) {
	cli, settingsPath := os.Getenv("AGENTHAIL_MOVE_LIVE_CLAUDE"), os.Getenv("AGENTHAIL_MOVE_LIVE_SETTINGS")
	if cli == "" || settingsPath == "" {
		t.Skip("set AGENTHAIL_MOVE_LIVE_CLAUDE and AGENTHAIL_MOVE_LIVE_SETTINGS to move a conversation the real Claude CLI runs")
	}
	settings, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if err := os.Unsetenv("CLAUDE_CONFIG_DIR"); err != nil {
		t.Fatal(err)
	}
	live := func() *moveNode {
		host := claudehosttest.New(t, "")
		host.Claude = cli
		root := filepath.Join(host.Home, ".claude")
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "settings.json"), settings, 0600); err != nil {
			t.Fatal(err)
		}
		return moveNodeOn(t, host)
	}
	source, target, _, checkout := pairMoveNodes(t, live(), live())
	var clis []int
	t.Cleanup(func() { stopLiveCLIs(t, cli, clis) })
	ctx := context.Background()

	started, _, err := source.claude.StartSession(ctx, surface.SessionStartOptions{Message: liveFirstTurn, Cwd: source.host.Cwd, PermissionMode: "acceptEdits"})
	if err != nil {
		t.Fatal(err)
	}
	session := hostedSession(t, source.claude, started.ID)
	clis = append(clis, session.PID)
	if err := source.reg.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	t.Logf("source: %s", awaitAssistantText(t, source.host.Home, session.ID, "STORED-7Q"))
	t.Logf("source tasks/%s: %v", session.ID, filesUnder(t, filepath.Join(source.host.Home, ".claude", "tasks", session.ID)))

	result, err := source.daemon.moveSession(ctx, sessionMoveRequest{SessionID: session.ID, Host: "forge", WaitSeconds: 180})
	if err != nil {
		t.Fatal(err)
	}
	if result.Move.State != registry.MoveDone {
		t.Fatalf("result=%+v", result)
	}
	moved := hostedSession(t, target.claude, session.ID)
	clis = append(clis, moved.PID)
	if moved.Cwd != checkout {
		t.Fatalf("the target's Claude host runs the session in %s, want %s", moved.Cwd, checkout)
	}
	if _, err := target.reg.Session(session.ID); err != nil {
		t.Fatalf("the target does not list the session: %v", err)
	}
	t.Logf("target tasks/%s: %v", session.ID, filesUnder(t, filepath.Join(target.host.Home, ".claude", "tasks", session.ID)))
	transcript := claudeTranscriptIn(t, target.host.Home, session.ID)
	arrived, err := os.Stat(transcript)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := target.claude.SendHumanTurn(ctx, &moved, liveSecondTurn, nil); err != nil {
		t.Fatal(err)
	}
	reply := awaitAssistantText(t, target.host.Home, session.ID, "CODEWORD=")
	t.Logf("target: %s", reply)
	if !strings.Contains(reply, "PLUM-4417") {
		t.Errorf("the turn on the target did not see the conversation's history: %q", reply)
	}
	if !toolResultAfter(t, transcript, arrived.Size(), "probe-task-alpha") {
		t.Errorf("no tool on the target returned the conversation's task: %q", reply)
	}
}

// toolResultAfter reports whether a tool result past offset in the
// transcript at path mentions text.
func toolResultAfter(t *testing.T, path string, offset int64, text string) bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || int64(len(data)) < offset {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, line := range bytes.Split(data[offset:], []byte("\n")) {
		var record struct {
			Type    string `json:"type"`
			Message struct {
				Content []struct {
					Type    string          `json:"type"`
					Content json.RawMessage `json:"content"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &record) != nil || record.Type != "user" {
			continue
		}
		for _, block := range record.Message.Content {
			if block.Type == "tool_result" && bytes.Contains(block.Content, []byte(text)) {
				return true
			}
		}
	}
	return false
}

// awaitAssistantText waits for an assistant message in the conversation's
// transcript under home that contains marker, and returns its text.
func awaitAssistantText(t *testing.T, home, conversation, marker string) string {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Minute); time.Now().Before(deadline); time.Sleep(time.Second) {
		data, err := os.ReadFile(claudeTranscriptIn(t, home, conversation))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 1<<20), 64<<20)
		for scanner.Scan() {
			var record struct {
				Type    string `json:"type"`
				Message struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(scanner.Bytes(), &record) != nil || record.Type != "assistant" {
				continue
			}
			for _, block := range record.Message.Content {
				if block.Type == "text" && strings.Contains(block.Text, marker) {
					return block.Text
				}
			}
		}
	}
	t.Fatalf("no assistant message in %s contains %q", conversation, marker)
	return ""
}

func filesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			files = append(files, rel)
		}
		return nil
	})
	return files
}

// stopLiveCLIs stops the Claude CLIs this test started that are still
// running, checking each pid still runs cli so a reused pid is left alone.
func stopLiveCLIs(t *testing.T, cli string, pids []int) {
	for _, pid := range pids {
		output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
		if err == nil && strings.Contains(string(output), filepath.Base(cli)) {
			_ = syscall.Kill(pid, syscall.SIGTERM)
			t.Logf("stopped Claude %d", pid)
		}
	}
}
