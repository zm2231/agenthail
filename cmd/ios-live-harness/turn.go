package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// A harness-owned sleep stands in for the Claude process; the host reads its transcript and session record.
type liveTurn struct {
	process      *exec.Cmd
	socket       net.Listener
	record       string
	conversation conversation
	startedAt    time.Time
}

func (h *host) startTurn(prompt string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.turn != nil {
		return "", errors.New("a turn is already open on this host")
	}
	cwd := filepath.Join(h.root, "work", "live-turn")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		return "", err
	}
	id := uuid.NewString()
	dir := filepath.Join(h.home, ".claude", "projects", claudeProjectDirUnsafe.ReplaceAllString(cwd, "-"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	c := conversation{id: id, cwd: cwd, transcript: filepath.Join(dir, id+".jsonl")}
	if err := appendRecords(c.transcript, map[string]any{"type": "user", "uuid": uuid.NewString(), "sessionId": id, "cwd": cwd, "timestamp": now.Format(time.RFC3339Nano),
		"message": map[string]any{"role": "user", "content": prompt}}); err != nil {
		return "", err
	}
	process := exec.Command(filepath.Join(h.dir("bin"), "sleep"), "900")
	process.Env = []string{"PATH=" + h.dir("bin")}
	if err := process.Start(); err != nil {
		return "", err
	}
	turn := &liveTurn{process: process, conversation: c, startedAt: now}
	go process.Wait()
	pid := process.Process.Pid
	socketPath := filepath.Join(h.dir("sockets"), strconv.Itoa(pid)+".sock")
	socket, err := net.Listen("unix", socketPath)
	if err != nil {
		turn.end()
		return "", err
	}
	turn.socket = socket
	go refuseAll(socket)
	turn.record = filepath.Join(h.home, ".claude", "sessions", strconv.Itoa(pid)+".json")
	if err := os.MkdirAll(filepath.Dir(turn.record), 0o700); err != nil {
		turn.end()
		return "", err
	}
	if err := turn.writeRecord("busy"); err != nil {
		turn.end()
		return "", err
	}
	h.turn = turn
	return id, nil
}

func (h *host) finishTurn(text string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	turn := h.turn
	if turn == nil {
		return "", errors.New("no turn is open on this host")
	}
	c, now := turn.conversation, time.Now().UTC()
	reply := map[string]any{"type": "assistant", "uuid": uuid.NewString(), "sessionId": c.id, "cwd": c.cwd, "timestamp": now.Format(time.RFC3339Nano),
		"message": map[string]any{"id": "msg-" + uuid.NewString(), "role": "assistant", "model": "claude-sonnet-5-5", "stop_reason": "end_turn", "content": []map[string]any{{"type": "text", "text": text}}}}
	duration := map[string]any{"type": "system", "subtype": "turn_duration", "uuid": uuid.NewString(), "sessionId": c.id, "cwd": c.cwd,
		"timestamp": now.Add(time.Millisecond).Format(time.RFC3339Nano), "durationMs": now.Sub(turn.startedAt).Milliseconds()}
	if err := appendRecords(c.transcript, reply, duration); err != nil {
		return "", err
	}
	if err := turn.writeRecord("idle"); err != nil {
		return "", err
	}
	return c.id, nil
}

func (h *host) endTurn() {
	h.mu.Lock()
	turn := h.turn
	h.turn = nil
	h.mu.Unlock()
	if turn != nil {
		turn.end()
	}
}

func (t *liveTurn) end() {
	if t.socket != nil {
		t.socket.Close()
	}
	if t.record != "" {
		os.Remove(t.record)
	}
	if t.process.Process != nil {
		t.process.Process.Kill()
	}
}

func (t *liveTurn) writeRecord(status string) error {
	pid := t.process.Process.Pid
	at := time.Now().UnixMilli()
	data, err := json.Marshal(map[string]any{"pid": pid, "sessionId": t.conversation.id, "cwd": t.conversation.cwd, "kind": "interactive", "status": status,
		"messagingSocketPath": t.socket.Addr().String(), "peerFeatures": []string{"notify_idle"}, "statusUpdatedAt": at, "updatedAt": at})
	if err != nil {
		return err
	}
	staged := t.record + ".tmp"
	if err := os.WriteFile(staged, data, 0o600); err != nil {
		return err
	}
	return os.Rename(staged, t.record)
}

func appendRecords(path string, records ...map[string]any) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			return err
		}
		if _, err := file.Write(append(line, '\n')); err != nil {
			return fmt.Errorf("append to %s: %w", path, err)
		}
	}
	return nil
}
