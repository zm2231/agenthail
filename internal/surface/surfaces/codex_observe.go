package surfaces

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Codex) Observe(ctx context.Context, sess *surface.Session) (*surface.TurnObservation, error) {
	runtimes := c.openRuntimes(ctx)
	conn := runtimes.reader(sess.ID)
	if conn == nil {
		return nil, runtimes.unavailable()
	}
	unheld := c.unheld(ctx, runtimes, sess, time.Now())
	runtimes.release(conn)
	defer conn.Close()
	thread, err := c.readObservationThread(ctx, conn, sess.ID)
	if err != nil {
		return nil, err
	}
	observation := codexObservation(thread)
	// A thread with no settled turn says how it stands only through its own
	// status; one Codex has not loaded is loaded again by a send.
	if observation.Status == surface.StatusUnknown {
		if notLoaded, err := codexThreadReadsNotLoaded(ctx, conn, sess.ID); err == nil && notLoaded {
			observation.Status = surface.StatusNotLoaded
		}
	}
	if observation.Status == surface.StatusBusy {
		// Only thread status carries the pending approval and input flags.
		response, err := conn.Request(ctx, "thread/read", map[string]any{"threadId": sess.ID, "includeTurns": false}, 5*time.Second)
		if err != nil {
			return nil, fmt.Errorf("thread/read: %w", err)
		}
		result, _ := response["result"].(map[string]any)
		value, _ := result["thread"].(map[string]any)
		thread.Waiting = codexWaitingKind(value["status"])
		observation = codexObservation(thread)
	}
	observation = dropUnconfirmedTurn(thread, observation)
	observation.Unheld = unheld && observation.Status != surface.StatusBusy
	observation.WaitingOn = c.preciseWait(ctx, conn, sess, observation.WaitingOn)
	if path := c.home.transcriptPath(sess); path != "" {
		if info, err := os.Stat(path); err == nil {
			observation.Plan = c.plan(ctx, path, info)
			observation.BackgroundTasks = c.codexBackground(ctx, sess.ID, path, info)
		}
	}
	return observation, nil
}

const codexTurnInterrupted = "turn interrupted"

func codexThreadReadsNotLoaded(ctx context.Context, conn codexClient, threadID string) (bool, error) {
	response, err := conn.Request(ctx, "thread/read", map[string]any{"threadId": threadID, "includeTurns": false}, 5*time.Second)
	if err != nil {
		return false, err
	}
	result, _ := response["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	status, _ := thread["status"].(map[string]any)
	return str(status, "type") == "notLoaded", nil
}

func dropUnconfirmedTurn(thread *codexThread, observation *surface.TurnObservation) *surface.TurnObservation {
	for index := len(thread.Turns) - 1; index >= 0; index-- {
		turn := thread.Turns[index]
		if turn.ID == "" || !turn.Done {
			continue
		}
		if !turn.Unsettled {
			return observation
		}
		thread.Turns = append(thread.Turns[:index:index], thread.Turns[index+1:]...)
		observation = codexObservation(thread)
		observation.TurnUnconfirmed = true
		return observation
	}
	return observation
}

// activeTurnID returns the thread's running turn, or "" when none runs. A new
// thread whose turns cannot be listed yet has none, so its first turn can start.
// A newest turn listed unsettled is running if this process started it and
// has not seen it end; otherwise it is listed again until it shows running or
// ended, for at most codexUnsettledTurnWait: a starting turn shows running
// once its thread turns active, and a turn a crashed server left behind never
// does.
func (c *Codex) activeTurnID(ctx context.Context, conn codexClient, threadID, expected string) (string, error) {
	deadline := time.Now().Add(codexUnsettledTurnWait)
	for {
		newest, err := codexNewestTurn(ctx, conn, threadID)
		if err != nil {
			return "", err
		}
		if status, _, _ := codexTurnState(newest["status"]); status == surface.StatusBusy {
			return str(newest, "id"), nil
		}
		if newest != nil && codexTurnUnsettled(newest) && c.turnStarts.running(threadID, str(newest, "id")) {
			return str(newest, "id"), nil
		}
		unsettled := newest != nil && codexTurnUnsettled(newest)
		unlisted := expected != "" && str(newest, "id") != expected
		if !(unsettled || unlisted) || !time.Now().Before(deadline) {
			return "", nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

var codexUnsettledTurnWait = 2 * time.Second

func codexNewestTurn(ctx context.Context, conn codexClient, threadID string) (map[string]any, error) {
	response, err := conn.Request(ctx, "thread/turns/list", map[string]any{
		"threadId":      threadID,
		"limit":         1,
		"sortDirection": "desc",
	}, 5*time.Second)
	if codexTurnsNotListedYet(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("thread/turns/list: %w", err)
	}
	result, _ := response["result"].(map[string]any)
	turns, _ := result["data"].([]any)
	if len(turns) == 0 {
		return nil, nil
	}
	newest, _ := turns[0].(map[string]any)
	return newest, nil
}

const codexQuietAfter = 5 * time.Minute

// A plain codex terminal is no runtime the daemon can ask, but it keeps writing the rollout.
func (c *Codex) unheld(ctx context.Context, runtimes *codexRuntimes, sess *surface.Session, now time.Time) bool {
	if len(runtimes.loadFailures) > 0 {
		return false
	}
	if c.desktopURL != "" && (runtimes.desktop == nil || runtimes.desktopLoaded == nil || runtimes.desktopLoaded[sess.ID]) {
		return false
	}
	if runtimes.managed == nil {
		if _, err := os.Stat(c.home.socketPath()); !errors.Is(err, os.ErrNotExist) {
			return false
		}
	} else if runtimes.managedLoaded == nil || runtimes.managedLoaded[sess.ID] {
		return false
	}
	path := c.home.transcriptPath(sess)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || now.Sub(info.ModTime()) <= codexQuietAfter || codexTurnOpen(path, info) {
		return false
	}
	return !surface.BackgroundRunning(c.transcriptBackground(ctx, path, info))
}

func codexTurnOpen(path string, info os.FileInfo) bool {
	file, err := os.Open(path)
	if err != nil {
		return true
	}
	defer file.Close()
	open := false
	scanJSONLBackward(file, info.Size(), info.Size(), func(line []byte, _ int64) bool {
		record, _, ok := parseCodexLiveTurnRecord(line)
		if !ok {
			return true
		}
		started, ended := codexLifecycle(record)
		open = started
		return !started && !ended
	})
	return open
}
