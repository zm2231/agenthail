package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const turnNeedsYouEvent = "turn.needs_you"

// Identical on the host and every viewer, and distinct from the turn's completion key, so the relay delivers it once even after the turn's finished push.
func needsYouDedupeKey(hostID, sessionID, turnID string) string {
	if hostID == "" || sessionID == "" || turnID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(hostID + "\x00" + sessionID + "\x00" + turnID + "\x00needs_you"))
	return hex.EncodeToString(sum[:])
}

// The completion and the needs-you push of one turn share it, so a phone shows the later one in place of the earlier.
func turnNotificationKey(hostID, sessionID, turnID string) string {
	if hostID == "" || sessionID == "" || turnID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(hostID + "\x00" + sessionID + "\x00" + turnID))
	return "turn-" + hex.EncodeToString(sum[:16])
}

// A turn the judge reads as waiting on the person pushes once, unless the person typed its prompt into the session and so is at it, or a newer turn has started since.
func (d *Daemon) pushJudgedTurn(judgment registry.TurnJudgment) bool {
	if judgment.Error != "" || judgment.State != turnWaitingPerson || judgment.Facts.Typed {
		return false
	}
	session, err := d.Registry.Session(judgment.SessionID)
	if err != nil || session.Delegated() {
		return false
	}
	state, askID, err := d.judgedTurnState(session, &judgment)
	if err != nil {
		d.log.Printf("turn judge: read the state of %s: %s", d.resolveDisplay(session.ID), err)
		return false
	}
	if state == nil || state.State != turnWaitingPerson || state.Source != turnSourceJudge {
		return false
	}
	claimedAt := time.Now()
	claimed, err := d.Registry.ClaimTurnPush(session.ID, judgment.TurnID, askID, claimedAt)
	if err != nil {
		d.log.Printf("turn judge: claim the push of %s turn %s: %s", d.resolveDisplay(session.ID), judgment.TurnID, err)
		return false
	}
	if !claimed {
		return false
	}
	if d.turnPushRead != nil {
		d.turnPushRead()
	}
	// The project and its lead are not in the claim, so the state is read again once claimed: the push goes out only if the row still needed the person at or after the claim. Until it is confirmed the claim reads as not pushed, so no viewer mirrors it.
	if state, _, err := d.judgedTurnState(session, &judgment); err != nil || state == nil || state.State != turnWaitingPerson || state.Source != turnSourceJudge {
		if err := d.Registry.ReleaseTurnPush(session.ID, judgment.TurnID, claimedAt); err != nil {
			d.log.Printf("turn judge: release the push of %s turn %s: %s", d.resolveDisplay(session.ID), judgment.TurnID, err)
		}
		return false
	}
	body := "Needs you"
	if end, ok, err := d.Registry.CompletedTurnEnd(session.ID, judgment.TurnID); err == nil && ok {
		body += ": " + shownLine(end.Headline, headlineShownRunes)
	}
	hostID := d.hostIdentity().ID
	desktop := body
	mobile := mobileNotification{Title: d.sessionPushTag(session), Message: body, SessionID: session.ID, TurnID: judgment.TurnID, EventType: turnNeedsYouEvent,
		CollapseID: turnNotificationKey(hostID, session.ID, judgment.TurnID), DedupeKey: needsYouDedupeKey(hostID, session.ID, judgment.TurnID)}
	sent, err := d.needsYouTakesTurnPush(session.ID, judgment.TurnID,
		func() (bool, error) { return d.Registry.ConfirmTurnPush(session.ID, judgment.TurnID, claimedAt) },
		func() error { return d.Registry.ReleaseTurnPush(session.ID, judgment.TurnID, claimedAt) },
		func() { d.sendTurnNeedsYou(session.ID, desktop, mobile) })
	if err != nil {
		d.log.Printf("turn judge: settle the push of %s turn %s: %s", d.resolveDisplay(session.ID), judgment.TurnID, err)
	}
	if !sent {
		return d.turnFlushed(session.ID, judgment.TurnID)
	}
	d.requestCatalogDiscovery()
	d.publishEvent("state.changed", session.ID, map[string]string{"source": "turn-needs-you"})
	return true
}

