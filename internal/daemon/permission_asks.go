package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const defaultPermissionAskEscalate = 3 * time.Minute

// waitsOf lists the waits an observation holds open. A provider names only
// the oldest one's kind and start; the rest take theirs when they become the
// oldest.
func waitsOf(waiting *surface.Waiting) []registry.Wait {
	if waiting == nil {
		return nil
	}
	oldest := registry.Wait{Ref: waiting.ID, Kind: waiting.Kind, Since: waiting.Since, Answerable: waiting.Answerable}
	if !waiting.Answerable {
		return []registry.Wait{oldest}
	}
	waits := []registry.Wait{oldest}
	for _, ref := range waiting.Open {
		if ref != waiting.ID {
			waits = append(waits, registry.Wait{Ref: ref, Answerable: true})
		}
	}
	return waits
}

func waitsAgree(open []registry.Ask, listed *surface.Waiting) bool {
	waits := waitsOf(listed)
	if len(waits) != len(open) {
		return false
	}
	answerable := make(map[string]bool, len(open))
	for _, ask := range open {
		answerable[ask.Ref] = ask.Answerable
	}
	for _, wait := range waits {
		if is, found := answerable[wait.Ref]; !found || is != wait.Answerable {
			return false
		}
	}
	return true
}

func (d *Daemon) recordedWaiting(session surface.Session) *surface.Waiting {
	waiting, err := d.Registry.SessionWaiting(session.ID)
	if err != nil {
		d.log.Printf("read waiting %s: %s", d.resolveDisplay(session.ID), err)
		return nil
	}
	return waiting
}

// syncWaits makes the session's permission asks what its provider holds
// open now, and reports whether that changed them.
func (d *Daemon) syncWaits(session surface.Session, waiting *surface.Waiting) bool {
	waits := waitsOf(waiting)
	routedTo := ""
	if needs, err := d.Registry.NeedsRoute(session.ID, waits); err != nil {
		d.log.Printf("route the waits of %s: %s", d.resolveDisplay(session.ID), err)
	} else if needs {
		if hearer, ok := d.askHearer(session.ID); ok {
			routedTo = hearer.sessionID
		}
	}
	sync, err := d.Registry.SyncWaits(session.ID, waits, routedTo, time.Now())
	if err != nil {
		d.log.Printf("record waiting %s: %s", d.resolveDisplay(session.ID), err)
		return false
	}
	for _, ask := range sync.Closed {
		d.closePermissionAsk(ask)
	}
	for _, ask := range sync.Opened {
		d.tellPermission(ask)
	}
	return sync.Changed()
}

func (d *Daemon) tellPermissionAsks(open []registry.Ask) {
	for _, ask := range open {
		d.tellPermission(ask)
	}
}

// tellPermission tells whoever a permission ask is routed to, once per
// holder while this daemon runs. The person's announcement is recorded, so a
// restart does not repeat it; a lead's notice is keyed, so it is queued once.
func (d *Daemon) tellPermission(ask registry.Ask) {
	if ask.RoutedTo == registry.AskToPerson && !ask.AnnouncedAt.IsZero() {
		return
	}
	d.permissionMu.Lock()
	if d.permissionTold == nil {
		d.permissionTold = map[string]string{}
	}
	if d.permissionTold[ask.ID] == ask.RoutedTo {
		d.permissionMu.Unlock()
		return
	}
	d.permissionTold[ask.ID] = ask.RoutedTo
	d.permissionMu.Unlock()
	started := d.background.start(func(ctx context.Context) {
		if ask.RoutedTo == registry.AskToPerson {
			d.announcePermission(ctx, ask)
		} else {
			d.noticePermission(ctx, ask)
		}
	})
	if !started {
		d.forgetPermission(ask.ID)
	}
}

func (d *Daemon) forgetPermission(askID string) {
	d.permissionMu.Lock()
	delete(d.permissionTold, askID)
	d.permissionMu.Unlock()
}

func (d *Daemon) closePermissionAsk(ask registry.Ask) {
	d.forgetPermission(ask.ID)
	if ask.RoutedTo != registry.AskToPerson {
		if _, err := d.Registry.CancelPendingByKey(askNoticeKey(ask.ID, ask.RoutedTo)); err != nil {
			d.log.Printf("withdraw the notice of %s: %s", ask.ID, err)
		}
	}
	if !ask.AnnouncedAt.IsZero() {
		d.withdrawAsk(ask.ID)
	}
}

