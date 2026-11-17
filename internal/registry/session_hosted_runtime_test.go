package registry

import (
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestARegistrationCannotMoveAHostedProcessToTheTerminalRuntime(t *testing.T) {
	r := openTestRegistry(t)
	hosted := surface.Session{ID: "hosted", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Transport: "uds", PID: 4242, Status: surface.StatusIdle}
	if err := r.RecordObservedSession(hosted); err != nil {
		t.Fatal(err)
	}
	terminal := hosted
	terminal.RuntimeKind = surface.RuntimeClaudeCLI
	if err := r.RecordObservedSession(terminal); err != nil {
		t.Fatal(err)
	}
	if stored, err := r.Session(hosted.ID); err != nil || stored.RuntimeKind != surface.RuntimeClaudeHost {
		t.Fatalf("stored=%+v err=%v, want the same process kept on the host runtime", stored, err)
	}
	terminal.PID = 4343
	if err := r.RecordObservedSession(terminal); err != nil {
		t.Fatal(err)
	}
	if stored, err := r.Session(hosted.ID); err != nil || stored.RuntimeKind != surface.RuntimeClaudeCLI {
		t.Fatalf("stored=%+v err=%v, want a terminal process that took the conversation recorded as one", stored, err)
	}
}

func TestATerminalProcessThatReusesAHostedProcessesPidIsRecordedAsATerminal(t *testing.T) {
	r := openTestRegistry(t)
	hosted := surface.Session{ID: "reused", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Transport: "uds", PID: 4242, PIDIdentity: "started-1", Status: surface.StatusIdle}
	if err := r.RecordObservedSession(hosted); err != nil {
		t.Fatal(err)
	}
	unread := hosted
	unread.RuntimeKind, unread.PIDIdentity = surface.RuntimeClaudeCLI, ""
	if err := r.RecordObservedSession(unread); err != nil {
		t.Fatal(err)
	}
	if stored, err := r.Session(hosted.ID); err != nil || stored.RuntimeKind != surface.RuntimeClaudeHost {
		t.Fatalf("stored=%+v err=%v, want a listing that could not read the process taken as the same process", stored, err)
	}
	reused := hosted
	reused.RuntimeKind, reused.PIDIdentity = surface.RuntimeClaudeCLI, "started-2"
	if err := r.RecordObservedSession(reused); err != nil {
		t.Fatal(err)
	}
	if stored, err := r.Session(hosted.ID); err != nil || stored.RuntimeKind != surface.RuntimeClaudeCLI {
		t.Fatalf("stored=%+v err=%v, want a later process given the same pid recorded as a terminal", stored, err)
	}
}
