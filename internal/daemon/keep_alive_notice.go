package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const AttentionKeepAliveFailed = "keepAliveFailed"

type dashboardKeepAlive struct {
	FailedAt time.Time `json:"failedAt"`
	Error    string    `json:"error,omitempty"`
}

func keepAliveRow(state registry.KeepAlive) *dashboardKeepAlive {
	if state.FailedAt.IsZero() {
		return nil
	}
	return &dashboardKeepAlive{FailedAt: state.FailedAt, Error: state.Error}
}

func keepAliveFailedText(name string) string {
	return fmt.Sprintf("%s is pinned but could not be brought back; resume it or unpin it", name)
}

// announceKeepAliveFailed tells the person once, on the desktop and on paired
// devices. It is marked announced only after both were attempted, so a daemon
// stopped in between announces it again rather than never.
func (d *Daemon) announceKeepAliveFailed(session surface.Session, state registry.KeepAlive) {
	d.announceMu.Lock()
	if d.announcingKeepAlive == nil {
		d.announcingKeepAlive = map[string]bool{}
	}
	if d.announcingKeepAlive[session.ID] {
		d.announceMu.Unlock()
		return
	}
	d.announcingKeepAlive[session.ID] = true
	d.announceMu.Unlock()
	finished := func() {
		d.announceMu.Lock()
		delete(d.announcingKeepAlive, session.ID)
		d.announceMu.Unlock()
	}
	conversationID := session.ConversationID()
	key := "keep-alive-" + session.ID
	started := d.background.start(func(ctx context.Context) {
		defer finished()
		if err := d.notifyDesktop("Agenthail", keepAliveFailedText(d.resolveDisplay(conversationID)), conversationID, "agenthail-"+key, "", nil); err != nil {
			d.log.Printf("desktop notification: %s", err)
		}
		notificationCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		d.notifyPairedDevices(notificationCtx, mobileNotification{Title: "Agenthail", Message: keepAliveFailedText("A pinned session"), SessionID: conversationID, EventType: "session.keepAliveFailed", CollapseID: key})
		if ctx.Err() != nil {
			return
		}
		if err := d.Registry.MarkKeepAliveAnnounced(session.ID, state.FailedAt); err != nil {
			d.log.Printf("mark keep-alive failure announced for %s: %s", d.resolveDisplay(session.ID), err)
		}
	})
	if !started {
		finished()
	}
}
