package daemon

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	surfaceOperationTimeout = 20 * time.Second
)

// planScan reads what a scan needs and queues a job for each watched session
// it observes, without waiting for them. The channel it returns closes once
// they have all finished; the catalog's presence is published then.
func (d *Daemon) planScan(ctx context.Context) <-chan struct{} {
	jobs := &scanJobs{finished: make(chan struct{}), publish: d.publishCatalogPresence}
	jobs.start()
	defer jobs.done()
	d.syncDeliveryAsks()
	if err := d.catalog.flushCommitted(); err != nil {
		d.log.Printf("publish committed catalog events: %s", err)
	}
	watched, err := d.Registry.WatchedSessions()
	if err != nil {
		d.log.Printf("scan watched sessions: %s", err)
		return jobs.finished
	}
	needsClaudeRefresh := false
	for _, session := range watched {
		if session.Surface == surface.KindClaude && !session.OpenTurnOnly {
			needsClaudeRefresh = true
			break
		}
	}
	removed, claudeRefreshed := d.refreshAndPruneInactiveClaudeRoutes(ctx, time.Now().Add(-time.Hour), needsClaudeRefresh)
	if removed > 0 {
		d.publishEvent("state.changed", "", map[string]any{"source": "relay-removed", "count": removed})
	}
	queueCounts, err := d.Registry.QueueCounts()
	if err != nil {
		d.log.Printf("scan queue counts: %s", err)
		return jobs.finished
	}
	taskChildren, err := d.Registry.RunningTaskChildren()
	if err != nil {
		d.log.Printf("scan running tasks: %s", err)
		return jobs.finished
	}
	d.taskGoals.retain(taskChildren)
	d.retainTaskRequestsRead(taskChildren)
	d.deliverTaskWakes()
	d.pruneObservationFailures(watched)
	scan := &relayScan{}
	for _, watchedSession := range watched {
		gone, isTask := taskChildren[watchedSession.ID]
		if watchedSession.Surface == surface.KindClaude && !claudeRefreshed && !isTask && !d.onlyClosesATurn(watchedSession, queueCounts[watchedSession.ID]) {
			continue
		}
		session, sessionErr := d.Registry.Session(watchedSession.ID)
		if sessionErr != nil {
			d.log.Printf("load watched session %s: %s", truncate(watchedSession.ID, 16), sessionErr)
			continue
		}
		if noRoute := surface.NoReplyRoute(session); noRoute != nil {
			// No session reads this queue, so nothing queued here is ever
			// delivered; the sends behind it stay listed as problems.
			if canceled, err := d.Registry.CancelMessagesForSession(session.ID, nil); err != nil {
				d.log.Printf("cancel undeliverable queue of %s: %s", d.resolveDisplay(session.ID), err)
			} else if canceled > 0 {
				d.log.Printf("canceled %d queued messages for %s: %s", canceled, d.resolveDisplay(session.ID), noRoute)
			}
			continue
		}
		adapter, adapterErr := d.adapters().For(session)
		if adapterErr != nil {
			d.log.Printf("watched session %s: %s", d.resolveDisplay(session.ID), adapterErr)
			continue
		}
		queued := queueCounts[session.ID]
		jobs.start()
		if !d.observeJobs.add(session.Surface, observeJob{ctx: ctx, session: session.ID, done: jobs.done, run: func(ctx context.Context) {
			d.scanSession(ctx, scan, adapter, session, watchedSession, queued, gone, isTask)
		}}) {
			jobs.done()
		}
	}
	return jobs.finished
}

// scanSession is one watched session's part of a scan, run as its own job.
func (d *Daemon) scanSession(ctx context.Context, scan *relayScan, adapter surface.Surface, session *surface.Session, watched registry.WatchedSession, queued int, gone, isTask bool) {
	if stopper, ok := adapter.(surface.RuntimeStopper); ok && queued > 0 && stopper.RuntimeStopped(ctx, session) {
		d.drainMessageQueue(ctx, adapter, session)
		return
	}
	// An exited or offline Claude, Codex or ACP session stays a delivery target,
	// since a send resumes it; it is observed only to drain its queue, unless
	// a turn the daemon started is still open on it. An offline one may have
	// no PID left to tell whether its process exited.
	inactive := claudeProcessExited(session) || relayTargetInactive(session)
	resumes := session.Surface == surface.KindClaude || session.Surface == surface.KindCodex || surface.IsACPRuntime(session.RuntimeKind)
	if !isTask && !watched.Armed && !watched.OpenTurnOnly && inactive && (!resumes || queued == 0) {
		d.syncRelaysObserved(ctx, scan, adapter, session, observeOnce(adapter, session, observeAdapter))
		return
	}
	if queued == 0 && !d.observationAllowed(session.ID) {
		if gone {
			d.settleGoneTask(session)
		}
		return
	}
	d.observeInScan(ctx, scan, adapter, session, !watched.OpenTurnOnly)
	if gone {
		d.settleGoneTask(session)
	}
}

