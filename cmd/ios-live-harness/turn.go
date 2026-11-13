package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
	ask          *liveAsk
	cancel       context.CancelFunc
}

type liveAsk struct {
	done     chan struct{}
	decision json.RawMessage
	err      error
}

type askStatus struct {
	Session  string          `json:"session"`
	Answered bool            `json:"answered"`
	Decision json.RawMessage `json:"decision,omitempty"`
	Error    string          `json:"error,omitempty"`
}

// The record goes to waiting first because the host holds a hook's prompt only while the session's record shows one.
func (h *host) askInTurn(command string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	turn := h.turn
	if turn == nil {
		return "", errors.New("no turn is open on this host")
	}
	if turn.ask != nil {
		select {
		case <-turn.ask.done:
		default:
			return "", errors.New("the open turn already waits on a prompt")
		}
	}
	token, err := os.ReadFile(filepath.Join(h.home, ".agenthail", "dashboard.token"))
	if err != nil {
		return "", fmt.Errorf("read the host's dashboard token: %w", err)
	}
	asked := time.Now()
	if err := turn.writeRecordWaiting("waiting", "permission prompt", asked); err != nil {
		return "", err
	}
	input, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(map[string]any{
		"prompt": map[string]any{"session_id": turn.conversation.id, "transcript_path": turn.conversation.transcript, "cwd": turn.conversation.cwd,
			"permission_mode": "default", "tool_name": "Bash", "tool_input": json.RawMessage(input)},
		"pid": turn.process.Process.Pid, "askedAt": asked.UnixMilli(),
	})
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithCancel(context.Background())
	turn.cancel = cancel
	ask := &liveAsk{done: make(chan struct{})}
	turn.ask = ask
	go func() {
		defer close(ask.done)
		ask.decision, ask.err = holdPrompt(ctx, h.endpoint(), strings.TrimSpace(string(token)), body)
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.turn == turn {
			turn.writeRecord("busy")
		}
	}()
	return turn.conversation.id, nil
}

func (h *host) askResult() (askStatus, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.turn == nil || h.turn.ask == nil {
		return askStatus{}, errors.New("no prompt was raised in an open turn on this host")
	}
	status := askStatus{Session: h.turn.conversation.id}
	select {
	case <-h.turn.ask.done:
		status.Answered = h.turn.ask.err == nil
		status.Decision = h.turn.ask.decision
		if h.turn.ask.err != nil {
			status.Error = h.turn.ask.err.Error()
		}
	default:
	}
	return status, nil
}

func holdPrompt(ctx context.Context, endpoint, token string, body []byte) (json.RawMessage, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/api/v1/claude/permission-request", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the host answered %s: %s", response.Status, strings.TrimSpace(string(data)))
	}
	var answer struct {
		Decision json.RawMessage `json:"decision"`
	}
	if err := json.Unmarshal(data, &answer); err != nil {
		return nil, err
	}
	if len(answer.Decision) == 0 || string(answer.Decision) == "null" {
		return nil, errors.New("the host settled the prompt without a decision")
	}
	return answer.Decision, nil
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
	if t.cancel != nil {
		t.cancel()
	}
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
	return t.writeRecordWaiting(status, "", time.Now())
}

func (t *liveTurn) writeRecordWaiting(status, waitingFor string, at time.Time) error {
	pid := t.process.Process.Pid
	record := map[string]any{"pid": pid, "sessionId": t.conversation.id, "cwd": t.conversation.cwd, "kind": "interactive", "entrypoint": "cli", "status": status,
		"messagingSocketPath": t.socket.Addr().String(), "peerFeatures": []string{"notify_idle"}, "statusUpdatedAt": at.UnixMilli(), "updatedAt": at.UnixMilli()}
	if waitingFor != "" {
		record["waitingFor"] = waitingFor
	}
	data, err := json.Marshal(record)
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
