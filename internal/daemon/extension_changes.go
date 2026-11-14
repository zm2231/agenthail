package daemon

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/extension"
	"github.com/zm2231/agenthail/internal/registry"
)

type extensionChanges struct {
	home string

	mu      sync.Mutex
	history *extension.History

	queueMu sync.Mutex
	queue   []extensionTurnEvent
	wake    chan struct{}
	// Only the worker touches inFlight.
	inFlight map[string]bool
}

type extensionTurnKind int

const (
	extensionTurnStart extensionTurnKind = iota
	extensionTurnEnd
	extensionTurnIdle
)

type extensionTurnEvent struct {
	kind      extensionTurnKind
	sessionID string
	turnID    string
	done      chan struct{}
}

func newExtensionChanges(home string) *extensionChanges {
	return &extensionChanges{home: home, wake: make(chan struct{}, 1), inFlight: map[string]bool{}}
}

// A host that has never had an extensions folder gets no repository.
func (c *extensionChanges) open(existing bool) (*extension.History, error) {
	if _, err := os.Stat(extension.Workspace(c.home)); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if c.history != nil {
		return c.history, nil
	}
	history, err := extension.OpenHistory(c.home, existing)
	if err != nil {
		return nil, err
	}
	c.history = history
	return history, nil
}

func (d *Daemon) extensionHistory() (*extension.History, error) {
	return d.openExtensionHistory(true)
}

func (d *Daemon) openExtensionHistory(existing bool) (*extension.History, error) {
	if d.extensions == nil {
		return nil, errExtensionsOff
	}
	c := d.extensions.changes
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.open(existing)
}

