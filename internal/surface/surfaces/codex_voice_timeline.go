package surfaces

import "github.com/zm2231/agenthail/internal/surface"

func codexVoiceTimelineItems(payload map[string]any) []surface.TimelineItem {
	switch str(payload, "type") {
	case "transcript_segment":
		role := str(payload, "role")
		return []surface.TimelineItem{{Kind: "message", Role: role, Title: role + " · voice", Text: str(payload, "text")}}
	case "realtime_session_started":
		return []surface.TimelineItem{{Kind: "event", Title: "Codex Voice started"}}
	case "realtime_session_closed":
		return []surface.TimelineItem{{Kind: "event", Title: "Codex Voice ended", Status: str(payload, "outcome")}}
	}
	return nil
}
