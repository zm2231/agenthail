package daemon

import (
	"time"

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

func (d *Daemon) oversees(sourceID string, session surface.Session) bool {
	projectID, where := "", ""
	if project, err := d.sessionProject(session); err == nil && project != nil {
		projectID, where = project.ID, project.DisplayName
	}
	return d.coordinator().Oversees(sourceID, session.ID, projectID, where)
}

func (d *Daemon) sendNotice(to noticeRecipient, message, key string) (bool, error) {
	if used, err := d.Registry.DeliveryKeyUsed(key); err != nil || used {
		return false, err
	}
	busy := string(deliverypolicy.Steer)
	if owner, err := d.Registry.SessionHost(to.sessionID); err != nil {
		return false, err
	} else if owner != "" {
		_, err := d.Registry.QueueHostSend(registry.HostSend{SessionID: to.sessionID, Message: message, Key: key, SourceSessionID: surface.SystemSessionID, BusyDelivery: busy})
		return err == nil, err
	}
	_, err := d.Registry.QueueMessageWithOptions(to.sessionID, message, key, surface.SendOptions{SourceSessionID: surface.SystemSessionID, BusyDelivery: busy})
	return err == nil, err
}

func (d *Daemon) answerTarget(sessionID string) string {
	if alias, err := d.Registry.ReverseAlias(sessionID); err == nil && alias != "" {
		return "@" + alias
	}
	return sessionID
}

func (d *Daemon) usesMCP(sessionID string) bool {
	_, live, err := d.Registry.LiveCredentialHash(sessionID, time.Now())
	return err == nil && live
}
