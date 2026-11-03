package daemon

import (
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/deliverypolicy"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type noticeRecipient struct {
	sessionID string
	role      string
}

// A coordinator that has never started a conversation leads nothing yet, so the spawner hears instead.
func (d *Daemon) noticeRecipient(session surface.Session) (noticeRecipient, bool) {
	service := d.coordinator()
	if _, coordinates := service.Of(session.ID); !coordinates {
		if project, err := d.sessionProject(session); err == nil && project != nil {
			if spec, err := service.ProjectLead(project.ID, project.DisplayName); err == nil {
				if lead := d.leadSession(spec); lead != "" && lead != session.ID {
					return noticeRecipient{sessionID: lead, role: "lead"}, true
				}
			}
		}
	}
	origin, found, err := d.Registry.SessionOrigin(session.ID)
	if err != nil || !found || origin.CreatorID == "" || origin.CreatorID == session.ID {
		return noticeRecipient{}, false
	}
	return noticeRecipient{sessionID: origin.CreatorID, role: "spawner"}, true
}

func (d *Daemon) leadSession(spec registry.CoordinatorSpec) string {
	if spec.HostID == "" {
		id, err := d.Registry.CoordinatorSessionID(spec)
		if err != nil {
			return ""
		}
		return id
	}
	id := spec.SessionID
	if host, target, ok := hosts.Split(id); ok {
		if host != spec.HostID {
			return ""
		}
		id = target
	}
	if owner, err := d.Registry.SessionHost(id); err != nil || id == "" || owner != spec.HostID {
		return ""
	}
	return id
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
	return fmt.Sprintf("[agenthail] %s is blocked on a prompt: %s\nAnswer it: agenthail answer %s <allow|deny|\"answer\"> --request %s\nThe person was notified too; the first answer settles it.", name, asks, target, request.ID)
}

func (d *Daemon) noticeWaiting(session surface.Session, waiting surface.Waiting, request surface.Request, open bool) {
	to, ok := d.noticeRecipient(session)
	if !ok {
		return
	}
	message := waitingNotice(d.resolveDisplay(session.ID), d.answerTarget(session.ID), waiting, session.RuntimeKind, request, open)
	if _, err := d.sendNotice(to, session, message, waitingNoticeKey(session.ID, waiting.ID)); err != nil {
		d.log.Printf("tell the %s that %s is waiting: %s", to.role, d.resolveDisplay(session.ID), err)
	}
}

func (d *Daemon) withdrawWaitingNotice(sessionID string, waiting surface.Waiting) {
	if _, err := d.Registry.CancelPendingByKey(waitingNoticeKey(sessionID, waiting.ID)); err != nil {
		d.log.Printf("withdraw the notice that %s is waiting: %s", d.resolveDisplay(sessionID), err)
	}
}
