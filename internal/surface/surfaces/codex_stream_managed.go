package surfaces

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// managedNotificationsHandled, when set by a test, runs after a managed
// stream has handled the notifications it drained.
var managedNotificationsHandled func()

// codexNotifier is a client that delivers app-server notifications as they
// arrive rather than only alongside a response.
type codexNotifier interface {
	DrainNotifications() []codexEvent
	Notified() <-chan struct{}
}

// managedStreamRead reads what the stream follows: the newest turn, or the
// whole thread when the stream's turn is no longer the newest.
func (c *Codex) managedStreamRead(ctx context.Context, client codexClient, threadID, turnID string) (*codexThread, error) {
	thread, err := c.readLatestTurn(ctx, client, threadID)
	if err != nil {
		return nil, err
	}
	if turnID != "" && codexTurnByID(thread, turnID) == nil {
		return c.readThread(ctx, client, threadID)
	}
	return thread, nil
}

// subscribeManagedThread asks the app-server to send this connection the
// thread's item, turn and token notifications. It subscribes only to a loaded
// thread, since resuming one that is not loaded would load it.
func (c *Codex) subscribeManagedThread(ctx context.Context, client codexClient, threadID string) bool {
	loaded, err := c.loadedThreadIDs(ctx, client, 0)
	if err != nil || !slices.Contains(loaded, threadID) {
		return false
	}
	_, err = client.Request(ctx, "thread/resume", map[string]any{"threadId": threadID, "excludeTurns": true}, 5*time.Second)
	return err == nil
}

func (c *Codex) streamManagedClient(ctx context.Context, client codexClient, sess *surface.Session, uuid string, onEvent func(surface.StreamEvent), commands *commandsWatch, timeout time.Duration) error {
	emitted := ""
	baselineTurnID := ""
	finalEmitted := false
	var lastContext surface.ContextUsage
	var nextContextPoll time.Time
	notifier, _ := client.(codexNotifier)
	subscribed := notifier != nil && c.subscribeManagedThread(ctx, client, sess.ID)
	// handle forwards the thread's notifications and reports whether one may
	// have changed what a read returns.
	handle := func() bool {
		if notifier == nil {
			return false
		}
		changed := false
		for _, event := range notifier.DrainNotifications() {
			if event.Method == "skills/changed" && commands != nil {
				commands.Refresh()
				continue
			}
			if !codexContainsID(event.Params, sess.ID) {
				continue
			}
			if !strings.HasSuffix(event.Method, "/delta") {
				changed = true
			}
			if event.Method == "thread/status/changed" && !subscribed {
				subscribed = c.subscribeManagedThread(ctx, client, sess.ID)
			}
			if goalEvent, ok := codexGoalStreamEvent(event); ok {
				onEvent(goalEvent)
				continue
			}
			if usage, matched := c.applyContextEvent(sess, event, lastContext); matched {
				lastContext = *usage
				onEvent(surface.StreamEvent{Kind: "context", Context: usage})
			}
		}
		if managedNotificationsHandled != nil {
			managedNotificationsHandled()
		}
		return changed
	}
	if uuid == "" {
		thread, readErr := c.readLatestTurn(ctx, client, sess.ID)
		if readErr != nil {
			return readErr
		}
		if len(thread.Turns) > 0 {
			latest := thread.Turns[len(thread.Turns)-1]
			if latest.Done {
				baselineTurnID = latest.ID
			} else {
				uuid = latest.ID
			}
		}
	}
	var notified <-chan struct{}
	if notifier != nil {
		notified = notifier.Notified()
	}
	deadline := time.Now().Add(timeout)
	for first := true; first || time.Now().Before(deadline); first = false {
		thread, err := c.managedStreamRead(ctx, client, sess.ID, uuid)
		if err != nil {
			return err
		}
		// A change reported while reading may postdate the read.
		reread := handle()
		if !time.Now().Before(nextContextPoll) {
			nextContextPoll = time.Now().Add(time.Second)
			if usage, usageErr := c.ContextUsage(ctx, sess); usageErr == nil && usage != nil && *usage != lastContext {
				lastContext = *usage
				onEvent(surface.StreamEvent{Kind: "context", Context: usage})
			}
		}
		turn := codexTurnByID(thread, uuid)
		if uuid == "" && len(thread.Turns) > 0 {
			latest := &thread.Turns[len(thread.Turns)-1]
			if latest.ID != baselineTurnID || !latest.Done {
				uuid = latest.ID
				turn = latest
			}
		}
		if turn != nil {
			if turn.Done && turn.Error != "" {
				return fmt.Errorf("Codex turn %s did not complete successfully: %s", turn.ID, turn.Error)
			}
			assistant, hasAssistant := turn.authoritativeAssistant()
			if hasAssistant && turn.Done {
				if !finalEmitted {
					onEvent(codexManagedAuthoritativeStreamEvent(turn.ID, assistant))
					finalEmitted = true
				}
				emitted = assistant.Text
			} else if hasAssistant && assistant.Text != emitted {
				emitted = assistant.Text
				key := "managed:" + turn.ID
				onEvent(surface.StreamEvent{ID: key + ":text", ProviderKey: key + ":text", Version: uint64(len(assistant.Text)), Operation: "upsert", TurnID: turn.ID, Kind: "text", Text: assistant.Text})
			}
			if turn.Done {
				key := "managed:" + turn.ID
				version := uint64(len(turn.Assistant))
				if hasAssistant {
					version = uint64(len(assistant.Text))
				}
				onEvent(surface.StreamEvent{ID: key + ":done", ProviderKey: key + ":done", Version: version, Operation: "phase", TurnID: turn.ID, Kind: "done", Turn: codexRolloutTurnMetadata(ctx, codexTranscriptPath(sess), turn.ID)})
				return nil
			}
		}
		if reread {
			continue
		}
		// Wait for a notification that may change the turn, or the poll
		// interval, which still covers anything the app-server does not send.
		timer := time.NewTimer(transcriptPollInterval)
	wait:
		for {
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-notified:
				if handle() {
					break wait
				}
			case <-timer.C:
				break wait
			}
		}
		timer.Stop()
	}
	return fmt.Errorf("stream timed out after %s: %w", timeout, surface.ErrStreamWindow)
}
