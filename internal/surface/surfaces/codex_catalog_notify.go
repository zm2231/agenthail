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
// since threads may have changed while it was away. The same connection lists
// loaded threads' background terminals, which nothing observes once it drops.
func (c *Codex) NotifyCatalog(ctx context.Context, changed func()) {
	if !c.managed {
		return
	}
	for {
		if client, err := c.home.dialManaged(ctx); err == nil {
			changed()
			c.forwardCodexStatusChanges(ctx, client, changed)
			_ = client.Close()
			if c.terminals.reset(false) {
				changed()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(codexWatchRetry):
		}
	}
}

func (c *Codex) forwardCodexStatusChanges(ctx context.Context, client *managedCodexClient, changed func()) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	watch := newTerminalWatch(client, &c.terminals)
	requests := newRequestWatch(c, client)
	if ids, err := c.loadedThreadIDs(ctx, client, 0); err == nil {
		watch.seed(ids, time.Now())
		requests.seed(ctx, ids)
	}
	ticker := time.NewTicker(codexTerminalsTick)
	defer ticker.Stop()
	watch.start(ctx, time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case <-client.Done():
			return
		case <-ticker.C:
			watch.start(ctx, time.Now())
		case result := <-watch.results:
			if watch.finish(result, time.Now()) {
				changed()
			}
			watch.start(ctx, time.Now())
		case result := <-requests.results:
			if requests.finish(ctx, result) {
				changed()
			}
		case seeded := <-requests.seeded:
			requests.applySeed(ctx, seeded)
		case <-client.Notified():
			report := false
			for _, event := range client.DrainNotifications() {
				if requests.heard(event) {
					report = true
					continue
				}
				if event.Method != "thread/status/changed" {
					continue
				}
				report = true
				threadID, _ := event.Params["threadId"].(string)
				watch.status(threadID, codexStatusType(event.Params["status"]), time.Now())
				requests.status(ctx, threadID, event.Params["status"])
			}
			if report {
				changed()
				watch.start(ctx, time.Now())
			}
		}
	}
}