// scanJobs counts a scan's plan and its jobs; when the last finishes it
// publishes the catalog's presence and closes finished.
type scanJobs struct {
	remaining atomic.Int64
	finished  chan struct{}
	publish   func()
}

func (j *scanJobs) start() { j.remaining.Add(1) }

func (j *scanJobs) done() {
	if j.remaining.Add(-1) == 0 {
		j.publish()
		close(j.finished)
	}
}

func (d *Daemon) refreshAndPruneInactiveClaudeRoutes(ctx context.Context, cutoff time.Time, refresh bool) (int, bool) {
	routes, err := d.Registry.ListRoutes()
	if err != nil {
		return 0, false
	}
	if !refresh {
		return 0, false
	}
	refreshed := false
	for _, adapter := range d.Surfaces {
		if adapter.Name() != surface.KindClaude {
			continue
		}
		operationCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
		sessions, listErr := adapter.List(operationCtx)
		cancel()
		if listErr != nil {
			d.logObserveError("claude:routes", listErr)
			return 0, false
		}
		for _, session := range sessions {
			if registerErr := d.Registry.RecordObservedSession(session); registerErr != nil {
				d.log.Printf("register active Claude session: %s", registerErr)
				return 0, false
			}
		}
		refreshed = true
	}
	if len(routes) == 0 {
		return 0, refreshed
	}
	routes, err = d.Registry.ListRoutes()
	if err != nil {
		return 0, false
	}
	removed := 0
	for _, route := range routes {
		if route.Active {
			continue
		}
		source, sessionErr := d.Registry.Session(route.FromSession)
		if sessionErr != nil || !claudeProcessExited(source) {
			continue
		}
		if old, ageErr := d.Registry.SessionUpdatedBefore(route.FromSession, cutoff); ageErr != nil || !old {
			continue
		}
		if err := d.Registry.RemoveRoute(route.ID); err != nil {
			d.log.Printf("remove inactive relay %d: %s", route.ID, err)
			continue
		}
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "relay-removed", SessionID: route.ToSession, SourceSessionID: route.FromSession, RouteID: route.ID, Error: "Claude Code source session did not resume within 1 hour: " + route.FromSession})
		removed++
	}
	return removed, refreshed
}

func (d *Daemon) observeSession(ctx context.Context, adapter surface.Surface, session *surface.Session) {
	d.observeInScan(ctx, nil, adapter, session, true)
}

