// Package claudetest fakes the parts of Claude Code that Agenthail reads and
// runs: the claude binary, its session records, background jobs and
// transcripts, all under a temporary home.
package claudetest

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const script = `#!/bin/sh
root="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"
mkdir -p "$root"
printf '%s\n' "$*" >> "$HOME/argv"
printf '%s\n' "$*" >> "$root/argv"
case "$1" in
agents)
 cat "$root/agents.json" 2>/dev/null || printf '[]\n'
 ;;
stop)
 printf 'stopped %s\n' "$2"
 ;;
esac
`

// Fixture is a fake Claude Code installation. Processes it reports as alive
// carry the PID of a child process that lives for the whole test, never the
// test's own, which callers would take for their own ancestor. Records, jobs
// and transcripts go under Root, the Claude config root, which starts as the
// primary one in Home.
type Fixture struct {
	Home   string
	Root   string
	PID    int
	socket string
	roots  []string
	t      testing.TB
}

func New(t testing.TB) *Fixture {
	t.Helper()
	process := exec.Command("sleep", "3600")
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = process.Process.Kill()
		_ = process.Wait()
	})
	// A short home keeps sockets under it within the 104-byte path limit.
	home, err := os.MkdirTemp("", "claude")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	f := &Fixture{Home: home, Root: filepath.Join(home, ".claude"), PID: process.Process.Pid, t: t}
	binary := filepath.Join(f.Home, "claude")
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_BIN", binary)
	return f
}

// AddRoot lists a further Claude config root, named under Home, in the
// Agenthail config and makes it the root the fixture writes to.
func (f *Fixture) AddRoot(name string) string {
	f.t.Helper()
	root := filepath.Join(f.Home, name)
	f.roots = append(f.roots, root)
	data, _ := json.Marshal(map[string]any{"claudeConfigDirs": f.roots})
	if err := os.MkdirAll(filepath.Join(f.Home, ".agenthail"), 0700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.Home, ".agenthail", "config.json"), data, 0600); err != nil {
		f.t.Fatal(err)
	}
	f.Root = root
	return root
}

func (f *Fixture) write(name, content string) {
	f.t.Helper()
	path := filepath.Join(f.Root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		f.t.Fatal(err)
	}
}

// Transcript writes a conversation whose last turn completed and returns its
// path.
func (f *Fixture) Transcript(conversation, cwd string) string {
	f.t.Helper()
	at := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	user, _ := json.Marshal(map[string]any{"type": "user", "uuid": "fixture-user", "sessionId": conversation, "cwd": cwd, "timestamp": at, "message": map[string]any{"role": "user", "content": "fixture question"}})
	assistant, _ := json.Marshal(map[string]any{"type": "assistant", "uuid": "fixture-assistant", "sessionId": conversation, "cwd": cwd, "timestamp": at, "message": map[string]any{"id": "fixture-message", "stop_reason": "end_turn", "content": []map[string]any{{"type": "text", "text": "fixture answer"}}}})
	duration, _ := json.Marshal(map[string]any{"type": "system", "subtype": "turn_duration", "sessionId": conversation, "cwd": cwd, "timestamp": at})
	name := filepath.Join("projects", "fixture-project", conversation+".jsonl")
	f.write(name, string(user)+"\n"+string(assistant)+"\n"+string(duration)+"\n")
	return filepath.Join(f.Root, name)
}

// Job lists a background job in the catalog that claude agents prints; pid 0
// means the job has no process.
func (f *Fixture) Job(id, conversation, cwd, state string, pid int) {
	f.t.Helper()
	job := map[string]any{"id": id, "sessionId": conversation, "kind": "background", "cwd": cwd, "state": state}
	if pid > 0 {
		job["pid"] = pid
	}
	data, _ := json.Marshal([]map[string]any{job})
	f.write("agents.json", string(data))
}

// Record writes the session record of a live process holding conversation;
// an empty bridge leaves it without Remote Control or a messaging socket.
func (f *Fixture) Record(conversation, cwd, bridge string) {
	f.t.Helper()
	f.RecordStatus(conversation, cwd, bridge, "idle", "", time.Time{})
}

