package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestCoordinatorStatusAndListSayWhoReplacedTheLeadAndWhy(t *testing.T) {
	c := newCoordinatorCLI(t).withGoneCoordinator(t)
	c.register(t, surface.Session{ID: replacedConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: exitedPID, Status: surface.StatusIdle, Cwd: c.cwd})
	if _, err := c.r.HandOverCoordinator(c.spec(t), goneConversation, replacedConversation, registry.CoordinatorHandover{ChangedBy: "agenthail-person", Reason: "gone: conversation " + goneConversation + " no longer exists"}); err != nil {
		t.Fatal(err)
	}
	status, err := c.run(t, "coordinator", "--cwd", c.cwd)
	if err != nil || !strings.Contains(status, "replaced:") || !strings.Contains(status, "by the person, because conversation "+goneConversation+" no longer exists (was "+goneConversation+")") {
		t.Fatalf("status=%q err=%v", status, err)
	}
	list, err := c.run(t, "coordinator", "list")
	if err != nil || !strings.Contains(list, "replaced ") || !strings.Contains(list, "by the person") {
		t.Fatalf("list=%q err=%v", list, err)
	}
}

func TestAReplacedLeadsListRowSaysWhenItWasTheLead(t *testing.T) {
	from := time.Date(2026, 11, 3, 10, 0, 0, 0, time.Local)
	until := from.Add(time.Hour)
	detail := replacedLeadDetail([]registry.CoordinatorTenure{{Name: "work-lead", From: from, Until: until}})
	if detail != "Replaced lead, was @work-lead 2026-11-03 10:00:00 to 2026-11-03 11:00:00" {
		t.Fatalf("detail=%q", detail)
	}
	if replacedLeadDetail(nil) != "" {
		t.Fatal("a session that never led got a replaced-lead detail")
	}
}
