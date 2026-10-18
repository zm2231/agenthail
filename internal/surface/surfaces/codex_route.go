package surfaces

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	codexTransportDesktop  = "desktop"
	codexTransportManaged  = "managed"
	codexTransportReadOnly = "readOnly"

	codexDesktopOriginator = "Codex Desktop"
)

// codexRuntimes is one view of the Codex app-servers Agenthail can reach:
// Desktop's private app-server through the renderer bridge and Codex's
// app-server daemon through its control socket. Opening it never starts the
// daemon. Each thread is loaded by at most one of them, because Codex holds a
// per-thread writer lock in the process that has the thread loaded.
type codexRuntimes struct {
	desktop       codexClient
	managed       codexClient
	desktopLoaded map[string]bool
	managedLoaded map[string]bool
	failures      []string
	loadFailures  []string
}

func (c *Codex) openRuntimes(ctx context.Context) *codexRuntimes {
	runtimes := &codexRuntimes{desktopLoaded: map[string]bool{}, managedLoaded: map[string]bool{}}
	if client, err := c.openDesktop(ctx); err == nil {
		runtimes.desktop = client
		runtimes.desktopLoaded = runtimes.loadedSet(ctx, c, client, "Codex Desktop")
	} else {
		runtimes.failures = append(runtimes.failures, err.Error())
	}
	if client, err := c.dialManaged(ctx); err == nil {
		runtimes.managed = client
		runtimes.managedLoaded = runtimes.loadedSet(ctx, c, client, "Codex's app-server daemon")
	} else {
		runtimes.failures = append(runtimes.failures, "Codex's app-server daemon is not running: "+err.Error())
	}
	return runtimes
}

func (r *codexRuntimes) loadedSet(ctx context.Context, c *Codex, client codexClient, name string) map[string]bool {
	loaded := map[string]bool{}
	ids, err := c.loadedThreadIDs(ctx, client, 0)
	if err != nil {
		r.loadFailures = append(r.loadFailures, name+": "+err.Error())
		return loaded
	}
	for _, id := range ids {
		loaded[id] = true
	}
	return loaded
}

func (r *codexRuntimes) Close() {
	if r.desktop != nil {
		_ = r.desktop.Close()
	}
	if r.managed != nil {
		_ = r.managed.Close()
	}
}

// ownershipUnknown reports a reachable runtime whose loaded threads could not
// be listed; until it can, no thread's writer route can be decided.
func (r *codexRuntimes) ownershipUnknown() error {
	if len(r.loadFailures) == 0 {
		return nil
	}
	return fmt.Errorf("cannot tell which Codex runtime owns the session: %s", strings.Join(r.loadFailures, "; "))
}

func (r *codexRuntimes) unavailable() error {
	return surface.DeliveryUnavailable(fmt.Errorf("Codex is unavailable: %s", strings.Join(r.failures, "; ")))
}

// route decides the one writer route for a thread. The runtime that has the
// thread loaded is its owner. An unloaded thread goes back to Desktop when
// Desktop created it or its creator is unknown, and to the daemon only when
// Codex positively records a creator other than Desktop.
func (r *codexRuntimes) route(threadID, originator string) string {
	switch {
	case r.desktopLoaded[threadID]:
		return codexTransportDesktop
	case r.managedLoaded[threadID]:
		return codexTransportManaged
	case originator != "" && originator != codexDesktopOriginator:
		return codexTransportManaged
	case r.desktop != nil:
		return codexTransportDesktop
	default:
		return codexTransportReadOnly
	}
}

// reader returns the runtime that can read a thread without loading it: its
// owner when loaded, otherwise any reachable runtime.
func (r *codexRuntimes) reader(threadID string) codexClient {
	switch {
	case r.managedLoaded[threadID] && r.managed != nil:
		return r.managed
	case r.desktop != nil:
		return r.desktop
	default:
		return r.managed
	}
}

