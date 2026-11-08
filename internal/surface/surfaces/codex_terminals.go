package surfaces

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// Codex reports no change to a thread's background terminals, so the status
// watch polls each loaded thread that may have some: every few seconds while
// a turn runs, since only a turn starts one, and less often after it while
// the last listing still had any, such as a dev server left running.
var (
	codexTerminalsActivePoll = 3 * time.Second
	codexTerminalsIdlePoll   = 10 * time.Second
	codexTerminalsTick       = time.Second
)

const (
	codexTerminalsMethod = "thread/backgroundTerminals/list"
	codexTerminalsPages  = 20
)

type codexTerminal struct {
	processID string
	itemID    string
	command   string
	seen      time.Time
}

// A transcript command starts its terminal a moment after the call is
// recorded, so a listing asked for before then cannot rule it out.
var codexTerminalSettle = 2 * time.Second

type codexListing struct {
	terminals []codexTerminal
	asked     time.Time
}

// A thread has a listing once the status watch has listed it on an app-server
// that serves the experimental method.
type codexTerminals struct {
	mu      sync.Mutex
	threads map[string]codexListing
	refused bool
}

func (t *codexTerminals) listing(threadID string) (codexListing, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	listing, ok := t.threads[threadID]
	if !ok || t.refused {
		return codexListing{}, false
	}
	listing.terminals = slices.Clone(listing.terminals)
	return listing, true
}

func (t *codexTerminals) publish(threadID string, listing codexListing) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.threads == nil {
		t.threads = map[string]codexListing{}
	}
	t.threads[threadID] = listing
}

// reset forgets every listing, and whether the app-server refused them, and
// reports whether any thread had terminals.
func (t *codexTerminals) reset(refused bool) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	had := false
	for _, listing := range t.threads {
		had = had || len(listing.terminals) > 0
	}
	t.threads = nil
	t.refused = refused
	return had
}

// A thread the watch has not listed, or whose app-server refuses the listing,
// falls back to its transcript. A listed thread runs the terminals its
// app-server lists, plus any its transcript started too recently for the last
// listing to have seen.
func (c *Codex) runtimeBackground(ctx context.Context, threadID, path string, info os.FileInfo) *surface.BackgroundTasks {
	listing, ok := c.terminals.listing(threadID)
	if !ok {
		return c.transcriptBackground(ctx, path, info)
	}
	var commands []surface.CodexBackgroundCommand
	if info != nil {
		commands = c.backgroundCommands(ctx, path, info)
	}
	started := map[string]time.Time{}
	for _, command := range commands {
		if at, err := time.Parse(time.RFC3339Nano, command.At); err == nil {
			started[command.CallID] = at.UTC()
		}
	}
	tasks := make([]surface.BackgroundTask, 0, len(listing.terminals))
	listed := map[string]bool{}
	for _, terminal := range listing.terminals {
		listed[terminal.itemID] = true
		since, ok := started[terminal.itemID]
		if !ok {
			since = terminal.seen
		}
		tasks = append(tasks, surface.BackgroundTask{ID: terminal.processID, Kind: surface.BackgroundShell, Description: terminal.command, Since: since})
	}
	for _, command := range commands {
		at, ok := started[command.CallID]
		if !ok || listed[command.CallID] || !listing.asked.Before(at.Add(codexTerminalSettle)) {
			continue
		}
		tasks = append(tasks, surface.BackgroundTask{ID: command.CallID, Kind: surface.BackgroundShell, Description: codexCommandLine(command.Command), Since: at})
	}
	return backgroundOf(tasks, surface.BackgroundSourceRuntime)
}

// Only the status watch's loop touches a terminalWatch; each thread has at
// most one listing in flight.
type terminalWatch struct {
	client      *managedCodexClient
	terminals   *codexTerminals
	threads     map[string]*terminalThread
	inFlight    map[string]bool
	results     chan terminalResult
	unsupported bool
	// Until a listing succeeds, one goes at a time, so an app-server without
	// the method is asked once.
	probed bool
}

type terminalThread struct {
	known  bool
	active bool
	// idleSince is when the thread was last seen idle. A command its turn
	// started may be missing from listings asked for less than
	// codexTerminalSettle after, so polling stops only after a later one.
	idleSince time.Time
	// settling is a listing due to rule out what the transcript may still
	// show; the row can change when it lands even if the listing does not.
	settling bool
	listed   []codexTerminal
	due      time.Time
}

type terminalResult struct {
	threadID  string
	thread    *terminalThread
	status    string
	terminals []codexTerminal
	asked     time.Time
	err       error
}

func newTerminalWatch(client *managedCodexClient, terminals *codexTerminals) *terminalWatch {
	return &terminalWatch{client: client, terminals: terminals, threads: map[string]*terminalThread{}, inFlight: map[string]bool{}, results: make(chan terminalResult)}
}

func (w *terminalWatch) seed(ids []string, now time.Time) {
	for _, id := range ids {
		if _, ok := w.threads[id]; !ok {
			w.threads[id] = &terminalThread{due: now}
		}
	}
}

func (w *terminalWatch) status(threadID, status string, now time.Time) bool {
	if threadID == "" || status == "" {
		return false
	}
	if status == "notLoaded" {
		return w.drop(threadID, now)
	}
	thread := w.threads[threadID]
	if thread == nil {
		thread = &terminalThread{}
		w.threads[threadID] = thread
	}
	thread.known = true
	thread.active = status == "active"
	if !thread.active {
		thread.idleSince = now
	}
	thread.due = now
	return false
}

