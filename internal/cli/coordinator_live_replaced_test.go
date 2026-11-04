package cli

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestASendToALiveReplacedLeadFromOutsideAnyAgentStaysWithThatLead(t *testing.T) {
	c := newCoordinatorCLI(t).withGoneCoordinator(t)
	c.register(t, surface.Session{ID: replacedConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: c.claude.PID, Status: surface.StatusBusy, Cwd: c.cwd, Transcript: c.claude.Transcript(replacedConversation, c.cwd)})
	c.claude.RecordStatus(replacedConversation, c.cwd, "", "busy", "", time.Now())
	c.register(t, surface.Session{ID: startedConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: exitedPID, Status: surface.StatusIdle, Cwd: c.cwd})
	if _, err := c.r.HandOverCoordinator(c.spec(t), goneConversation, replacedConversation, registry.CoordinatorHandover{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.r.HandOverCoordinator(c.spec(t), replacedConversation, startedConversation, registry.CoordinatorHandover{}); err != nil {
		t.Fatal(err)
	}
	output, err := c.run(t, "send", replacedConversation, "for the conversation I am looking at")
	if err != nil {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if sends := c.sends(t); len(sends) != 0 {
		t.Fatalf("the send went to the coordinator instead of the live session it addressed: %v", sends)
	}
	if c.r.QueueCount(replacedConversation) != 1 || c.r.QueueCount(startedConversation) != 0 {
		t.Fatalf("queued for addressed=%d current=%d output=%q", c.r.QueueCount(replacedConversation), c.r.QueueCount(startedConversation), output)
	}
}