// A turn that ended saying it waits on the person pushes once, in place of its finished push, unless the person typed its prompt or a lead hears it first.
func (d *Daemon) pushWaitingTurn(session *surface.Session, turnID string) bool {
	end, found, err := d.Registry.CompletedTurnEnd(session.ID, turnID)
	if err != nil || !found || end.WaitingOn == "" || session.Delegated() {
		return false
	}
	if typed, err := d.Registry.TurnTyped(session.ID, turnID); err != nil || typed {
		return false
	}
	waits := func() (bool, string) {
		state, askID, err := d.waitingTurnState(session, end)
		return err == nil && state != nil && state.State == turnWaitingPerson && state.Source == turnSourceEndTurn, askID
	}
	ok, askID := waits()
	if !ok {
		return false
	}
	claimedAt := time.Now()
	claimed, err := d.Registry.ClaimWaitPush(session.ID, turnID, askID, claimedAt)
	if err != nil {
		d.log.Printf("waiting turn: claim the push of %s turn %s: %s", d.resolveDisplay(session.ID), turnID, err)
		return false
	}
	if !claimed {
		return false
	}
	if ok, _ := waits(); !ok {
		if err := d.Registry.ReleaseWaitPush(session.ID, turnID, claimedAt); err != nil {
			d.log.Printf("waiting turn: release the push of %s turn %s: %s", d.resolveDisplay(session.ID), turnID, err)
		}
		return false
	}
	body := "Needs you: " + shownLine(end.WaitingOn, headlineShownRunes)
	hostID := d.hostIdentity().ID
	mobile := mobileNotification{Title: d.sessionPushTag(session), Message: body, SessionID: session.ID, TurnID: turnID, EventType: turnNeedsYouEvent,
		CollapseID: turnNotificationKey(hostID, session.ID, turnID), DedupeKey: needsYouDedupeKey(hostID, session.ID, turnID)}
	sent, err := d.needsYouTakesTurnPush(session.ID, turnID,
		func() (bool, error) { return d.Registry.ConfirmWaitPush(session.ID, turnID, claimedAt) },
		func() error { return d.Registry.ReleaseWaitPush(session.ID, turnID, claimedAt) },
		func() { d.sendTurnNeedsYou(session.ID, body, mobile) })
	if err != nil {
		d.log.Printf("waiting turn: settle the push of %s turn %s: %s", d.resolveDisplay(session.ID), turnID, err)
	}
	if sent {
		d.requestCatalogDiscovery()
		d.publishEvent("state.changed", session.ID, map[string]string{"source": "turn-needs-you"})
	}
	return sent
}

func (d *Daemon) waitingTurnState(session *surface.Session, end registry.TurnEnd) (*dashboardTurnState, string, error) {
	identity, err := d.sessionIdentity(*session)
	if err != nil {
		return nil, "", err
	}
	leads, err := d.loadLeadDirectory()
	if err != nil {
		return nil, "", err
	}
	row := dashboardSession{ID: session.ID, Project: identity.Project, EndTurn: endTurnRow(&end)}
	ask, asked, err := d.Registry.RowAsk(session.ID)
	if err != nil {
		return nil, "", err
	}
	if asked {
		row.Ask = askRow(&ask)
	}
	return turnStateOf(&row, nil, leads), ask.ID, nil
}

func (d *Daemon) judgedTurnState(session *surface.Session, judgment *registry.TurnJudgment) (*dashboardTurnState, string, error) {
	identity, err := d.sessionIdentity(*session)
	if err != nil {
		return nil, "", err
	}
	leads, err := d.loadLeadDirectory()
	if err != nil {
		return nil, "", err
	}
	row := dashboardSession{ID: session.ID, Project: identity.Project}
	ask, asked, err := d.Registry.RowAsk(session.ID)
	if err != nil {
		return nil, "", err
	}
	if asked {
		row.Ask = askRow(&ask)
	}
	return turnStateOf(&row, judgment, leads), ask.ID, nil
}

// A viewer pushes a mirrored turn only once its host has pushed it, under the host's key, so a phone paired to either hears it once.
func (d *Daemon) notifyMirroredNeedsYou(host *pairedHost, rawID string, row dashboardSession, prior *dashboardTurnState) {
	state := row.TurnState
	if state == nil || !state.Pushed || state.TurnID == "" || state.State != turnWaitingPerson {
		return
	}
	if prior != nil && prior.Pushed && prior.TurnID == state.TurnID {
		return
	}
	desktop := "Needs you"
	if state.Headline != "" {
		desktop += ": " + state.Headline
	}
	mobile := mobileNotification{Title: pushTag(row.Surface, row.Alias, row.Name, row.Cwd), Message: desktop, SessionID: row.ID, TurnID: state.TurnID, EventType: turnNeedsYouEvent,
		CollapseID: turnNotificationKey(host.record.ID, rawID, state.TurnID), DedupeKey: needsYouDedupeKey(host.record.ID, rawID, state.TurnID)}
	d.sendTurnNeedsYou(row.ID, desktop, mobile)
}

func (d *Daemon) sendTurnNeedsYou(sessionID, desktop string, mobile mobileNotification) {
	d.background.start(func(ctx context.Context) {
		if err := d.notifyDesktop(mobile.Title, desktop, sessionID, "", "", nil); err != nil {
			d.log.Printf("desktop notification: %s", err)
		}
		notificationCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		d.notifyPairedDevices(notificationCtx, mobile)
	})
}