// Turn starts and ends are handled one at a time in the order the observer
// saw them, so a turn's commit is made before the next turn's start is
// looked at.
func (d *Daemon) queueExtensionTurn(event extensionTurnEvent) {
	if d.extensions == nil {
		return
	}
	c := d.extensions.changes
	c.queueMu.Lock()
	c.queue = append(c.queue, event)
	c.queueMu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// A turn's end still queued at shutdown is recorded before the worker
// returns; left uncommitted, the next quiet start would take it for the
// person's edit.
func (d *Daemon) runExtensionTurns(ctx context.Context) {
	for {
		d.handleExtensionTurns()
		select {
		case <-ctx.Done():
			d.handleExtensionTurns()
			return
		case <-d.extensions.changes.wake:
		}
	}
}

func (d *Daemon) handleExtensionTurns() {
	c := d.extensions.changes
	for {
		c.queueMu.Lock()
		events := c.queue
		c.queue = nil
		c.queueMu.Unlock()
		if len(events) == 0 {
			return
		}
		for _, event := range events {
			switch event.kind {
			case extensionTurnStart:
				d.extensionTurnStarted(event.sessionID)
			case extensionTurnEnd:
				if rev, changed := d.extensionTurnCompleted(event.sessionID, event.turnID); changed {
					d.background.start(func(ctx context.Context) { d.applyExtensionChange(ctx, rev) })
				}
			case extensionTurnIdle:
				delete(c.inFlight, event.sessionID)
			}
			if event.done != nil {
				close(event.done)
			}
		}
	}
}

// Edits found when a turn starts are the person's only if no other turn is
// running; otherwise they may be that turn's, and are left for the next
// turn's commit.
func (d *Daemon) extensionTurnStarted(sessionID string) {
	c := d.extensions.changes
	quiet := len(c.inFlight) == 0 || (len(c.inFlight) == 1 && c.inFlight[sessionID])
	c.inFlight[sessionID] = true
	if !quiet {
		return
	}
	history, err := d.extensionHistory()
	if err != nil || history == nil {
		d.logExtensionHistory(err)
		return
	}
	if _, _, err := history.CommitHandEdit(); err != nil {
		d.log.Printf("extensions: record edits made by hand: %s", err)
	}
}

func (d *Daemon) extensionTurnCompleted(sessionID, turnID string) (string, bool) {
	c := d.extensions.changes
	delete(c.inFlight, sessionID)
	history, err := d.openExtensionHistory(false)
	if err != nil || history == nil {
		d.logExtensionHistory(err)
		return "", false
	}
	var also []string
	for id := range c.inFlight {
		also = append(also, id)
	}
	slices.Sort(also)
	change, changed, err := history.CommitTurn(sessionID, d.resolveDisplay(sessionID), turnID, also)
	if err != nil {
		d.log.Printf("extensions: record %s's turn: %s", d.resolveDisplay(sessionID), err)
		return "", false
	}
	if !changed {
		return "", false
	}
	return change.Rev, true
}

// Agent changes take effect without asking; asks raised before that are
// withdrawn once so nothing is left waiting that can no longer be answered.
func (d *Daemon) withdrawExtensionChangeAsks() {
	open, err := d.Registry.OpenAsks()
	if err != nil {
		d.log.Printf("extensions: read open asks: %s", err)
		return
	}
	for _, ask := range open {
		if ask.Origin != registry.AskOriginExtensionChange {
			continue
		}
		if withdrawn, err := d.Registry.WithdrawAsk(ask.ID, time.Now()); err != nil {
			d.log.Printf("extensions: withdraw %s: %s", ask.ID, err)
		} else if withdrawn {
			d.withdrawAsk(ask.ID)
			d.publishAsk(ask.SessionID)
		}
	}
	if d.extensions != nil {
		_ = os.Remove(filepath.Join(extension.DataDir(d.extensions.home), "changes.json"))
	}
}

func (d *Daemon) logExtensionHistory(err error) {
	if err != nil && !errors.Is(err, errExtensionsOff) {
		d.log.Printf("extensions: history: %s", err)
	}
}

func (d *Daemon) applyExtensionChange(ctx context.Context, rev string) {
	if err := d.syncExtensionMounts(); err != nil {
		d.log.Printf("extensions: sync mounts after %s: %s", rev, err)
	}
	if err := d.reloadExtensions(ctx); err != nil && !errors.Is(err, errExtensionHostDown) {
		d.log.Printf("extensions: reload after %s: %s", rev, err)
	}
	ids := []string{}
	if history, err := d.extensionHistory(); err == nil && history != nil {
		if changes, err := history.Log(1, rev); err == nil && len(changes) == 1 {
			for _, files := range changes[0].Extensions {
				ids = append(ids, files.ID)
			}
		}
	}
	d.publishEvent("extension.changed", "", map[string]any{"rev": rev, "extensions": ids})
}

func (d *Daemon) undoExtensionChange(ctx context.Context, rev string) (extension.Undone, error) {
	history, err := d.extensionHistory()
	if err != nil {
		return extension.Undone{}, actionFailed(http.StatusServiceUnavailable, "unavailable", err.Error())
	}
	if history == nil {
		return extension.Undone{}, invalidRequest("there is no extensions folder")
	}
	c := d.extensions.changes
	c.mu.Lock()
	undone, err := history.Undo(rev)
	c.mu.Unlock()
	if errors.Is(err, extension.ErrUndoLater) || errors.Is(err, extension.ErrUnrecorded) {
		return extension.Undone{}, actionFailed(http.StatusConflict, "conflict", err.Error())
	}
	if err != nil {
		return extension.Undone{}, invalidRequest(err.Error())
	}
	for _, id := range undone.Removed {
		if err := d.extensions.approvals.Forget(id); err != nil {
			d.log.Printf("extensions: forget %s's approvals: %s", id, err)
		}
		if err := d.extensions.calls.forget(id); err != nil {
			d.log.Printf("extensions: forget %s's calls: %s", id, err)
		}
	}
	if len(undone.Removed) > 0 {
		if err := d.dropExtensionTables(undone.Removed); err != nil {
			d.log.Printf("extensions: drop tables of %s: %s", strings.Join(undone.Removed, ", "), err)
		}
	}
	d.applyExtensionChange(ctx, undone.Reverted.Rev)
	return undone, nil
}

// ah.table joins the id and the name with an underscore, so "a_b_x" may be
// extension a's table "b_x" or extension a-b's table "x"; a table that also
// fits an extension still in the folder is left alone.
func (d *Daemon) dropExtensionTables(removed []string) error {
	path := extension.StorePath(d.extensions.home)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	found, err := extension.Discover(extension.Workspace(d.extensions.home))
	if err != nil {
		return err
	}
	var kept []string
	for _, ext := range found {
		kept = append(kept, extensionTablePrefix(ext.ID))
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT type, name FROM sqlite_master WHERE type IN ('table','view')`)
	if err != nil {
		return err
	}
	type object struct{ kind, name string }
	var drop []object
	for rows.Next() {
		var o object
		if err := rows.Scan(&o.kind, &o.name); err != nil {
			rows.Close()
			return err
		}
		if extensionOwnsTable(o.name, removed, kept) {
			drop = append(drop, o)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, o := range drop {
		if _, err := db.Exec(`DROP ` + strings.ToUpper(o.kind) + ` IF EXISTS "` + strings.ReplaceAll(o.name, `"`, `""`) + `"`); err != nil {
			return err
		}
	}
	return nil
}

func extensionTablePrefix(id string) string { return strings.ReplaceAll(id, "-", "_") + "_" }

func extensionOwnsTable(name string, removed, kept []string) bool {
	owned := false
	for _, id := range removed {
		if strings.HasPrefix(name, extensionTablePrefix(id)) {
			owned = true
		}
	}
	if !owned {
		return false
	}
	for _, prefix := range kept {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	return true
}
