package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/coordinator"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const replacementAnnounceEvery = 30 * time.Second

// replacementAnnouncer keeps one announcing pass at a time.
type replacementAnnouncer struct {
	mu sync.Mutex
}

func replacementNoticeKey(historyID int64, recipient string) string {
	return fmt.Sprintf("coordinator-replaced:%d:%s", historyID, recipient)
}

func (d *Daemon) runReplacementAnnouncements(ctx context.Context) {
	ticker := time.NewTicker(replacementAnnounceEvery)
	defer ticker.Stop()
	d.announceReplacements(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.announceReplacements(ctx)
		}
	}
}

func (d *Daemon) announceReplacementsSoon() {
	d.background.start(d.announceReplacements)
}

func (d *Daemon) announceReplacements(ctx context.Context) {
	d.replacements.mu.Lock()
	defer d.replacements.mu.Unlock()
	entries, err := d.Registry.UnannouncedCoordinatorReplacements()
	if err != nil {
		d.log.Printf("read coordinator handovers to announce: %s", err)
		return
	}
	for _, entry := range entries {
		if err := d.announceReplacement(ctx, entry); err != nil {
			d.log.Printf("announce that @%s was replaced: %s", entry.Name, err)
		}
	}
}

// A crash between the push and its record resends it under the same identifier, which replaces rather than repeats it.
func (d *Daemon) announceReplacement(ctx context.Context, entry registry.CoordinatorHistoryEntry) error {
	if !entry.PersonNotified {
		d.notifyReplacement(ctx, entry)
		if err := d.Registry.MarkCoordinatorReplacementPersonNotified(entry.ID, time.Now()); err != nil {
			return fmt.Errorf("record that the person was told: %w", err)
		}
	}
	if entry.NotifySender && coordinator.ChangedByAgent(entry.ChangedBy) {
		message := fmt.Sprintf("[agenthail] Your message to @%s started a new conversation, %s, because %s. The previous conversation %s is no longer the lead. Read its reply with 'agenthail last @%s'.", entry.Name, entry.SessionID, replacementWhy(entry), entry.PreviousSessionID, entry.Name)
		if _, err := d.sendNotice(noticeRecipient{sessionID: entry.ChangedBy, role: "sender"}, surface.Session{ID: entry.SessionID}, message, replacementNoticeKey(entry.ID, "sender")); errors.Is(err, sql.ErrNoRows) {
			d.log.Printf("the sender %s that replaced @%s no longer exists; nobody to tell", entry.ChangedBy, entry.Name)
		} else if err != nil {
			return fmt.Errorf("tell the sender %s: %w", entry.ChangedBy, err)
		}
	}
	if err := d.Registry.MarkCoordinatorReplacementAnnounced(entry.ID, time.Now()); err != nil {
		return err
	}
	d.publishEvent("state.changed", "", map[string]any{"source": "lead-replacement"})
	d.background.start(func(ctx context.Context) { d.tellReplacedLead(ctx, entry) })
	return nil
}

func (d *Daemon) notifyReplacement(ctx context.Context, entry registry.CoordinatorHistoryEntry) {
	key := replacementNoticeKey(entry.ID, "person")
	message := fmt.Sprintf("@%s now runs in %s, replacing %s: %s (by %s).", entry.Name, d.resolveDisplay(entry.SessionID), entry.PreviousSessionID, replacementWhy(entry), d.changedByDisplay(entry.ChangedBy))
	if err := d.notifyDesktop("Lead replaced", message, entry.SessionID, "agenthail-"+key, "", nil); err != nil {
		d.log.Printf("desktop notification: %s", err)
	}
	notifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	d.notifyPairedDevices(notifyCtx, mobileNotification{Title: "Lead replaced", Message: message, SessionID: entry.SessionID, EventType: "coordinator.replaced", CollapseID: key})
}

// tellReplacedLead reaches the old conversation only if it turns out to still
// exist; a queued notice would be moved to the new lead like any other message.
func (d *Daemon) tellReplacedLead(ctx context.Context, entry registry.CoordinatorHistoryEntry) {
	old, err := d.Registry.Session(entry.PreviousSessionID)
	if err != nil {
		return
	}
	adapter, err := d.adapters().For(old)
	if err != nil {
		return
	}
	if keeper, ok := adapter.(surface.ConversationKeeper); ok {
		state, err := keeper.ConversationState(ctx, old)
		if err != nil || (state != surface.ConversationLive && state != surface.ConversationResumable) {
			return
		}
	}
	message := fmt.Sprintf("[agenthail] You are no longer coordinator @%s. Conversation %s took over at %s because %s. Stop working as the lead and end your turn now; messages for @%s go to %s.", entry.Name, entry.SessionID, entry.At.Local().Format(time.Kitchen), replacementWhy(entry), entry.Name, entry.SessionID)
	operationCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	if _, err := d.dispatcher().DeliverWithoutQueue(operationCtx, adapter, old, message, replacementNoticeKey(entry.ID, "previous"), surface.SendOptions{SourceSessionID: entry.SessionID}); err != nil {
		d.log.Printf("tell %s it is no longer @%s: %s", entry.PreviousSessionID, entry.Name, err)
	}
}

func replacementWhy(entry registry.CoordinatorHistoryEntry) string {
	return coordinator.ReplacementWhy(entry.Reason)
}

func (d *Daemon) changedByDisplay(changedBy string) string {
	return coordinator.ChangedByLabel(changedBy, d.resolveDisplay)
}
