package surfaces

import (
	"context"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

var managedStateRetry = time.Second

// watchManagedState forwards a managed thread's goal, context and command
// changes until ctx ends, reconnecting to the app-server when its connection
// is lost. The thread's content comes from its rollout, not from here.
func (c *Codex) watchManagedState(ctx context.Context, sess surface.Session, onEvent func(surface.StreamEvent), commands *commandsWatch) {
	var last surface.ContextUsage
	for ctx.Err() == nil {
		if client, err := c.openReader(ctx, &sess); err == nil {
			c.watchManagedClient(ctx, client, &sess, onEvent, commands, &last)
			_ = client.Close()
		}
		select {
		case <-ctx.Done():
		case <-time.After(managedStateRetry):
		}
	}
}

func (c *Codex) watchManagedClient(ctx context.Context, client codexClient, sess *surface.Session, onEvent func(surface.StreamEvent), commands *commandsWatch, last *surface.ContextUsage) {
	poll := func() {
		if usage, err := c.ContextUsage(ctx, sess); err == nil && usage != nil && *usage != *last {
			*last = *usage
			onEvent(surface.StreamEvent{Kind: "context", Context: usage})
		}
	}
	poll()
	defer c.subscriptions.remove(sess.ID, client)
	notifier, _ := client.(codexNotifier)
	subscribed := notifier != nil && c.subscribeManagedThread(ctx, client, sess.ID)
	var notified <-chan struct{}
	if notifier != nil {
		notified = notifier.Notified()
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if lost, ok := client.(interface{ failed() bool }); ok && lost.failed() {
				return
			}
			poll()
		case <-notified:
			for _, event := range notifier.DrainNotifications() {
				if event.Method == "skills/changed" && commands != nil {
					commands.Refresh()
					continue
				}
				if !codexContainsID(event.Params, sess.ID) {
					continue
				}
				if event.Method == "thread/status/changed" && !subscribed {
					subscribed = c.subscribeManagedThread(ctx, client, sess.ID)
				}
				if goalEvent, ok := codexGoalStreamEvent(event); ok {
					onEvent(goalEvent)
					continue
				}
				if usage, matched := c.applyContextEvent(sess, event, *last); matched {
					*last = *usage
					onEvent(surface.StreamEvent{Kind: "context", Context: usage})
				}
			}
			if managedNotificationsHandled != nil {
				managedNotificationsHandled()
			}
		}
	}
}