// permissionRequest is the provider's open request behind an ask, read now
// so a notification shows what is actually asked.
func (d *Daemon) permissionRequest(ctx context.Context, session surface.Session, ask registry.Ask) (surface.Request, bool) {
	if !ask.Answerable {
		return surface.Request{}, false
	}
	adapter, err := d.adapters().For(&session)
	if err != nil {
		return surface.Request{}, false
	}
	responder, ok := sessionResponder(adapter, &session)
	if !ok {
		return surface.Request{}, false
	}
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, err := responder.Requests(requestCtx, &session)
	if err != nil {
		d.log.Printf("read the request %s waits on: %s", d.resolveDisplay(session.ID), err)
		return surface.Request{}, false
	}
	return openRequest(state, ask.Ref)
}

// announcePermission tells the person, on the desktop and on paired devices.
// The ask is marked announced only after both were attempted, so a daemon
// stopped in between announces it again after it restarts rather than never.
func (d *Daemon) announcePermission(ctx context.Context, ask registry.Ask) {
	session, err := d.Registry.Session(ask.SessionID)
	if err != nil {
		d.log.Printf("announce %s: %s", ask.ID, err)
		return
	}
	conversationID := session.ConversationID()
	restartable := d.restartable(*session)
	waiting := surface.Waiting{Kind: ask.WaitKind, ID: ask.ID, Answerable: ask.Answerable}
	desktop := elsewhereWaitingMessage(d.resolveDisplay(conversationID), waiting, session.RuntimeKind, restartable)
	key := askNotificationKey(ask.ID)
	mobile := mobileNotification{Title: "Agenthail", Message: elsewhereWaitingMessage(notificationSurfaceName(session.Surface), waiting, session.RuntimeKind, restartable), SessionID: conversationID, TurnID: ask.ID, EventType: "session.waiting", CollapseID: key}
	var actions []surface.RequestAction
	if request, open := d.permissionRequest(ctx, *session, ask); open {
		// A notification's buttons answer the session it names, so one that
		// carries a request names the session that holds it.
		mobile.SessionID, mobile.RequestID = session.ID, ask.ID
		desktop = fmt.Sprintf("%s: %s", d.resolveDisplay(session.ID), request.Prompt())
		actions = request.Actions()
	}
	d.deliverAskNotifications(ctx, desktop, mobile, actions)
	if ctx.Err() != nil {
		return
	}
	if err := d.Registry.MarkAskAnnounced(ask.ID, time.Now()); err != nil {
		d.log.Printf("mark %s announced: %s", ask.ID, err)
	}
	// An ask that closed while it was being announced was not withdrawn with
	// it, since it was not marked announced yet.
	if current, found, err := d.Registry.Ask(ask.ID); err == nil && (!found || current.State != registry.AskOpen || current.RoutedTo != registry.AskToPerson) {
		d.withdrawAskNow(ctx, ask.ID)
	}
}

func (d *Daemon) noticePermission(ctx context.Context, ask registry.Ask) {
	session, err := d.Registry.Session(ask.SessionID)
	if err != nil {
		d.log.Printf("tell the holder of %s: %s", ask.ID, err)
		return
	}
	asks := ask.Title
	if request, open := d.permissionRequest(ctx, *session, ask); open {
		asks = request.Prompt()
		if detail := strings.TrimSpace(request.Description); detail != "" && detail != asks {
			asks += ": " + detail
		}
	}
	message := permissionNotice(d.resolveDisplay(session.ID), d.answerTarget(session.ID), ask.ID, asks, d.usesMCP(ask.RoutedTo), d.permissionEscalateAfter())
	hearer := noticeRecipient{sessionID: ask.RoutedTo, role: "lead"}
	d.taskNoticeMu.Lock()
	read := d.taskRequestsRead[ask.SessionID] == (taskRequestRead{waitingID: ask.ID, reader: ask.RoutedTo})
	var sendErr error
	if !read {
		_, sendErr = d.sendNotice(hearer, message, askNoticeKey(ask.ID, ask.RoutedTo))
	}
	d.taskNoticeMu.Unlock()
	if sendErr != nil {
		d.log.Printf("tell %s that %s is waiting: %s", d.resolveDisplay(ask.RoutedTo), d.resolveDisplay(session.ID), sendErr)
		d.escalateAsk(ask, registry.AskToPerson)
	}
}

