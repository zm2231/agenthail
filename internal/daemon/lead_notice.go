package daemon

import (
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/deliverypolicy"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type noticeRecipient struct {
	sessionID string
	role      string
}

func (d *Daemon) noticeRecipient(session surface.Session) (noticeRecipient, bool) {
	projectID, where := "", ""
	if project, err := d.sessionProject(session); err == nil && project != nil {
		projectID, where = project.ID, project.DisplayName
	}
	id, role, ok := d.coordinator().PromptHearer(session.ID, projectID, where)
	return noticeRecipient{sessionID: id, role: role}, ok
}

func (d *Daemon) sendNotice(to noticeRecipient, from surface.Session, message, key string) (bool, error) {
	if used, err := d.Registry.DeliveryKeyUsed(key); err != nil || used {
		return false, err
	}
	busy := string(deliverypolicy.Steer)
	if owner, err := d.Registry.SessionHost(to.sessionID); err != nil {
		return false, err
	} else if owner != "" {
		_, err := d.Registry.QueueHostSend(registry.HostSend{SessionID: to.sessionID, Message: message, Key: key, SourceSessionID: from.ID, BusyDelivery: busy})
		return err == nil, err
	}
	_, err := d.Registry.QueueMessageWithOptions(to.sessionID, message, key, surface.SendOptions{SourceSessionID: from.ID, BusyDelivery: busy})
	return err == nil, err
}

func (d *Daemon) answerTarget(sessionID string) string {
	if alias, err := d.Registry.ReverseAlias(sessionID); err == nil && alias != "" {
		return "@" + alias
	}
	return sessionID
}

func waitingNoticeKey(sessionID, questionID string) string {
	return "notice-" + waitingNotificationKey(sessionID, questionID)
}

func waitingNotice(name, target string, waiting surface.Waiting, runtime surface.RuntimeKind, request surface.Request, open bool) string {
	if !open {
		return fmt.Sprintf("[agenthail] %s. The person was notified too.", elsewhereWaitingMessage(name, waiting, runtime, false))
	}
	asks := request.Prompt()
	if detail := strings.TrimSpace(request.Description); detail != "" && detail != asks {
		asks += ": " + detail
	}
	return fmt.Sprintf("[agenthail] %s is blocked on a prompt: %s\nRead the request first: agenthail requests %s --json\nAnswer it: agenthail answer %s <allow|deny|\"answer\"> --request %s\nAnswer it yourself now. The person was notified too, but don't wait for them: the first answer settles it.", name, asks, target, target, request.ID)
}

func (d *Daemon) noticeWaiting(session surface.Session, waiting surface.Waiting, request surface.Request, open bool) {
	to, ok := d.noticeRecipient(session)
	if !ok {
		return
	}
	message := waitingNotice(d.resolveDisplay(session.ID), d.answerTarget(session.ID), waiting, session.RuntimeKind, request, open)
	d.taskNoticeMu.Lock()
	defer d.taskNoticeMu.Unlock()
	if d.taskRequestsRead[session.ID] == (taskRequestRead{waitingID: waiting.ID, reader: to.sessionID}) {
		return
	}
	if _, err := d.sendNotice(to, session, message, waitingNoticeKey(session.ID, waiting.ID)); err != nil {
		d.log.Printf("tell the %s that %s is waiting: %s", to.role, d.resolveDisplay(session.ID), err)
	}
}

func (d *Daemon) withdrawWaitingNotice(sessionID string, waiting surface.Waiting) {
	if _, err := d.Registry.CancelPendingByKey(waitingNoticeKey(sessionID, waiting.ID)); err != nil {
		d.log.Printf("withdraw the notice that %s is waiting: %s", d.resolveDisplay(sessionID), err)
	}
}