// RecordStatus writes the record with the status Claude reports, such as
// busy, or waiting with waitingFor "input needed" or "permission prompt"
// since the given time.
func (f *Fixture) RecordStatus(conversation, cwd, bridge, status, waitingFor string, since time.Time) {
	f.t.Helper()
	record := map[string]any{"pid": f.PID, "sessionId": conversation, "cwd": cwd, "status": status, "peerFeatures": []string{"notify_idle"}, "startedAt": time.Now().UnixMilli()}
	if waitingFor != "" {
		record["waitingFor"] = waitingFor
	}
	if !since.IsZero() {
		record["statusUpdatedAt"] = since.UnixMilli()
	}
	if bridge != "" {
		record["bridgeSessionId"] = bridge
	}
	if f.socket != "" {
		record["messagingSocketPath"] = f.socket
	}
	data, _ := json.Marshal(record)
	f.write(filepath.Join("sessions", fmt.Sprint(f.PID)+".json"), string(data))
}

// JobState writes the record Claude keeps for background job id.
func (f *Fixture) JobState(id, state, detail string, updated time.Time) {
	f.t.Helper()
	data, _ := json.Marshal(map[string]any{"state": state, "detail": detail, "updatedAt": updated})
	f.write(filepath.Join("jobs", id, "state.json"), string(data))
}

func (f *Fixture) RemoveRecord() {
	f.t.Helper()
	if err := os.Remove(filepath.Join(f.Root, "sessions", fmt.Sprint(f.PID)+".json")); err != nil && !os.IsNotExist(err) {
		f.t.Fatal(err)
	}
}

// Listen gives the live process a messaging socket, which its records and
// resumed records then name.
func (f *Fixture) Listen() {
	f.t.Helper()
	path := ClaimSocket(f.t, f.PID)
	listener, err := net.Listen("unix", path)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = listener.Close() })
	f.socket = path
}

// ClaimSocket returns the messaging socket path Claude uses for pid, which
// the test owns. Claude names the socket by its pid, so a file there that
// nothing answers was left by an earlier process with the same pid and is
// removed; one that answers is a live socket the test did not close.
func ClaimSocket(t testing.TB, pid int) string {
	t.Helper()
	if err := os.MkdirAll("/tmp/cc-socks", 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("/tmp/cc-socks", fmt.Sprint(pid)+".sock")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return path
	}
	if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
		conn.Close()
		t.Fatalf("a live socket already serves %s", path)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return path
}

// Invocations counts claude invocations whose arguments equal args.
func (f *Fixture) Invocations(args ...string) int {
	f.t.Helper()
	return f.count(filepath.Join(f.Home, "argv"), args)
}

// InvocationsIn counts claude invocations run against a config root.
func (f *Fixture) InvocationsIn(root string, args ...string) int {
	f.t.Helper()
	return f.count(filepath.Join(root, "argv"), args)
}

func (f *Fixture) count(path string, args []string) int {
	f.t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		f.t.Fatal(err)
	}
	want := strings.Join(args, " ")
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if line == want {
			count++
		}
	}
	return count
}

// PeerManager answers the daemon's Claude peer manager socket under a home,
// accepting every send and recording it.
type PeerManager struct {
	mu       sync.Mutex
	messages []string
	sends    []PeerSend
}

// PeerSend is one send the manager took: the sender it frames the message
// from, the target socket and the message.
type PeerSend struct {
	Source  string `json:"sourceSessionId"`
	Target  string `json:"targetSocket"`
	Message string `json:"message"`
}

func ListenPeerManager(t testing.TB, home string) *PeerManager {
	t.Helper()
	manager := &PeerManager{}
	path := filepath.Join(home, ".agenthail", "claude-peers.sock")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			var request PeerSend
			if json.NewDecoder(conn).Decode(&request) == nil {
				manager.mu.Lock()
				manager.messages = append(manager.messages, request.Message)
				manager.sends = append(manager.sends, request)
				id := fmt.Sprintf("peer-%d", len(manager.messages))
				manager.mu.Unlock()
				_ = json.NewEncoder(conn).Encode(map[string]any{"ok": true, "result": map[string]any{"uuid": id, "accepted": true}})
			}
			_ = conn.Close()
		}
	}()
	return manager
}

// Count is how many sends carried exactly message.
func (m *PeerManager) Count(message string) int {
	return m.matching(func(sent string) bool { return sent == message })
}

// Containing is how many sends contained text.
func (m *PeerManager) Containing(text string) int {
	return m.matching(func(sent string) bool { return strings.Contains(sent, text) })
}

// Sends are the sends the manager took, in order.
func (m *PeerManager) Sends() []PeerSend {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]PeerSend(nil), m.sends...)
}

func (m *PeerManager) Messages() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.messages...)
}

func (m *PeerManager) matching(match func(string) bool) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, sent := range m.messages {
		if match(sent) {
			count++
		}
	}
	return count
}
