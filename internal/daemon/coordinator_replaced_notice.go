package daemon

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/coordinator"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const replacementAnnounceEvery = 30 * time.Second

// replacementAnnouncer keeps one announcing pass at a time, and the person
// told once per handover within this daemon even when a later notice retries.
type replacementAnnouncer struct {
	mu     sync.Mutex
	pushed map[int64]bool
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

// The handover is marked announced only once the person and any agent sender
// have been told; the old lead's notice is a best-effort attempt after that.
func (d *Daemon) announceReplacement(ctx context.Context, entry registry.CoordinatorHistoryEntry) error {
	if d.replacements.pushed == nil {
		d.replacements.pushed = map[int64]bool{}
	}
	if !d.replacements.pushed[entry.ID] {
		d.notifyReplacement(ctx, entry)
		d.replacements.pushed[entry.ID] = true
	}
	if entry.NotifySender && d.agentSender(entry.ChangedBy) {
		message := fmt.Sprintf("[agenthail] Your message to @%s started a new conversation, %s, because %s. The previous conversation %s is no longer the lead. Read its reply with 'agenthail last @%s'.", entry.Name, entry.SessionID, replacementWhy(entry), entry.PreviousSessionID, entry.Name)
		if _, err := d.sendNotice(noticeRecipient{sessionID: entry.ChangedBy, role: "sender"}, surface.Session{ID: entry.SessionID}, message, replacementNoticeKey(entry.ID, "sender")); err != nil {
			return fmt.Errorf("tell the sender %s: %w", entry.ChangedBy, err)
		}
	}
	if err := d.Registry.MarkCoordinatorReplacementAnnounced(entry.ID, time.Now()); err != nil {
		return err
	}
	delete(d.replacements.pushed, entry.ID)
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
	why, _ := strings.CutPrefix(entry.Reason, "gone: ")
	return why
}

func (d *Daemon) changedByDisplay(changedBy string) string {
	switch changedBy {
	case surface.PersonSessionID:
		return "the person"
	case surface.CLISessionID, "":
		return "the CLI"
	case coordinator.RestartedByDaemon:
		return "the daemon"
	}
	return d.resolveDisplay(changedBy)
}

func (d *Daemon) agentSender(changedBy string) bool {
	switch changedBy {
	case "", surface.PersonSessionID, surface.CLISessionID, coordinator.RestartedByDaemon:
		return false
	}
	return true
}
