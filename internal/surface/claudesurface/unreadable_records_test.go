package claudesurface

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/claudesurface/claudetest"
	"github.com/zm2231/agenthail/internal/testkit"
)

func holderRecordPath(fixture *claudetest.Fixture, holder *claudetest.Holder) string {
	return filepath.Join(fixture.Root, "sessions", fmt.Sprint(holder.PID)+".json")
}

func TestClaudeSendReachesATerminalWhoseRecordHasAStrayTail(t *testing.T) {
	fixture, host, c := hostResume(t)
	holder := fixture.Holder(fixtureConversation, fixture.Home)
	path := holderRecordPath(fixture, holder)
	record, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(record, '}', '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	writeNativeSender(t, fixture.Root, "sender-native")
	session := exitedSession(fixture)
	if _, err := c.Send(surface.WithSourceSessionID(context.Background(), "sender-native"), &session, "hello"); err != nil {
		t.Fatalf("err=%v", err)
	}
	testkit.Eventually(t, "the terminal to take the message", 5*time.Second, func() bool {
		received := holder.Received()
		return len(received) == 1 && strings.Contains(received[0], "hello")
	})
	if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 0 {
		t.Fatalf("a terminal's conversation was opened in the host too: %+v", spawns)
	}
}

func TestClaudeSendIsHeldWithoutAResumeWhileALiveProcessRecordIsUnreadable(t *testing.T) {
	fixture, host, c := hostResume(t)
	holder := fixture.Holder(fixtureConversation, fixture.Home)
	if err := os.WriteFile(holderRecordPath(fixture, holder), []byte(`{"pid":`), 0o600); err != nil {
		t.Fatal(err)
	}
	session := exitedSession(fixture)
	_, err := c.Send(context.Background(), &session, "hello")
	if !surface.IsDeliveryHeld(err) || !strings.Contains(err.Error(), fmt.Sprintf("Claude process %d", holder.PID)) {
		t.Fatalf("err=%v, want it held naming process %d", err, holder.PID)
	}
	if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 0 {
		t.Fatalf("resumed while a live process might hold it: %+v", spawns)
	}

	holder.Exit()
	if _, err := c.SendHumanTurn(context.Background(), &session, "hello", nil); err != nil {
		t.Fatalf("after the process exited err=%v", err)
	}
	if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 1 {
		t.Fatalf("spawns=%+v, want one resume once the process exited", spawns)
	}
}

func TestClaudeAHolderIsNotTheLaterProcessThatReusedItsPid(t *testing.T) {
	fixture := claudetest.New(t)
	holder := fixture.Holder(fixtureConversation, fixture.Home)
	command := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(holder.PID))
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	start, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	record, _ := json.Marshal(map[string]any{"pid": holder.PID, "sessionId": fixtureConversation, "cwd": fixture.Home, "status": "idle", "version": "2.1.270", "procStart": string(start)})
	if err := os.WriteFile(holderRecordPath(fixture, holder), record, 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewClaude(nil, fixture.Home)
	if !c.StillHolds(fixtureConversation, surface.Holder{PID: holder.PID, Start: string(start)}) {
		t.Fatal("the process that wrote the record does not hold its conversation")
	}
	if c.StillHolds(fixtureConversation, surface.Holder{PID: holder.PID, Start: "an earlier process"}) {
		t.Fatal("a later process under the same pid counted as the holder that closed")
	}
}
