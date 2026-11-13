package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/atomicfile"
	"github.com/zm2231/agenthail/internal/extension"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	extensionKeep = "keep"
	extensionUndo = "undo"
)

// Every session that changed the extensions folder has at most one open ask
// listing its changes not yet kept or undone, oldest first.
type pendingExtensionChanges struct {
	AskID string   `json:"askId"`
	Revs  []string `json:"revs"`
}

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

func (c *extensionChanges) pendingPath() string {
	return filepath.Join(extension.DataDir(c.home), "changes.json")
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

func (c *extensionChanges) readPending() (map[string]pendingExtensionChanges, error) {
	data, err := os.ReadFile(c.pendingPath())
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]pendingExtensionChanges{}, nil
	}
	if err != nil {
		return nil, err
	}
	pending := map[string]pendingExtensionChanges{}
	return pending, json.Unmarshal(data, &pending)
}

func (c *extensionChanges) writePending(pending map[string]pendingExtensionChanges) error {
	data, err := json.MarshalIndent(pending, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(extension.DataDir(c.home), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(c.pendingPath(), append(data, '\n'), 0o600)
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
// turn's commit, whose ask the person answers.
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
	if err := d.holdExtensionChange(sessionID, change.Rev); err != nil {
		d.log.Printf("extensions: ask about %s's change: %s", d.resolveDisplay(sessionID), err)
	}
	return change.Rev, true
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
	d.publishEvent("extension.changed", "", map[string]string{"rev": rev})
}

func (d *Daemon) holdExtensionChange(sessionID, rev string) error {
	c := d.extensions.changes
	c.mu.Lock()
	defer c.mu.Unlock()
	pending, err := c.readPending()
	if err != nil {
		return err
	}
	held := pending[sessionID]
	open := false
	if held.AskID != "" {
		ask, found, err := d.Registry.Ask(held.AskID)
		if err != nil {
			return err
		}
		open = found && ask.State == registry.AskOpen
	}
	if !open {
		held = pendingExtensionChanges{}
	}
	held.Revs = append(held.Revs, rev)
	title, questions, err := d.extensionChangeAsk(sessionID, held.Revs)
	if err != nil {
		return err
	}
	if open {
		reworded, err := d.Registry.RewordAsk(held.AskID, title, questions)
		if err != nil {
			return err
		}
		open = reworded
	}
	if !open {
		ask, err := d.Registry.RaiseAsk(registry.Ask{SessionID: sessionID, Origin: registry.AskOriginExtensionChange, Title: title, Questions: questions, RoutedTo: registry.AskToPerson}, time.Now())
		if err != nil {
			return err
		}
		held.AskID = ask.ID
		d.announceAsk(ask)
		if err := d.Registry.MarkAskAnnounced(ask.ID, time.Now()); err != nil {
			d.log.Printf("extensions: mark %s told: %s", ask.ID, err)
		}
	}
	pending[sessionID] = held
	if err := c.writePending(pending); err != nil {
		return err
	}
	d.publishAsk(sessionID)
	return nil
}

func (d *Daemon) extensionChangeAsk(sessionID string, revs []string) (string, []registry.AskQuestion, error) {
	c := d.extensions.changes
	history, err := c.open(true)
	if err != nil || history == nil {
		return "", nil, errors.Join(err, errors.New("the extensions folder is gone"))
	}
	var summaries []string
	var last extension.Change
	for _, rev := range revs {
		changes, err := history.Log(1, rev)
		if err != nil {
			return "", nil, err
		}
		if len(changes) > 0 {
			last = changes[0]
			summaries = append(summaries, describeExtensionChange(last))
		}
	}
	if len(summaries) == 0 {
		return "", nil, errors.New("none of its changes is in the history")
	}
	who := d.resolveDisplay(sessionID)
	title := who + " changed extensions"
	keep, undo := "Keep", "Undo"
	question := "Keep this change? " + strings.Join(summaries, "; ")
	if len(revs) > 1 {
		title = fmt.Sprintf("%s changed extensions in %d turns", who, len(revs))
		keep, undo = "Keep all", "Undo last"
		question = fmt.Sprintf("%d turns pending. Last: %s", len(revs), summaries[len(summaries)-1])
	}
	if len(last.AlsoRunning) > 0 {
		question += ". Also mid-turn, so it may hold their edits: " + d.namesOf(last.AlsoRunning)
	}
	return title, []registry.AskQuestion{{
		Question:    clipText(question, registry.MaxAskQuestion),
		Options:     []registry.AskOption{{ID: extensionKeep, Label: keep}, {ID: extensionUndo, Label: undo, Does: clipText("Undo "+summaries[len(summaries)-1], registry.MaxAskDoes)}},
		Recommended: extensionKeep,
	}}, nil
}

func (d *Daemon) namesOf(sessionIDs []string) string {
	var names []string
	for _, id := range sessionIDs[:min(len(sessionIDs), 3)] {
		names = append(names, d.resolveDisplay(id))
	}
	if more := len(sessionIDs) - len(names); more > 0 {
		names = append(names, fmt.Sprintf("%d more", more))
	}
	return strings.Join(names, ", ")
}

func describeExtensionChange(change extension.Change) string {
	if len(change.Extensions) == 0 {
		return "no extension files"
	}
	var parts []string
	for _, files := range change.Extensions {
		count := len(files.Added) + len(files.Updated) + len(files.Deleted)
		switch {
		case files.Created:
			parts = append(parts, fmt.Sprintf("created %s (%s)", files.ID, fileCount(count)))
		case files.Removed:
			parts = append(parts, "removed "+files.ID)
		default:
			parts = append(parts, fmt.Sprintf("changed %s in %s", fileCount(count), files.ID))
		}
	}
	return strings.Join(parts, ", ")
}

func fileCount(n int) string { return fmt.Sprintf("%d %s", n, plural(n, "file", "files")) }

func clipText(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}

// An agent keeping its own change is not the person keeping it.
func (d *Daemon) answerExtensionChange(ctx context.Context, ask registry.Ask, response surface.Response, p principal) (actionResult, error) {
	if p.Kind == principalSession && !p.ForPerson {
		return actionResult{}, actionFailed(http.StatusForbidden, "forbidden", "only the person keeps or undoes a change to extensions")
	}
	if len(response.Choices) != 1 || (response.Choices[0] != extensionKeep && response.Choices[0] != extensionUndo) {
		return actionResult{}, invalidRequest("choose keep or undo")
	}
	c := d.extensions.changes
	c.mu.Lock()
	pending, err := c.readPending()
	held := pending[ask.SessionID]
	c.mu.Unlock()
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
	}
	if held.AskID != ask.ID || len(held.Revs) == 0 {
		return actionResult{}, staleRequest()
	}
	if response.Choices[0] == extensionUndo {
		if _, err := d.undoExtensionChange(ctx, held.Revs[len(held.Revs)-1]); err != nil {
			return actionResult{}, err
		}
		return actionOK(map[string]any{"ok": true, "result": map[string]any{"requestId": ask.ID, "outcome": "undone"}}), nil
	}
	if err := d.keepExtensionChanges(ask, p); err != nil {
		return actionResult{}, err
	}
	return actionOK(map[string]any{"ok": true, "result": map[string]any{"requestId": ask.ID, "outcome": "kept"}}), nil
}

// The ask closes and its turns are forgotten under the lock the rolling
// reword takes, so a turn recorded meanwhile either joins the ask before it
// closes or raises a new one.
func (d *Daemon) keepExtensionChanges(ask registry.Ask, p principal) error {
	c := d.extensions.changes
	c.mu.Lock()
	defer c.mu.Unlock()
	pending, err := c.readPending()
	if err != nil {
		return actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
	}
	if pending[ask.SessionID].AskID != ask.ID {
		return staleRequest()
	}
	claimed, err := d.Registry.ClaimAsk(ask.ID, registry.AskAnswer{Answers: map[string]string{ask.Questions[0].Question: extensionKeep}}, registry.AskToPerson, p.actor(), time.Now())
	if err != nil {
		return actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	if !claimed {
		return staleRequest()
	}
	delete(pending, ask.SessionID)
	if err := c.writePending(pending); err != nil {
		d.log.Printf("extensions: forget kept changes: %s", err)
	}
	if err := d.Registry.RecordAskApplied(ask.ID, "applied", "", time.Now()); err != nil {
		d.log.Printf("extensions: record the answer to %s: %s", ask.ID, err)
	}
	d.sendWithdraws()
	d.publishAsk(ask.SessionID)
	return nil
}

func (d *Daemon) undoExtensionChange(ctx context.Context, rev string) (extension.Undone, error) {
	history, err := d.extensionHistory()
	if err != nil {
		return extension.Undone{}, actionFailed(http.StatusServiceUnavailable, "unavailable", err.Error())
	}
	if history == nil {
		return extension.Undone{}, invalidRequest("there is no extensions folder")
	}
	undone, err := history.Undo(rev)
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
	}
	if len(undone.Removed) > 0 {
		if err := d.dropExtensionTables(undone.Removed); err != nil {
			d.log.Printf("extensions: drop tables of %s: %s", strings.Join(undone.Removed, ", "), err)
		}
	}
	d.applyExtensionChange(ctx, undone.Reverted.Rev)
	d.forgetPendingExtensionChange(undone.Reverted.Rev)
	return undone, nil
}

func (d *Daemon) forgetPendingExtensionChange(rev string) {
	c := d.extensions.changes
	c.mu.Lock()
	defer c.mu.Unlock()
	pending, err := c.readPending()
	if err != nil {
		d.log.Printf("extensions: read pending changes: %s", err)
		return
	}
	for sessionID, held := range pending {
		index := slices.Index(held.Revs, rev)
		if index < 0 {
			continue
		}
		held.Revs = slices.Delete(held.Revs, index, index+1)
		ask, found, err := d.Registry.Ask(held.AskID)
		if err != nil {
			d.log.Printf("extensions: read %s: %s", held.AskID, err)
			continue
		}
		open := found && ask.State == registry.AskOpen
		if len(held.Revs) == 0 {
			delete(pending, sessionID)
			if open {
				if _, err := d.Registry.ClaimAsk(ask.ID, registry.AskAnswer{Answers: map[string]string{ask.Questions[0].Question: extensionUndo}}, registry.AskToPerson, "undo", time.Now()); err != nil {
					d.log.Printf("extensions: close %s: %s", ask.ID, err)
				}
				d.sendWithdraws()
			}
		} else {
			pending[sessionID] = held
			if open {
				if title, questions, err := d.extensionChangeAsk(sessionID, held.Revs); err == nil {
					if reworded, err := d.Registry.RewordAsk(ask.ID, title, questions); err != nil || !reworded {
						d.log.Printf("extensions: reword %s: closed meanwhile or %v", ask.ID, err)
					}
				}
			}
		}
		d.publishAsk(sessionID)
	}
	if err := c.writePending(pending); err != nil {
		d.log.Printf("extensions: write pending changes: %s", err)
	}
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
