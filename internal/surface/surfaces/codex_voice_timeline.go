package surfaces

import (
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

func codexVoiceTimelineItems(payload map[string]any) []surface.TimelineItem {
	switch transcript.String(payload, "type") {
	case "transcript_segment":
		role := transcript.String(payload, "role")
		return []surface.TimelineItem{{Kind: "message", Role: role, Title: role + " · voice", Text: transcript.String(payload, "text")}}
	case "realtime_session_started":
		return []surface.TimelineItem{{Kind: "event", Title: "Codex Voice started"}}
	case "realtime_session_closed":
		return []surface.TimelineItem{{Kind: "event", Title: "Codex Voice ended", Status: transcript.String(payload, "outcome")}}
	}
	return nil
}
