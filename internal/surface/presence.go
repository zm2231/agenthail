package surface

import (
	"context"
	"time"
)

// PresenceInput is what decides whether a session is current: its queued
// messages, whether its adapter found it open, and the dashboard's Codex
// recency window.
type PresenceInput struct {
	QueueCount       int
	Open             bool
	Now              time.Time
	CodexRecentHours int
}

// PresenceRule is implemented by an adapter whose sessions are current by a
// rule of their own. Every other adapter's sessions follow DefaultPresence.
type PresenceRule interface {
	Presence(session Session, input PresenceInput) (bool, string)
}

// OpenSessionSource is implemented by an adapter that can tell which listed
// sessions have a live process, in one pass over the sessions.
type OpenSessionSource interface {
	OpenSessions(ctx context.Context, sessions []Session) map[string]bool
}

func PresenceFor(adapter Surface, session Session, input PresenceInput) (bool, string) {
	if session.Status == StatusOffline {
		return UnobservedPresence(input.QueueCount)
	}
	if rule, ok := adapter.(PresenceRule); ok {
		return rule.Presence(session, input)
	}
	return DefaultPresence(session, input)
}

// UnobservedPresence is the presence of a session its runtime no longer
// reports: it stays current only while messages wait for it.
func UnobservedPresence(queueCount int) (bool, string) {
	if queueCount > 0 {
		return true, "queued"
	}
	return false, ""
}

func OpenSessions(ctx context.Context, adapter Surface, sessions []Session) map[string]bool {
	if source, ok := adapter.(OpenSessionSource); ok {
		return source.OpenSessions(ctx, sessions)
	}
	return nil
}

// DefaultPresence counts a session current while it works, has queued
// messages, or was active in the last day.
func DefaultPresence(session Session, input PresenceInput) (bool, string) {
	if session.Status == StatusBusy {
		return true, "working"
	}
	if input.QueueCount > 0 {
		return true, "queued"
	}
	if !session.LastActive.IsZero() && input.Now.Sub(session.LastActive) <= 24*time.Hour {
		return true, "recent"
	}
	return false, ""
}
