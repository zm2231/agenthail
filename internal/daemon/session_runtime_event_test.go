package daemon

import (
	"encoding/json"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestSessionRuntimeEventMapsCanonicalActivityAndPhases(t *testing.T) {
	cases := []struct {
		name     string
		typeName string
		data     any
		want     string
	}{
		{name: "message", typeName: "message", data: canonicalRuntimeEvent{Type: "message", SessionID: "to", ID: "m1", Role: "assistant", Text: "answer"}, want: "message"},
		{name: "thought", typeName: "thought", data: canonicalRuntimeEvent{Type: "thought", SessionID: "to", ID: "h1", Text: "plan"}, want: "thought"},
		{name: "tool start", typeName: "tool_start", data: canonicalRuntimeEvent{Type: "tool_start", SessionID: "to", ID: "t1", Name: "shell", Input: map[string]any{"cmd": "pwd"}}, want: "tool_start"},
		{name: "tool done", typeName: "tool_done", data: canonicalRuntimeEvent{Type: "tool_done", SessionID: "to", ID: "t1", Name: "shell", Output: map[string]any{"code": 0}}, want: "tool_done"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			data, _ := json.Marshal(test.data)
			mapped, ok := sessionRuntimeEvent(apiEvent{Type: test.typeName, EntityID: "to", Data: data}, "to")
			if !ok || mapped["type"] != test.want {
				t.Fatalf("mapped=%v ok=%v", mapped, ok)
			}
		})
	}
	for _, status := range []string{"busy", "idle", "offline"} {
		data, _ := json.Marshal(map[string]string{"status": status})
		mapped, ok := sessionRuntimeEvent(apiEvent{Type: "session.updated", EntityID: "to", Data: data}, "to")
		if !ok || mapped["type"] != "phase" {
			t.Fatalf("status=%s mapped=%v ok=%v", status, mapped, ok)
		}
	}
	data, _ := json.Marshal(map[string]string{"turnId": "turn-1"})
	mapped, ok := sessionRuntimeEvent(apiEvent{Type: "turn.completed", EntityID: "to", Data: data}, "to")
	if !ok || mapped["type"] != "phase" || mapped["phase"] != "idle" {
		t.Fatalf("turn mapped=%v ok=%v", mapped, ok)
	}
}

func TestDashboardSessionAuthorityKeepsGatewayOwnership(t *testing.T) {
	session := &surface.Session{ID: "to", Source: "vscode", Transport: "desktop"}
	adapter := &timelineDaemonSurface{daemonSurface: &daemonSurface{kind: surface.KindCodex, caps: surface.Capabilities{Stream: true}}}
	authority := dashboardSessionAuthority(session, adapter)
	if authority["origin"] != "vscode" || authority["executionOwner"] != "agenthail" || authority["controlPlane"] != "agenthail" || authority["observationHost"] != "agenthail" {
		t.Fatalf("authority=%v", authority)
	}
	withoutTimeline := projectDashboardSession(session, &daemonSurface{kind: surface.KindCodex, caps: surface.Capabilities{Stream: true}})
	if withoutTimeline.Capabilities.Stream {
		t.Fatal("stream capability advertised without a replayable timeline provider")
	}
}
