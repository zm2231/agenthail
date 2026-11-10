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
	Lead             bool
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
		return UnobservedPresence(input.QueueCount, input.Lead)
	}
	var current bool
	var reason string
	if rule, ok := adapter.(PresenceRule); ok {
		current, reason = rule.Presence(session, input)
	} else {
		current, reason = DefaultPresence(session, input)
	}
	return leadPresence(current, reason, input.Lead)
}

// UnobservedPresence is the presence of a session its runtime no longer
// reports: it stays current while messages wait for it or while it leads a project.
func UnobservedPresence(queueCount int, lead bool) (bool, string) {
	if queueCount > 0 {
		return true, "queued"
	}
	return leadPresence(false, "", lead)
}

func leadPresence(current bool, reason string, lead bool) (bool, string) {
	if !current && lead {
		return true, "lead"
	}
	return current, reason
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

// ActiveWindow is how long after its last activity an idle session still
// counts as active for peer advertising and project channels.
const ActiveWindow = 24 * time.Hour

// RecentlyActive reports a session that is working, or idle and active
// within ActiveWindow. An offline session never is.
func RecentlyActive(session Session, now time.Time) bool {
	return session.Status != StatusOffline && RecentlyUsed(session, now)
}

// RecentlyUsed reports a session that is working or was active within
// ActiveWindow, whether or not its process is still up.
func RecentlyUsed(session Session, now time.Time) bool {
	if session.ID == "" {
		return false
	}
	if session.Status == StatusBusy {
		return true
	}
	return !session.LastActive.IsZero() && now.Sub(session.LastActive) <= ActiveWindow
}
