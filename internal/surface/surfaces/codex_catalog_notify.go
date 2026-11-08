package surfaces

import (
	"context"
	"time"
)

var codexWatchRetry = time.Second

// NotifyCatalog reports a managed thread whose status changes, such as one
// that starts waiting on an approval, which no file shows. The app-server
// sends every initialized connection each thread's status changes without a
// subscription, so one connection that subscribes to nothing covers every
// thread and keeps none of them loaded. The watch never starts the
// app-server: it attaches once one is running, and reports on attaching,
// since threads may have changed while it was away.
func (c *Codex) NotifyCatalog(ctx context.Context, changed func()) {
	if !c.managed {
		return
	}
	for {
		if client, err := dialManagedCodex(ctx); err == nil {
			changed()
			forwardCodexStatusChanges(ctx, client, changed)
			_ = client.Close()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(codexWatchRetry):
		}
	}
}

func forwardCodexStatusChanges(ctx context.Context, client *managedCodexClient, changed func()) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-client.Done():
			return
		case <-client.Notified():
			report := false
			for _, event := range client.DrainNotifications() {
				report = report || event.Method == "thread/status/changed"
			}
			if report {
				changed()
			}
		}
	}
}