func permissionNotice(name, target, askID, asks string, mcp bool, window time.Duration) string {
	read := fmt.Sprintf("agenthail requests %s --json", target)
	answer := fmt.Sprintf("agenthail answer %s <allow|deny|\"answer\"> --request %s", target, askID)
	pass := fmt.Sprintf("agenthail ask escalate %s", askID)
	if mcp {
		read = fmt.Sprintf(`agenthail_requests {"sessionId":%q,"requestId":%q}`, target, askID)
		answer = fmt.Sprintf(`agenthail_answer {"requestId":%q,"decision":"allow|deny"}`, askID)
		pass = fmt.Sprintf(`agenthail_answer {"requestId":%q,"decision":"escalate"}`, askID)
	}
	return fmt.Sprintf("[agenthail] %s is blocked on a prompt: %s\nRead the request first: %s\nAnswer it: %s\nOr pass it to the person: %s\nIt goes to the person by itself if it is not answered within %s.", name, asks, read, answer, pass, plainDuration(window))
}

func plainDuration(window time.Duration) string {
	if minutes := int(window.Minutes()); minutes == 1 {
		return "a minute"
	} else if minutes > 1 {
		return fmt.Sprintf("%d minutes", minutes)
	}
	return window.String()
}

func (d *Daemon) permissionEscalateAfter() time.Duration {
	config, err := LoadDashboardConfig()
	if err != nil || config.AskPermissionEscalateMinutes <= 0 {
		return defaultPermissionAskEscalate
	}
	return time.Duration(config.AskPermissionEscalateMinutes) * time.Minute
}

// answerPermission answers the provider request behind an ask, at most once:
// the ask is claimed first and released when the provider did not take the
// answer.
func (d *Daemon) answerPermission(ctx context.Context, adapter surface.Surface, session *surface.Session, ask registry.Ask, decision surface.Decision, by, actor string) (actionResult, error) {
	if !ask.Answerable {
		return actionResult{}, invalidRequest("Agenthail can't answer this prompt; answer it where the session runs")
	}
	responder, ok := sessionResponder(adapter, session)
	if !ok {
		return actionResult{}, invalidRequest("this session's requests are not answered through Agenthail")
	}
	if decision.Behavior == "" && decision.Choice == "" {
		if len(decision.Answers) == 0 {
			return actionResult{}, invalidRequest("answer a permission request with allow, deny, never or stop")
		}
		decision.Behavior = "allow"
	}
	if decision.Choice != "" {
		state, err := responder.Requests(ctx, session)
		if err != nil {
			return actionResult{}, actionFailed(http.StatusBadGateway, "bad_gateway", err.Error())
		}
		request, open := openRequest(state, ask.Ref)
		if !open {
			d.observeAfterAnswer(adapter, session)
			return actionResult{}, staleRequest()
		}
		if decision, err = request.Choose(decision.Choice, decision.Message); err != nil {
			return actionResult{}, invalidRequest(err.Error())
		}
	}
	claimed, err := d.Registry.ClaimAsk(ask.ID, registry.AskAnswer{Answers: decision.Answers, Note: strings.TrimSpace(decision.Message)}, by, actor, time.Now())
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	if !claimed {
		return actionResult{}, staleRequest()
	}
	outcome, err := responder.Respond(ctx, session, ask.Ref, decision)
	if err != nil {
		if releaseErr := d.Registry.ReleaseAsk(ask.ID); releaseErr != nil {
			d.log.Printf("release the answer to %s: %s", ask.ID, releaseErr)
		}
		if errors.Is(err, surface.ErrRequestStale) {
			d.observeAfterAnswer(adapter, session)
			return actionResult{}, staleRequest()
		}
		return actionResult{}, actionFailed(http.StatusBadGateway, "bad_gateway", err.Error())
	}
	d.settleAsk(ask, outcome, "")
	entry := registry.HistoryEntry{Kind: "request-" + outcome, SessionID: session.ID, Message: ask.ID, Result: "answered by " + actor}
	record := func(context.Context) {
		if err := d.Registry.RecordHistory(entry); err != nil {
			d.log.Printf("record the answer to %s: %s", d.resolveDisplay(session.ID), err)
		}
	}
	if !d.background.start(record) {
		record(ctx)
	}
	d.observeAfterAnswer(adapter, session)
	return actionOK(map[string]any{"ok": true, "result": map[string]string{"requestId": ask.ID, "outcome": outcome}}), nil
}
