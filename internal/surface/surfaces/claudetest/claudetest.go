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
	"testing"
	"time"
)

const script = `#!/bin/sh
printf '%s\n' "$*" >> "$HOME/argv"
case "$1" in
agents)
 cat "$HOME/agents.json" 2>/dev/null || printf '[]\n'
 ;;
stop)
 printf 'stopped %s\n' "$2"
 ;;
--bg)
 if [ "$2" != --resume ]; then
  [ -f "$HOME/start-id" ] || exit 2
  sid=$(cat "$HOME/start-id")
  id=$(printf '%s' "$sid" | cut -c1-8)
  for prompt; do :; done
  printf '%s' "$prompt" > "$HOME/prompt-$sid"
  printf 'x' >> "$HOME/starts"
  printf '[{"id":"%s","sessionId":"%s","kind":"background","cwd":"%s","state":"working","pid":%s}]\n' "$id" "$sid" "$PWD" "$AGENTHAIL_FIXTURE_PID" > "$HOME/agents.json"
  mkdir -p "$HOME/.claude/sessions"
  printf '{"pid":%s,"sessionId":"%s","cwd":"%s","bridgeSessionId":"%s","status":"idle","peerFeatures":["notify_idle"],"startedAt":%s000}' "$AGENTHAIL_FIXTURE_PID" "$sid" "$PWD" "$(cat "$HOME/resume-bridge")" "$(date +%s)" > "$HOME/.claude/sessions/$AGENTHAIL_FIXTURE_PID.json"
  printf 'backgrounded · %s (working)\n' "$id"
  exit 0
 fi
 id=$(printf '%s' "$3" | cut -c1-8)
 if [ -f "$HOME/resume-copy" ]; then
  printf 'backgrounded · %s (idle - send a prompt to start)\n' "$(cat "$HOME/resume-copy")"
  exit 0
 fi
 if [ ! -f "$HOME/resume-never-ready" ]; then
  record="{\"pid\":$AGENTHAIL_FIXTURE_PID,\"sessionId\":\"$3\",\"cwd\":\"$PWD\",\"bridgeSessionId\":\"$(cat "$HOME/resume-bridge")\",\"messagingSocketPath\":\"$(cat "$HOME/resume-socket" 2>/dev/null)\",\"status\":\"idle\",\"peerFeatures\":[\"notify_idle\"],\"startedAt\":$(date +%s)000}"
  mkdir -p "$HOME/.claude/sessions"
  (sleep "$(cat "$HOME/resume-delay" 2>/dev/null || printf 0)"; printf '%s' "$record" > "$HOME/.claude/sessions/$AGENTHAIL_FIXTURE_PID.json") >/dev/null 2>&1 &
 fi
 printf 'backgrounded · %s (idle - send a prompt to start)\n' "$id"
 ;;
esac
`

// Fixture is a fake Claude Code installation. Processes it reports as alive
// carry the PID of a child process that lives for the whole test, never the
// test's own, which callers would take for their own ancestor.
type Fixture struct {
	Home   string
	PID    int
	socket string
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
	f := &Fixture{Home: home, PID: process.Process.Pid, t: t}
	binary := filepath.Join(f.Home, "claude")
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_BIN", binary)
	t.Setenv("AGENTHAIL_FIXTURE_PID", fmt.Sprint(f.PID))
	f.write("resume-bridge", "")
	return f
}

func (f *Fixture) write(name, content string) {
	f.t.Helper()
	path := filepath.Join(f.Home, name)
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
	name := filepath.Join(".claude", "projects", "fixture-project", conversation+".jsonl")
	f.write(name, string(user)+"\n"+string(assistant)+"\n"+string(duration)+"\n")
	return filepath.Join(f.Home, name)
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
	f.write(filepath.Join(".claude", "sessions", fmt.Sprint(f.PID)+".json"), string(data))
}

// JobState writes the record Claude keeps for background job id.
func (f *Fixture) JobState(id, state, detail string, updated time.Time) {
	f.t.Helper()
	data, _ := json.Marshal(map[string]any{"state": state, "detail": detail, "updatedAt": updated})
	f.write(filepath.Join(".claude", "jobs", id, "state.json"), string(data))
}

func (f *Fixture) RemoveRecord() {
	f.t.Helper()
	if err := os.Remove(filepath.Join(f.Home, ".claude", "sessions", fmt.Sprint(f.PID)+".json")); err != nil && !os.IsNotExist(err) {
		f.t.Fatal(err)
	}
}

// Listen gives the live process a messaging socket, which its records and
// resumed records then name.
func (f *Fixture) Listen() {
	f.t.Helper()
	if err := os.MkdirAll("/tmp/cc-socks", 0700); err != nil {
		f.t.Fatal(err)
	}
	path := filepath.Join("/tmp/cc-socks", fmt.Sprint(f.PID)+".sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = listener.Close() })
	f.socket = path
	f.write("resume-socket", path)
}

// ResumeBridge sets the Remote Control id the resumed process registers.
func (f *Fixture) ResumeBridge(bridge string) { f.write("resume-bridge", bridge) }

// ResumeDelay makes a resumed process write its record only after delay.
func (f *Fixture) ResumeDelay(delay time.Duration) {
	f.write("resume-delay", fmt.Sprintf("%.3f", delay.Seconds()))
}

// ResumeCopy makes claude report a copy under id instead of resuming.
func (f *Fixture) ResumeCopy(id string) { f.write("resume-copy", id) }

// ResumeNeverReady makes a resumed process never write its record.
func (f *Fixture) ResumeNeverReady() { f.write("resume-never-ready", "") }

// StartAs makes claude start new conversations under conversation, held by
// the live process with the Remote Control id ResumeBridge set.
func (f *Fixture) StartAs(conversation string) { f.write("start-id", conversation) }

// Starts counts the conversations claude started.
func (f *Fixture) Starts() int {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.Home, "starts"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		f.t.Fatal(err)
	}
	return len(data)
}

// Prompt returns the first message claude started conversation with.
func (f *Fixture) Prompt(conversation string) string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.Home, "prompt-"+conversation))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(data)
}

// Invocations counts claude invocations whose arguments equal args.
func (f *Fixture) Invocations(args ...string) int {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.Home, "argv"))
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