// A session watched only to see its turn end is not armed when seen busy, so a turn it started itself pushes nothing.
func (d *Daemon) observeInScan(ctx context.Context, scan *relayScan, adapter surface.Surface, session *surface.Session, arm bool) {
	read := *session
	observed := observeOnce(adapter, session, d.observeTurn)
	observation, err := observed.get(ctx)
	unobserved := err != nil && d.readUnobservedBackground(ctx, adapter, session)
	d.syncRelaysObserved(ctx, scan, adapter, session, observed)
	source, transport := session.Source, session.Transport
	if err != nil {
		d.recordObservationFailure(session.ID)
		d.logObserveError(session.ID, err)
		if unobserved {
			d.releaseUnobservedHold(ctx, adapter, session)
		}
		return
	}
	d.clearObservationFailure(session.ID)
	d.clearObserveError(session.ID)
	if observation == nil {
		d.log.Printf("observe %s: empty observation", d.resolveDisplay(session.ID))
		return
	}
	if observation.Status == surface.StatusBusy && observation.ActiveTurnID != "" && surface.EffectiveCapabilities(adapter, session).Stream {
		d.holdSessionSource(session, adapter, "active-turn")
	} else if observation.Status != surface.StatusBusy || observation.ActiveTurnID == "" {
		d.releaseSessionSource(session.ID, "active-turn")
	}
	session.Status = observation.Status
	session.WaitingOn = observation.WaitingOn
	session.BackgroundTasks = observation.BackgroundTasks
	session.UsageLimit, session.TurnStop = observation.UsageLimit, observation.TurnStop
	if session.TurnStop != nil {
		d.noticeTurnStop(*session)
	}
	session.Activity = observation.Activity
	session.Plan = observation.Plan
	if session.Source != source || session.Transport != transport {
		if err := d.Registry.UpdateSession(*session, registry.LearnedSessionUpdate(read, *session)); err != nil {
			d.log.Printf("persist refreshed session %s: %s", d.resolveDisplay(session.ID), err)
			return
		}
	}
	previous, found, err := d.Registry.RuntimeState(session.ID)
	if err != nil {
		d.log.Printf("runtime state %s: %s", d.resolveDisplay(session.ID), err)
		return
	}
	notificationTitle := ""
	notificationBody := ""
	notificationOutcome := ""
	completionPredatesActiveDelivery := previous.ActiveTurnID != "" && observation.ActiveTurnID == previous.ActiveTurnID
	completionChanged := found && !completionPredatesActiveDelivery && observation.CompletedTurnID != "" && observation.CompletedTurnID != previous.CompletedTurnID
	if completionChanged {
		if observation.Reply != nil && observation.Reply.Done && observation.Reply.Error == "" {
			for _, providerKey := range turnInputs(session.Surface, observation.CompletedTurnID, observation.InputTurnIDs) {
				if providerKey == "" {
					continue
				}
				reconciled, reconcileErr := d.Registry.ReconcileDeliveryIntent(session.ID, providerKey)
				if reconcileErr != nil {
					d.log.Printf("reconcile delivery intent %s: %s", d.resolveDisplay(session.ID), reconcileErr)
				} else if reconciled {
					_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "delivered", SessionID: session.ID, CompletionID: observation.CompletedTurnID, Result: "provider completion correlated to delivery intent"})
				}
			}
		}
	}
	stale := completionChanged && completionStale(observation, time.Now())
	held, released := heldCompletion(previous, observation, completionChanged)
	if stale {
		held = ""
	}
	terminalCompletion := completionChanged && observation.ActiveTurnID == "" && (stale || !surface.BackgroundRunning(observation.BackgroundTasks)) || released
	completed := terminalCompletion && completionNotificationEligible(previous, d.consumeNotificationArm(session.ID)) && observation.Reply != nil && observation.Reply.Done
	if completed && !session.Delegated() && !stale {
		notificationOutcome = turnOutcomeFinished
		if observation.Reply.Error != "" {
			notificationOutcome = turnOutcomeFailed
		}
		notificationTitle = d.sessionPushTag(session)
		notificationBody = "Finished"
		if notificationOutcome == turnOutcomeFailed {
			notificationBody = "Failed"
		}
	}
	if terminalCompletion || previous.NotificationArmed && !completionChanged && turnCannotRun(session, observation) {
		if err := d.Registry.ClearCompletionNotification(session.ID); err != nil {
			d.log.Printf("clear completion notification %s: %s", d.resolveDisplay(session.ID), err)
		}
	}
	if completionChanged {
		d.turnEndedForPush(session.ID)
	}
	if found && observation.Status == surface.StatusBusy && (previous.LastStatus != surface.StatusBusy || previous.ActiveTurnID != observation.ActiveTurnID) {
		if arm {
			d.armCompletionNotification(session.ID)
		}
		if !completionChanged || observation.ActiveTurnID != "" {
			d.pauseTurnPush(session.ID)
		}
		d.queueExtensionTurn(extensionTurnEvent{kind: extensionTurnStart, sessionID: session.ID})
	}
	d.settleTaskFromObservation(ctx, adapter, session, observation)
	if err := d.Registry.SaveRuntimeState(session.ID, *observation); err != nil {
		d.log.Printf("save runtime state %s: %s", d.resolveDisplay(session.ID), err)
		return
	}
	firstSeen := !found && observation.CompletedTurnID != "" && !observation.CompletedAt.IsZero() && !completionStale(observation, time.Now())
	if completionChanged || firstSeen {
		if err := d.Registry.BindTurnCompletion(session.ID, observation.CompletedTurnID, observation.CompletedAt, completionPrecision(session)); err != nil {
			d.log.Printf("bind turn completion %s: %s", d.resolveDisplay(session.ID), err)
		}
		if !stale && observation.Reply != nil && observation.Reply.Done && observation.Reply.Error == "" {
			d.queueTurnJudgment(session, observation.CompletedTurnID, observation.Reply.Text, observation.BackgroundTasks)
		}
		if !stale {
			d.pushWaitingTurn(session, observation.CompletedTurnID)
		}
	}
	notice := turnCompletedNotice{title: notificationTitle, desktop: notificationBody, mobile: notificationBody, outcome: notificationOutcome}
	if notice.desktop != "" && observation.Reply.Error == "" {
		notice = d.endTurnNotifications(session, observation.CompletedTurnID, notice.title, notice.desktop)
	}
	if notice.desktop != "" {
		notice.agentStarted = d.turnStartedByAgents(session.ID, turnInputs(session.Surface, observation.CompletedTurnID, observation.InputTurnIDs))
	}
	if held != previous.HeldCompletionTurnID {
		if err := d.Registry.HoldCompletion(session.ID, held); err != nil {
			d.log.Printf("hold completion %s: %s", d.resolveDisplay(session.ID), err)
		}
	}
	if found && previous.LastStatus == surface.StatusBusy && observation.Status != surface.StatusBusy && !completionChanged {
		d.queueExtensionTurn(extensionTurnEvent{kind: extensionTurnIdle, sessionID: session.ID})
		d.resumeTurnPush(session.ID)
	}
	changed := !found || previous.LastStatus != observation.Status || previous.ActiveTurnID != observation.ActiveTurnID || previous.CompletedTurnID != observation.CompletedTurnID
	if changed {
		d.publishEvent("session.updated", session.ID, map[string]any{"status": observation.Status, "activeTurnId": observation.ActiveTurnID, "completedTurnId": observation.CompletedTurnID})
	}
	if completionChanged {
		d.publishEvent("turn.completed", session.ID, map[string]string{"turnId": observation.CompletedTurnID})
		d.queueExtensionTurn(extensionTurnEvent{kind: extensionTurnEnd, sessionID: session.ID, turnID: observation.CompletedTurnID})
		if d.agenthailStarted(*session) {
			observed := *session
			d.background.start(func(ctx context.Context) { d.prs.LinkCreated(ctx, adapter, observed) })
		}
	}
	if completed {
		var noticed *turnCompletedNotice
		if notice.quiet {
			noticed = &notice
		}
		d.recordCompletion(session, observation.CompletedTurnID, observation.Reply.Error != "", noticed)
	}
	switch {
	case notice.quiet:
		d.dropTurnPush(session.ID)
	case notice.desktop != "":
		d.scheduleTurnPush(session.ID, observation.CompletedTurnID, notice)
	case terminalCompletion:
		d.resumeTurnPush(session.ID)
	}
	if terminalCompletion {
		d.gateLedgerTurn(*session, observation.CompletedTurnID)
	}
	canLoadQueue := session.Surface == surface.KindCodex && observation.Status == surface.StatusNotLoaded
	steerQueued, steerQueueErr := d.Registry.PendingSteer(session.ID)
	if steerQueueErr != nil {
		d.log.Printf("check steer queue %s: %s", d.resolveDisplay(session.ID), steerQueueErr)
	}
	canSteerQueued := steerQueued && surface.EffectiveCapabilities(adapter, session).Steer
	canDrainQueue := (observation.Status == surface.StatusIdle && observation.ActiveTurnID == "") || canLoadQueue
	canDrainSteer := canSteerQueued && observation.Status == surface.StatusBusy && observation.WaitingOn == nil
	canDrainPeer := observation.Status == surface.StatusBusy && surface.RouteFor(adapter, session, "") == surface.RoutePeer
	if canDrainQueue || canDrainPeer || canDrainSteer {
		before, markErr := d.Registry.QueueMark(session.ID)
		if canDrainQueue {
			d.drainMessageQueue(ctx, adapter, session)
		} else {
			if canDrainPeer {
				d.drainPeerMessageQueue(ctx, adapter, session)
			}
			if canDrainSteer {
				d.drainSteerMessageQueue(ctx, adapter, session)
			}
		}
		if after, err := d.Registry.QueueMark(session.ID); markErr != nil || err != nil || after != before {
			d.publishEvent("state.changed", session.ID, map[string]string{"source": "queue"})
		}
	}
}

