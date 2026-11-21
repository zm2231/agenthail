package cli

import (
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/dashconfig"
)

func TestOnlyThePersonChangesTheAgentPolicyAndAnAgentCanReadIt(t *testing.T) {
	f := newGatedFixture(t)
	t.Setenv("AGENTHAIL_SESSION_ID", "widget-lead")
	if err := f.app.Run([]string{"agent-policy", "set", "project-remove", "allow"}); err == nil || !strings.Contains(err.Error(), "the person's to change") {
		t.Fatalf("an agent loosening its own policy: %v", err)
	}
	output, err := captureStdout(t, func() error { return f.app.Run([]string{"agent-policy"}) })
	if err != nil || !strings.Contains(output, "project-remove") {
		t.Fatalf("an agent reading the policy: %q %v", output, err)
	}

	t.Setenv("AGENTHAIL_SESSION_ID", "")
	if _, err := captureStdout(t, func() error { return f.app.Run([]string{"agent-policy", "set", "project-remove", "allow"}) }); err != nil {
		t.Fatal(err)
	}
	config, err := dashconfig.Load()
	if err != nil || config.Agents.Decision("project-remove") != dashconfig.AgentAllow {
		t.Fatalf("the person's choice was stored as %+v (%v)", config.Agents, err)
	}
	t.Setenv("AGENTHAIL_SESSION_ID", "widget-lead")
	if _, err := captureStdout(t, func() error { return f.app.Run([]string{"project", "remove", widgetProject}) }); err != nil {
		t.Fatalf("the lead removing its project once the person allows it: %v", err)
	}
}
