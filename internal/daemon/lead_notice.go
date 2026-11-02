package daemon

import (
	"context"
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/deliverypolicy"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type noticeRecipient struct {
	lead      *registry.CoordinatorSpec
	sessionID string
	role      string
}

func (d *Daemon) noticeRecipient(session surface.Session) (noticeRecipient, bool) {
	service := d.coordinator()
	if _, coordinates := service.Of(session.ID); !coordinates {
		if project, err := d.sessionProject(session); err == nil && project != nil {
			if spec, err := service.ProjectLead(project.ID, project.DisplayName); err == nil {
				leadSession := ""
				if spec.HostID == "" {
					leadSession, _ = d.Registry.CoordinatorSessionID(spec)
				}
				if spec.HostID != "" || leadSession != session.ID {
					return noticeRecipient{lead: &spec, sessionID: leadSession, role: "lead"}, true
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

// Keys stay unique after delivery, so a keyed notice is sent once and can be withdrawn while pending.
func (d *Daemon) sendNotice(ctx context.Context, to noticeRecipient, from surface.Session, message, key string) error {
	options := surface.SendOptions{SourceSessionID: from.ID, BusyDelivery: string(deliverypolicy.Steer)}
	switch {
	case to.lead != nil && to.lead.HostID != "":
		_, err := daemonBroadcastDelivery{d: d}.PeerCoordinator(ctx, *to.lead, message, from.ID)
		return err
	case to.lead != nil && to.sessionID == "":
		_, err := d.coordinator().Deliver(ctx, *to.lead, message, options)
		return err
	}
	_, err := d.Registry.QueueMessageWithOptions(to.sessionID, message, key, options)
	return err
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

func (d *Daemon) noticeWaiting(ctx context.Context, session surface.Session, waiting surface.Waiting, request surface.Request, open bool) {
	to, ok := d.noticeRecipient(session)
	if !ok {
		return
	}
	message := waitingNotice(d.resolveDisplay(session.ID), d.answerTarget(session.ID), waiting, session.RuntimeKind, request, open)
	if err := d.sendNotice(ctx, to, session, message, waitingNoticeKey(session.ID, waiting.ID)); err != nil {
		d.log.Printf("tell the %s that %s is waiting: %s", to.role, d.resolveDisplay(session.ID), err)
	}
}

func (d *Daemon) withdrawWaitingNotice(sessionID string, waiting surface.Waiting) {
	if _, err := d.Registry.CancelPendingByKey(waitingNoticeKey(sessionID, waiting.ID)); err != nil {
		d.log.Printf("withdraw the notice that %s is waiting: %s", d.resolveDisplay(sessionID), err)
	}
}
