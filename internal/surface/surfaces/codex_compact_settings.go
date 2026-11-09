package surfaces

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const codexCompactLimit = "model_auto_compact_token_limit"

func (c *Codex) CompactSettings(_ context.Context, sess *surface.Session) ([]surface.TurnSetting, error) {
	store := c.codexConfigStore()
	if store == nil {
		return nil, nil
	}
	overrides, err := store.CodexConfigOverrides(sess.ID)
	if err != nil {
		return nil, err
	}
	return []surface.TurnSetting{surface.CompactWindowSetting(codexLimitValue(overrides), surface.TurnSettingAppliesNextLoad, "")}, nil
}

func (c *Codex) SetCompactSetting(ctx context.Context, sess *surface.Session, key, value string) error {
	if key != surface.CompactSettingWindow {
		return fmt.Errorf("%w: a Codex thread offers only its compact window", surface.ErrUnsupported)
	}
	if err := surface.ValidCompactWindow(value); err != nil {
		return err
	}
	store := c.codexConfigStore()
	if store == nil {
		return fmt.Errorf("%w: this Codex has no config store", surface.ErrUnsupported)
	}
	if sess.Status == surface.StatusBusy {
		return fmt.Errorf("%w: the thread is working; change its compact window once it is idle", surface.ErrInvalidTurnSetting)
	}
	var limit any
	if value != surface.CompactAuto {
		limit, _ = strconv.ParseInt(value, 10, 64)
	}
	lock, err := acquireCodexWriteLock(ctx)
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	defer releaseCodexWriteLock(lock)
	client, err := c.openRoute(ctx, sess)
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	defer client.Close()
	if err := c.requireDirectInput(ctx, client, sess); err != nil {
		if surface.IsDeliveryTerminal(err) {
			return err
		}
		return surface.DeliveryUnavailable(err)
	}
	previous, err := store.SetCodexConfigOverride(sess.ID, codexCompactLimit, limit)
	if err != nil {
		return err
	}
	return c.reloadThread(ctx, client, sess, func() error { return store.RestoreCodexConfigOverrides(sess.ID, previous) })
}

// A reload Codex refused is undone before any follower resumes with it.
func (c *Codex) reloadThread(ctx context.Context, client codexClient, sess *surface.Session, undo func() error) error {
	release := c.subscriptions.exclusive(sess.ID)
	var dropped []codexClient
	for _, follower := range c.subscriptions.clients(sess.ID) {
		if follower == client {
			continue
		}
		c.subscriptions.remove(sess.ID, follower)
		if _, err := follower.Request(ctx, "thread/unsubscribe", map[string]any{"threadId": sess.ID}, 5*time.Second); err != nil {
			_ = follower.Close()
			continue
		}
		dropped = append(dropped, follower)
	}
	sent, err := c.resumeAlone(ctx, client, sess)
	var refused *codexRPCError
	switch {
	case err == nil:
	case !sent || errors.As(err, &refused):
		if undoErr := undo(); undoErr != nil {
			err = fmt.Errorf("%w; restoring the previous compact window also failed: %v", err, undoErr)
		}
	default:
		err = fmt.Errorf("%w; Codex may have taken the new compact window, so it stays set and applies the next time Codex loads this thread", err)
	}
	// Restored under the gate, so a follower leaving meanwhile leaves after it is restored.
	for _, follower := range dropped {
		if !c.followLoadedThread(ctx, follower, sess.ID) {
			_ = follower.Close()
		}
	}
	release()
	return err
}

var codexReloadWait = 10 * time.Second

func (c *Codex) resumeAlone(ctx context.Context, client codexClient, sess *surface.Session) (sent bool, err error) {
	c.subscriptions.remove(sess.ID, client)
	if _, err := client.Request(ctx, "thread/unsubscribe", map[string]any{"threadId": sess.ID}, 5*time.Second); err != nil {
		return false, fmt.Errorf("thread/unsubscribe: %w", err)
	}
	if _, err := c.resumeThread(ctx, client, sess, map[string]any{"threadId": sess.ID}, codexReloadWait); err != nil {
		return true, fmt.Errorf("reload the thread with its compact window: %w", err)
	}
	c.subscriptions.add(sess.ID, client)
	return true, nil
}

func codexLimitValue(config map[string]any) string {
	switch limit := config[codexCompactLimit].(type) {
	case float64:
		return strconv.FormatInt(int64(limit), 10)
	case int64:
		return strconv.FormatInt(limit, 10)
	case int:
		return strconv.Itoa(limit)
	}
	return ""
}
