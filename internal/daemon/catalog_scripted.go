package daemon

import (
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const catalogHideBatch = 500

func (d *Daemon) splitHiddenScripted(sessions []surface.Session) ([]surface.Session, map[string]struct{}) {
	listed := make([]surface.Session, 0, len(sessions))
	hidden := map[string]struct{}{}
	for _, session := range sessions {
		if session.Entrypoint != "" {
			if isHidden, err := d.Registry.HiddenScripted(session.ID, session.Entrypoint, string(session.RuntimeKind)); err != nil {
				d.log.Printf("catalog: read whether %s is a scripted run: %s", session.ID, err)
			} else if isHidden {
				d.keepHiddenSession(session)
				hidden[session.ID] = struct{}{}
				continue
			}
		}
		listed = append(listed, session)
	}
	return listed, hidden
}

func (d *Daemon) keepHiddenSession(session surface.Session) {
	if err := d.Registry.RegisterSession(session); err != nil {
		d.log.Printf("catalog: register scripted run %s: %s", session.ID, err)
		return
	}
	if err := d.Registry.RecordEntrypoints(map[string]string{session.ID: session.Entrypoint}); err != nil {
		d.log.Printf("catalog: record the entrypoint of %s: %s", session.ID, err)
	}
}

func (d *Daemon) recordConversationEntrypoints(kind surface.SurfaceKind, reader surface.UnseenConversations, deadline time.Time) error {
	rows, err := d.Registry.SessionsWithoutEntrypoint(string(kind))
	if err != nil {
		return err
	}
	found := map[string]string{}
	for _, row := range rows {
		if time.Now().After(deadline) {
			break
		}
		if entrypoint := reader.ConversationEntrypoint(row.Transcript); entrypoint != "" {
			found[row.ID] = entrypoint
		}
	}
	return d.Registry.RecordEntrypoints(found)
}

func (d *Daemon) queueHiddenRemovals(kind surface.SurfaceKind) error {
	ids, err := d.Registry.HiddenCatalogRows(string(kind))
	if err != nil {
		return err
	}
	d.catalogEnded.setHidden(kind, ids)
	return nil
}
