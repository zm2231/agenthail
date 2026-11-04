package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (c *coordinatorCLI) withLiveReplacedLead(t *testing.T) *coordinatorCLI {
	t.Helper()
	c.withGoneCoordinator(t)
	c.register(t, surface.Session{ID: replacedConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: c.claude.PID, Status: surface.StatusBusy, Cwd: c.cwd, Transcript: c.claude.Transcript(replacedConversation, c.cwd)})
	c.claude.RecordStatus(replacedConversation, c.cwd, "", "busy", "", time.Now())
	c.register(t, surface.Session{ID: startedConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: exitedPID, Status: surface.StatusIdle, Cwd: c.cwd})
	if _, err := c.r.HandOverCoordinator(c.spec(t), goneConversation, replacedConversation, registry.CoordinatorHandover{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.r.HandOverCoordinator(c.spec(t), replacedConversation, startedConversation, registry.CoordinatorHandover{}); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestASendToALiveReplacedLeadFromOutsideAnyAgentStaysWithThatLead(t *testing.T) {
	c := newCoordinatorCLI(t).withLiveReplacedLead(t)
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

func TestAWorkersSendToALiveReplacedLeadGoesToTheCurrentLeadAndItsReceiptSaysSo(t *testing.T) {
	c := newCoordinatorCLI(t).withLiveReplacedLead(t)
	const worker = "fixture-worker"
	c.register(t, surface.Session{ID: worker, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Status: surface.StatusIdle})
	t.Setenv("AGENTHAIL_SESSION_ID", worker)
	c.answers(`{"ok":true,"result":{"evidence":"queued","status":"queued","sessionId":"` + startedConversation + `","queueId":6}}`)
	output, err := c.run(t, "send", replacedConversation, "done with the task", "--json")
	if err != nil {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if sends := c.sends(t); len(sends) != 1 || sends[0]["name"] != "fixture-lead" {
		t.Fatalf("sends=%v", sends)
	}
	var receipt struct {
		SessionID string `json:"sessionId"`
		MovedTo   string `json:"movedTo"`
		Detail    string `json:"detail"`
	}
	if err := json.Unmarshal([]byte(output), &receipt); err != nil {
		t.Fatalf("output=%q: %v", output, err)
	}
	if receipt.SessionID != startedConversation || receipt.MovedTo != "@fixture-lead" || !strings.Contains(receipt.Detail, "@fixture-lead") {
		t.Fatalf("receipt=%+v", receipt)
	}
}