func (d *Daemon) holdSessionSource(session *surface.Session, adapter surface.Surface, holder string) {
	d.sourceHoldMu.Lock()
	if holders := d.sourceHolds[session.ID]; holders != nil && holders[holder] != nil {
		d.sourceHoldMu.Unlock()
		return
	}
	d.sourceHoldMu.Unlock()
	release, err := d.sources.hold(session, adapter, holder)
	if err != nil {
		d.log.Printf("hold session source %s: %s", d.resolveDisplay(session.ID), err)
		return
	}
	d.sourceHoldMu.Lock()
	if holders := d.sourceHolds[session.ID]; holders != nil && holders[holder] != nil {
		d.sourceHoldMu.Unlock()
		release()
		return
	}
	if d.sourceHolds[session.ID] == nil {
		d.sourceHolds[session.ID] = map[string]func(){}
	}
	d.sourceHolds[session.ID][holder] = release
	d.sourceHoldMu.Unlock()
}

func (d *Daemon) releaseSessionSource(sessionID, holder string) {
	d.sourceHoldMu.Lock()
	holders := d.sourceHolds[sessionID]
	release := holders[holder]
	delete(holders, holder)
	if len(holders) == 0 {
		delete(d.sourceHolds, sessionID)
	}
	d.sourceHoldMu.Unlock()
	if release != nil {
		release()
	}
}

