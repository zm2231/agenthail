package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudepeer"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func TestATurnOpenedOnAHostReadsBusyUntilItFinishes(t *testing.T) {
	h := newTestHarness(t)
	handler := h.controlHandler(context.Background())
	host := h.hosts[0]
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(host.home, ".claude"))
	t.Setenv(claudepeer.SocketDirEnv, host.dir("sockets"))
	claude := surfaces.NewClaude(nil, host.home)

	id := control(t, handler, "/hosts/0/turn/start", `{"text":"Ship the release"}`)
	if again := post(handler, "/hosts/0/turn/start", `{"text":"Again"}`); again.Code != http.StatusConflict {
		t.Fatalf("a second open turn answered %d, want 409", again.Code)
	}
	session := listed(t, claude, id)
	if session.Status != surface.StatusBusy || session.Name != "Ship the release" {
		t.Fatalf("open turn listed as %q %q, want busy \"Ship the release\"", session.Status, session.Name)
	}
	observed, err := claude.Observe(context.Background(), &session)
	if err != nil || observed.Status != surface.StatusBusy || observed.ActiveTurnID == "" {
		t.Fatalf("open turn observed as %+v (%v), want busy with an active turn", observed, err)
	}

	if finished := control(t, handler, "/hosts/0/turn/finish", `{"text":"Released."}`); finished != id {
		t.Fatalf("finish named %q, want %q", finished, id)
	}
	session = listed(t, claude, id)
	observed, err = claude.Observe(context.Background(), &session)
	if err != nil || session.Status != surface.StatusIdle || observed.Status != surface.StatusIdle || observed.Reply == nil || observed.Reply.Text != "Released." {
		t.Fatalf("finished turn listed %q, observed %+v (%v); want idle with the reply", session.Status, observed, err)
	}

	pid := host.turn.process.Process.Pid
	record := host.turn.record
	host.endTurn()
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatalf("the session record outlived the turn: %v", err)
	}
	if err := waitGone(pid); err != nil {
		t.Fatal(err)
	}
}

func listed(t *testing.T, claude *surfaces.Claude, id string) surface.Session {
	t.Helper()
	sessions, err := claude.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		if session.ID == id {
			return session
		}
	}
	t.Fatalf("session %s is not listed among %d", id, len(sessions))
	return surface.Session{}
}

func post(handler http.Handler, path, body string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return response
}

func control(t *testing.T, handler http.Handler, path, body string) string {
	t.Helper()
	response := post(handler, path, body)
	var answer struct{ Session string }
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &answer) != nil || answer.Session == "" {
		t.Fatalf("%s answered %d: %s", path, response.Code, response.Body)
	}
	return answer.Session
}

func waitGone(pid int) error {
	for range 100 {
		if syscall.Kill(pid, 0) != nil {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("the turn's process %d is still running", pid)
}
