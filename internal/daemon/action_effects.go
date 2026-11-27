package daemon

import (
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

const nativeQueueChangedEvent = "native.queue.changed"

// actionScope is what a successful action changes, which decides the event
// that announces it.
type actionScope int

const (
	scopeReads actionScope = iota + 1
	scopeSession
	scopeQueued
	scopeProject
	scopeChannel
	scopeDaemon
	scopeNativeQueue
)

// actionScopes declares every dispatched action. scopeDaemon is for changes no
// single entity holds: new sessions, relays, runtime health, lead
// replacements and ledger reviews.
var actionScopes = map[string]actionScope{
	"context":                  scopeReads,
	"usage":                    scopeReads,
	"status":                   scopeReads,
	"session-focus":            scopeReads,
	"session-lifecycle-status": scopeReads,
	"session-lifecycle-logs":   scopeReads,

	"alias":                    scopeSession,
	"compact":                  scopeSession,
	"dismiss-decision":         scopeSession,
	"effort":                   scopeSession,
	"goal-budget":              scopeSession,
	"goal-clear":               scopeSession,
	"goal-edit":                scopeSession,
	"goal-pause":               scopeSession,
	"goal-resume":              scopeSession,
	"goal-set":                 scopeSession,
	"interrupt":                scopeSession,
	"limit-move":               scopeSession,
	"limit-resume":             scopeSession,
	"limit-resume-cancel":      scopeSession,
	"model":                    scopeSession,
	"permission-mode":          scopeSession,
	"pr":                       scopeSession,
	"queue-clear":              scopeSession,
	"rename":                   scopeSession,
	"request-withdraw":         scopeSession,
	"respond":                  scopeSession,
	"send":                     scopeSession,
	"session-adopt":            scopeSession,
	"session-archive":          scopeSession,
	"session-fork":             scopeSession,
	"session-lifecycle-resume": scopeSession,
	"session-lifecycle-stop":   scopeSession,
	"session-pin":              scopeSession,
	"session-read":             scopeSession,
	"session-snooze":           scopeSession,
	"session-unarchive":        scopeSession,
	"session-unpin":            scopeSession,
	"session-unread":           scopeSession,
	"session-wake":             scopeSession,
	"steer":                    scopeSession,
	"turn-setting":             scopeSession,
	"work-clean":               scopeSession,

	"queue-cancel": scopeQueued,

	"channel-deliver":   scopeProject,
	"coordinator-brief": scopeProject,
	"coordinator-clear": scopeProject,
	"coordinator-new":   scopeProject,
	"coordinator-send":  scopeProject,
	"coordinator-set":   scopeProject,
	"project-add":       scopeProject,
	"project-archive":   scopeProject,
	"project-join":      scopeProject,
	"project-link":      scopeProject,
	"project-remove":    scopeProject,
	"project-rename":    scopeProject,
	"project-setting":   scopeProject,
	"project-share":     scopeProject,
	"project-unarchive": scopeProject,
	"project-unjoin":    scopeProject,
	"project-unlink":    scopeProject,

	"channel-add":    scopeChannel,
	"channel-create": scopeChannel,
	"channel-delete": scopeChannel,
	"channel-remove": scopeChannel,
	"channel-rename": scopeChannel,
	"channel-send":   scopeChannel,

	"codex-launch":         scopeDaemon,
	"lead-replacement-ack": scopeDaemon,
	"notion-create":        scopeDaemon,
	"relay-add":            scopeDaemon,
	"relay-remove":         scopeDaemon,
	"runtime-ensure":       scopeDaemon,
	"session-create":       scopeDaemon,
	workReviewAction:       scopeDaemon,

	"native-queue": scopeNativeQueue,
}

// actionChanges reports whether a successful action changed anything.
func actionChanges(action string, queue surface.NativeQueueRequest) bool {
	switch actionScopes[action] {
	case scopeReads:
		return false
	case scopeNativeQueue:
		return surface.NativeQueueActions[queue.Action]
	}
	return true
}

// announceAction publishes what a successful action changed. An undeclared
// action, which the declaration test rules out, announces a change to all.
func (d *Daemon) announceAction(request dashboardActionRequest) {
	if !actionChanges(request.Action, request.NativeQueue) {
		return
	}
	if actionScopes[request.Action] == scopeNativeQueue {
		d.publishNativeQueueChanged(request.SessionID, "action")
		return
	}
	d.publishEvent("state.changed", d.actionEntity(request), map[string]string{"source": "action", "action": request.Action})
}

func (d *Daemon) actionEntity(request dashboardActionRequest) string {
	switch actionScopes[request.Action] {
	case scopeSession:
		if id, err := d.Registry.ResolveTarget(request.SessionID); err == nil {
			return id
		}
		return request.SessionID
	case scopeQueued:
		if item, err := d.Registry.QueueItem(request.QueueID, d.adapters()); err == nil {
			return item.SessionID
		}
	case scopeProject:
		return request.ProjectID
	case scopeChannel:
		return strings.TrimPrefix(strings.TrimSpace(request.Channel), "#")
	}
	return ""
}

func (d *Daemon) publishNativeQueueChanged(sessionID, source string) {
	d.publishEvent(nativeQueueChangedEvent, sessionID, map[string]string{"source": source})
}