func (d *Daemon) setSessionSourceHold(session *surface.Session, active bool, holder string) {
	if session == nil {
		return
	}
	if !active {
		d.releaseSessionSource(session.ID, holder)
		return
	}
	adapter, err := d.adapters().For(session)
	if err != nil || !surface.EffectiveCapabilities(adapter, session).Stream {
		return
	}
	d.holdSessionSource(session, adapter, holder)
}

// turnInputs are the provider keys of what a completed turn read, as a
// delivery records them: a Codex turn's id, or each Claude input.
func turnInputs(kind surface.SurfaceKind, completedTurnID string, inputs []string) []string {
	switch kind {
	case surface.KindCodex:
		return []string{completedTurnID}
	case surface.KindClaude:
		return inputs
	}
	return nil
}

func completionNotificationEligible(state registry.RuntimeState, observedActive bool) bool {
	return state.NotificationArmed || observedActive
}

func (d *Daemon) armCompletionNotification(sessionID string) {
	d.notificationMu.Lock()
	d.notificationArmed[sessionID] = true
	d.notificationMu.Unlock()
}

func (d *Daemon) consumeNotificationArm(sessionID string) bool {
	d.notificationMu.Lock()
	armed := d.notificationArmed[sessionID]
	delete(d.notificationArmed, sessionID)
	d.notificationMu.Unlock()
	return armed
}

func notificationSurfaceName(kind surface.SurfaceKind) string {
	switch kind {
	case surface.KindClaude:
		return "Claude Code agent"
	case surface.KindCodex:
		return "Codex agent"
	case surface.KindNotion:
		return "Notion agent"
	default:
		return "Agent"
	}
}

func (d *Daemon) adapters() surface.Adapters { return surface.Adapters(d.Surfaces) }

const staleCompletionAge = 5 * time.Minute

func completionPrecision(session *surface.Session) time.Duration {
	if session.Surface == surface.KindCodex {
		return time.Second
	}
	return 0
}

func completionStale(observation *surface.TurnObservation, now time.Time) bool {
	return !observation.CompletedAt.IsZero() && now.Sub(observation.CompletedAt) > staleCompletionAge
}

func turnCannotRun(session *surface.Session, observation *surface.TurnObservation) bool {
	return observation.Unheld || claudeProcessExited(session)
}

// Without a fresh listing a Claude session's metadata may be stale, so only a turn with nothing queued or relayed is closed.
func (d *Daemon) onlyClosesATurn(watched registry.WatchedSession, queued int) bool {
	if !watched.Armed && !watched.OpenTurnOnly || queued > 0 {
		return false
	}
	relays, err := d.activeRelaysFrom(watched.ID)
	return err == nil && len(relays) == 0
}
