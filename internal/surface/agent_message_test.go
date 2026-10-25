package surface

import (
	"reflect"
	"strings"
	"testing"
)

func TestPresentTimelineItemMarksAgentMessages(t *testing.T) {
	cases := []struct {
		name  string
		item  TimelineItem
		want  TimelineItem
		agent *AgentMessage
	}{
		{
			name:  "claude peer message",
			item:  TimelineItem{Kind: "message", Role: "peer", Origin: "peer", Sender: "agenthail/codex: @builder", Text: "Tests\n\n  pass"},
			agent: &AgentMessage{Direction: AgentIn, Kind: AgentKindPeer, Peer: "@builder", Summary: "Tests pass"},
		},
		{
			name:  "claude peer message carrying a channel post",
			item:  TimelineItem{Kind: "message", Role: "peer", Origin: "peer", Sender: "@builder", Text: "[from @builder via #ops] deployed"},
			want:  TimelineItem{Kind: "message", Role: "peer", Origin: "peer", Sender: "@builder", Text: "deployed"},
			agent: &AgentMessage{Direction: AgentIn, Kind: AgentKindPeer, Peer: "@builder", Channel: "ops", Summary: "deployed"},
		},
		{
			name:  "codex prompt from another agent",
			item:  TimelineItem{Kind: "message", Role: "user", Title: "user", Text: "[from reviewer] Please rebase"},
			want:  TimelineItem{Kind: "message", Role: "peer", Origin: "peer", Sender: "reviewer", Title: "reviewer", Text: "Please rebase"},
			agent: &AgentMessage{Direction: AgentIn, Kind: AgentKindPeer, Peer: "reviewer", Summary: "Please rebase"},
		},
		{
			name: "person's prompt",
			item: TimelineItem{Kind: "message", Role: "user", Title: "user", Text: "fix the build"},
		},
		{
			name: "person's words relayed to claude keep their origin",
			item: TimelineItem{Kind: "message", Role: "user", Origin: OriginPerson, Title: "user", Text: "[from reviewer] quoted"},
		},
		{
			name:  "task notification",
			item:  TimelineItem{Kind: "event", Role: "system", Origin: "task-notification", Title: "Task notification", Text: "Build finished"},
			agent: &AgentMessage{Direction: AgentIn, Kind: AgentKindTask, Peer: "Task notification", Summary: "Build finished"},
		},
		{
			name:  "automatic continuation",
			item:  TimelineItem{Kind: "event", Role: "system", Origin: "auto-continuation", Title: "Continued automatically", Text: "go on"},
			agent: &AgentMessage{Direction: AgentIn, Kind: AgentKindContinuation, Peer: "Continued automatically", Summary: "go on"},
		},
		{
			name: "other injected prompt",
			item: TimelineItem{Kind: "event", Role: "system", Origin: "system", Title: "Injected prompt", Text: "x"},
		},
		{
			name:  "SendMessage call",
			item:  TimelineItem{Kind: "toolCall", Title: "SendMessage", Text: `{"to":"@collab","summary":"","message":"Audit done\nDetails follow"}`},
			agent: &AgentMessage{Direction: AgentOut, Kind: AgentKindSend, Peer: "@collab", Summary: "Audit done", Body: "Audit done\nDetails follow"},
		},
		{
			name: "other tool call",
			item: TimelineItem{Kind: "toolCall", Title: "Bash", Text: `{"command":"go test ./..."}`},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PresentTimelineItem(c.item)
			want := c.want
			if reflect.DeepEqual(want, TimelineItem{}) {
				want = c.item
			}
			want.Agent = c.agent
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("PresentTimelineItem = %+v (agent %+v); want %+v (agent %+v)", got, got.Agent, want, want.Agent)
			}
		})
	}
}

func TestAgentSummaryCollapsesAndCaps(t *testing.T) {
	if got := AgentSummary("  a\n\tb  "); got != "a b" {
		t.Fatalf("collapsed summary = %q", got)
	}
	long := strings.Repeat("é", agentSummaryRunes+10)
	got := AgentSummary(long)
	if []rune(got)[agentSummaryRunes] != '…' || len([]rune(got)) != agentSummaryRunes+1 {
		t.Fatalf("capped summary has %d runes", len([]rune(got)))
	}
}

func TestPeerDisplayNameDropsTheAgenthailPrefix(t *testing.T) {
	for sender, want := range map[string]string{"agenthail/claude: @collab": "@collab", "agenthail/codex: Fix the build": "Fix the build", "reviewer": "reviewer", "agenthail/claude: ": "agenthail/claude:"} {
		if got := PeerDisplayName(sender); got != want {
			t.Errorf("PeerDisplayName(%q) = %q; want %q", sender, got, want)
		}
	}
}