// A thread the app-server says is not loaded runs nothing, so it keeps an
// empty listing: what its transcript shows from before stays ruled out.
func (w *terminalWatch) drop(threadID string, now time.Time) bool {
	delete(w.threads, threadID)
	w.terminals.publish(threadID, codexListing{asked: now})
	return true
}

func (w *terminalWatch) start(ctx context.Context, now time.Time) {
	if w.unsupported {
		return
	}
	for id, thread := range w.threads {
		if !w.probed && len(w.inFlight) > 0 {
			return
		}
		if w.inFlight[id] || thread.due.IsZero() || thread.due.After(now) {
			continue
		}
		w.inFlight[id] = true
		readStatus := !thread.known
		go func() {
			result := w.list(ctx, id, readStatus)
			result.thread = thread
			select {
			case w.results <- result:
			case <-ctx.Done():
			case <-w.client.Done():
			}
		}()
	}
}

func (w *terminalWatch) list(ctx context.Context, threadID string, readStatus bool) terminalResult {
	result := terminalResult{threadID: threadID}
	if readStatus {
		response, err := w.client.Request(ctx, "thread/read", map[string]any{"threadId": threadID, "includeTurns": false}, 5*time.Second)
		if err != nil {
			result.err = err
			return result
		}
		page, _ := response["result"].(map[string]any)
		thread, _ := page["thread"].(map[string]any)
		result.status = codexStatusType(thread["status"])
	}
	params := map[string]any{"threadId": threadID}
	result.asked = time.Now()
	for range codexTerminalsPages {
		response, err := w.client.Request(ctx, codexTerminalsMethod, params, 5*time.Second)
		if err != nil {
			result.err = err
			return result
		}
		page, _ := response["result"].(map[string]any)
		entries, _ := page["data"].([]any)
		for _, entry := range entries {
			terminal, _ := entry.(map[string]any)
			if id := str(terminal, "processId"); id != "" {
				result.terminals = append(result.terminals, codexTerminal{processID: id, itemID: str(terminal, "itemId"), command: str(terminal, "command")})
			}
		}
		cursor := str(page, "nextCursor")
		if cursor == "" {
			return result
		}
		params["cursor"] = cursor
	}
	return result
}

func (w *terminalWatch) finish(result terminalResult, now time.Time) bool {
	delete(w.inFlight, result.threadID)
	if codexTerminalsUnavailable(result.err) {
		w.unsupported = true
		w.threads = map[string]*terminalThread{}
		return w.terminals.reset(true)
	}
	if result.err == nil {
		w.probed = true
	}
	// A listing asked for before the thread unloaded says nothing about it
	// once it has loaded again.
	thread := w.threads[result.threadID]
	if thread == nil || thread != result.thread {
		return false
	}
	switch {
	case codexTerminalsThreadGone(result.err):
		return w.drop(result.threadID, now)
	case result.err != nil:
		thread.due = now.Add(codexTerminalsActivePoll)
		return false
	}
	if result.status == "notLoaded" {
		return w.drop(result.threadID, now)
	}
	if result.status != "" {
		thread.known = true
		thread.active = result.status == "active"
		if !thread.active {
			thread.idleSince = result.asked
		}
	}
	seen := map[string]time.Time{}
	for _, terminal := range thread.listed {
		seen[terminal.processID] = terminal.seen
	}
	for i := range result.terminals {
		if at, ok := seen[result.terminals[i].processID]; ok {
			result.terminals[i].seen = at
		} else {
			result.terminals[i].seen = now.UTC()
		}
	}
	changed := !slices.EqualFunc(thread.listed, result.terminals, func(a, b codexTerminal) bool {
		return a.processID == b.processID && a.command == b.command
	})
	thread.listed = result.terminals
	if thread.settling && !result.asked.Before(thread.idleSince.Add(codexTerminalSettle)) {
		thread.settling = false
		changed = true
	}
	switch {
	case thread.active:
		thread.due = now.Add(codexTerminalsActivePoll)
	case len(thread.listed) > 0:
		thread.due = now.Add(codexTerminalsIdlePoll)
	case result.asked.Before(thread.idleSince.Add(codexTerminalSettle)):
		thread.due = thread.idleSince.Add(codexTerminalSettle)
		thread.settling = true
	default:
		thread.due = time.Time{}
	}
	w.terminals.publish(result.threadID, codexListing{terminals: thread.listed, asked: result.asked})
	return changed
}

// Codex answers an unknown method, and an experimental one on a connection
// that did not opt in, with -32600 and only the message to tell them apart.
func codexTerminalsUnavailable(err error) bool {
	var rpc *codexRPCError
	if !errors.As(err, &rpc) {
		return false
	}
	switch rpc.Code {
	case -32601:
		return true
	case -32600:
		return strings.Contains(rpc.Message, "requires experimentalApi capability") || strings.HasPrefix(rpc.Message, "Invalid request: unknown variant")
	}
	return false
}

func codexTerminalsThreadGone(err error) bool {
	var rpc *codexRPCError
	return errors.As(err, &rpc) && rpc.Code == -32600 && strings.HasPrefix(rpc.Message, "thread not found")
}
