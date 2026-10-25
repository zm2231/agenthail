package surfaces

import (
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// setCodexTransport routes a session and records the runtime that carries it:
// the managed app-server for managed threads, Codex Desktop for the rest,
// including read-only threads whose Desktop bridge is unavailable.
func setCodexTransport(session *surface.Session, transport string) {
	session.Transport = transport
	session.RuntimeKind = surface.RuntimeCodexDesktop
	if transport == codexTransportManaged {
		session.RuntimeKind = surface.RuntimeCodexAppServer
	}
}

// codexListsThread excludes threads that are not conversations to address:
// ephemeral threads, which Codex never stores, and subagents Codex runs for
// itself (review, compaction, memory) with no parent thread to sit under.
func codexListsThread(thread map[string]any) bool {
	if ephemeral, _ := thread["ephemeral"].(bool); ephemeral {
		return false
	}
	source, _ := thread["source"].(map[string]any)
	if _, subagent := source["subAgent"]; subagent && codexSubagent(thread) == nil {
		return false
	}
	return true
}

func codexSession(thread map[string]any) surface.Session {
	session := surface.Session{
		ID:         str(thread, "id"),
		Surface:    surface.KindCodex,
		Name:       surface.DeriveName(str(thread, "name"), str(thread, "preview"), 60),
		Cwd:        str(thread, "cwd"),
		Status:     codexStatus(thread["status"]),
		Source:     codexSource(thread["source"]),
		WaitingOn:  codexSessionWaiting(str(thread, "id"), thread["status"]),
		Subagent:   codexSubagent(thread),
		Transcript: str(thread, "path"),
	}
	setCodexTransport(&session, "")
	if timestamp, ok := thread["recencyAt"].(float64); ok && timestamp > 0 {
		session.LastActive = time.Unix(int64(timestamp), 0)
	}
	return session
}

// codexWaitingKind reads the waiting flags of an active ThreadStatus: the
// app-server sets them while an approval or user-input request is pending.
func codexWaitingKind(status any) surface.WaitingKind {
	object, _ := status.(map[string]any)
	if object == nil || object["type"] != "active" {
		return ""
	}
	flags, _ := object["activeFlags"].([]any)
	for _, flag := range flags {
		switch flag {
		case "waitingOnApproval":
			return surface.WaitingApproval
		case "waitingOnUserInput":
			return surface.WaitingInput
		}
	}
	return ""
}

func codexSessionWaiting(threadID string, status any) *surface.Waiting {
	return codexWaiting(threadID, codexWaitingKind(status))
}

// codexWaiting names what a thread waits on the same way from a thread
// listing and from an observation, so either one recognizes the other's
// record. Codex reports the wait on the thread, without the request id.
func codexWaiting(threadID string, kind surface.WaitingKind) *surface.Waiting {
	if kind == "" {
		return nil
	}
	return &surface.Waiting{Kind: kind, ID: threadID + ":" + string(kind)}
}

func codexStatus(s any) surface.SessionStatus {
	if m, ok := s.(map[string]any); ok {
		s = m["type"]
	}
	str, _ := s.(string)
	return surface.NormalizeStatus(str)
}