// readers lists every reachable runtime, Desktop first.
func (r *codexRuntimes) readers() []codexClient {
	var clients []codexClient
	for _, client := range []codexClient{r.desktop, r.managed} {
		if client != nil {
			clients = append(clients, client)
		}
	}
	return clients
}

// session converts a Codex thread object and routes it.
func (r *codexRuntimes) session(thread map[string]any) surface.Session {
	session := codexSession(thread)
	session.Transport = r.route(session.ID, codexThreadOriginator(thread))
	return session
}

// release closes every client except keep.
func (r *codexRuntimes) release(keep codexClient) {
	if r.desktop != nil && r.desktop != keep {
		_ = r.desktop.Close()
	}
	if r.managed != nil && r.managed != keep {
		_ = r.managed.Close()
	}
}

// codexThreadOriginator returns the client Codex recorded as the thread's
// creator, from the thread object or, when absent, from its rollout.
func codexThreadOriginator(thread map[string]any) string {
	if originator := str(thread, "originator"); originator != "" {
		return originator
	}
	return codexRolloutOriginator(str(thread, "path"))
}

func codexRolloutOriginator(path string) string {
	if path == "" {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 64<<10)
	line, err := reader.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return ""
	}
	var record struct {
		Type    string `json:"type"`
		Payload struct {
			Originator string `json:"originator"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &record) != nil || record.Type != "session_meta" {
		return ""
	}
	return record.Payload.Originator
}

// resolveRoute sets sess.Transport from the live runtimes and the thread's
// recorded creator.
func (c *Codex) resolveRoute(ctx context.Context, runtimes *codexRuntimes, sess *surface.Session) {
	originator := ""
	if reader := runtimes.reader(sess.ID); reader != nil {
		if thread, err := c.readThreadObject(ctx, reader, sess.ID); err == nil {
			originator = codexThreadOriginator(thread)
		}
	}
	if originator == "" {
		originator = codexRolloutOriginator(codexTranscriptPath(sess))
	}
	sess.Transport = runtimes.route(sess.ID, originator)
}

// openWriter makes the session's route the thread's live writer and returns a
// client on it. Its errors are classified for delivery: a writer held by
// another runtime is a terminal ownership conflict, anything else unavailable.
func (c *Codex) openWriter(ctx context.Context, sess *surface.Session) (codexClient, error) {
	client, err := c.openRoute(ctx, sess)
	if err != nil {
		return nil, surface.DeliveryUnavailable(err)
	}
	if err := c.requireDirectInput(ctx, client, sess); err != nil {
		_ = client.Close()
		if surface.IsDeliveryTerminal(err) {
			return nil, err
		}
		return nil, surface.DeliveryUnavailable(err)
	}
	return client, nil
}

// openRoute returns a client on the session's writer route, starting Codex's
// daemon only when the route is managed and the daemon is not running.
func (c *Codex) openRoute(ctx context.Context, sess *surface.Session) (codexClient, error) {
	runtimes := c.openRuntimes(ctx)
	if err := runtimes.ownershipUnknown(); err != nil {
		runtimes.Close()
		return nil, err
	}
	c.resolveRoute(ctx, runtimes, sess)
	switch sess.Transport {
	case codexTransportDesktop:
		runtimes.release(runtimes.desktop)
		return runtimes.desktop, nil
	case codexTransportManaged:
		if runtimes.managed != nil {
			runtimes.release(runtimes.managed)
			return runtimes.managed, nil
		}
		runtimes.Close()
		return c.openManaged(ctx)
	default:
		runtimes.Close()
		return nil, errors.New(surface.ReadOnlySessionReason(sess))
	}
}

// openReader returns a client that can read the session without loading it.
func (c *Codex) openReader(ctx context.Context, sess *surface.Session) (codexClient, error) {
	runtimes := c.openRuntimes(ctx)
	reader := runtimes.reader(sess.ID)
	if reader == nil {
		return nil, runtimes.unavailable()
	}
	runtimes.release(reader)
	return reader, nil
}
