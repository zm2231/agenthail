package claudesurface

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/procstart"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	claudeHostListTTL       = time.Second
	claudeHostListTimeout   = 2 * time.Second
	claudeHostSilentOpenTTL = 5 * time.Second
	claudeHostTakenTTL      = 10 * time.Minute
)

type claudeHost struct {
	client claudehost.Client
	state  string
	// fetch lets one listing ask the hosts at a time; mu guards the cache and
	// is never held across a call to a host.
	fetch      sync.Mutex
	mu         sync.Mutex
	ttl        time.Duration
	listed     time.Time
	forgotten  time.Time
	sessions   map[string]int
	open       map[string]claudehost.Session
	openListed map[string]time.Time
	partial    bool
	// taken are the requests a host took an answer to, by request id, so a
	// snapshot read before the answer cannot bring them back.
	taken map[string]time.Time
}

// claudeHosted reports whether the host runs the session's conversation. Its
// process also serves Claude's native messaging socket, which agent messages
// take; only a person's turn goes through the host.
func claudeHosted(sess *surface.Session) bool {
	return sess != nil && sess.RuntimeKind == surface.RuntimeClaudeHost
}

func newClaudeHost(home string) *claudeHost {
	return &claudeHost{client: claudehost.NewClient(home), ttl: claudeHostListTTL, state: filepath.Join(claudehost.StateDir(home), "state.json")}
}

// runs reports whether a host started this Claude process for this
// conversation. That includes a process an earlier host started, which holds
// its session until it exits, so a person's turn to it waits rather than
// taking the native socket. A host knows a process's pid from the moment it
// spawns it, before Claude writes the session record read at readAt, so a
// listing older than that record is read again before the process is called
// unhosted.
func (h *claudeHost) runs(ctx context.Context, pid int, conversation string, readAt time.Time) bool {
	if pid <= 0 || conversation == "" {
		return false
	}
	if h.listing(ctx)[conversation] == pid {
		return true
	}
	h.mu.Lock()
	if h.listed.Before(readAt) {
		h.listed = time.Time{}
	}
	h.mu.Unlock()
	return h.listing(ctx)[conversation] == pid
}

// forget drops the cached listing, so the next read asks the host.
func (h *claudeHost) forget() {
	h.mu.Lock()
	h.listed = time.Time{}
	h.forgotten = time.Now()
	h.mu.Unlock()
}

// HostChanged drops the cached host listing after the host reports a change
// to what a session waits on or runs, so the next read sees it.
func (c *Claude) HostChanged() {
	c.host.forget()
}

// listing is the host's sessions and orphans by conversation, with their
// process ids. A host that does not answer is read from its state file, which
// names the processes it started. While any host does not answer, a process
// last listed that still runs keeps its listing, since the silent host may be
// the one running it.
func (h *claudeHost) listing(ctx context.Context) map[string]int {
	if h == nil {
		return nil
	}
	h.fetch.Lock()
	defer h.fetch.Unlock()
	h.mu.Lock()
	fresh := time.Since(h.listed) <= h.ttl
	h.mu.Unlock()
	if !fresh {
		h.refresh(ctx)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return maps.Clone(h.sessions)
}

func (h *claudeHost) refresh(ctx context.Context) {
	listCtx, cancel := context.WithTimeout(ctx, claudeHostListTimeout)
	started := time.Now()
	listing, err := h.client.List(listCtx)
	took := time.Since(started)
	cancel()
	sessions := map[string]int{}
	open := map[string]claudehost.Session{}
	openListed := map[string]time.Time{}
	partial := err != nil || len(listing.Unanswered) > 0
	if err == nil {
		for _, session := range listing.Sessions {
			sessions[session.SessionID] = session.PID
			open[session.SessionID] = session
			openListed[session.SessionID] = started
		}
		for _, orphan := range listing.Orphans {
			sessions[orphan.SessionID] = orphan.PID
		}
	} else {
		h.readState(sessions)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	previous, previousOpen, previousListed, wasPartial := h.sessions, h.open, h.openListed, h.partial
	h.sessions, h.open, h.openListed, h.partial = sessions, open, openListed, partial
	for id, at := range previousListed {
		if session, ok := previousOpen[id]; ok && at.After(started) {
			h.sessions[id] = session.PID
			h.open[id] = session
			h.openListed[id] = at
		}
	}
	if partial {
		h.keepUnseen(previous, previousOpen, previousListed)
	}
	for id, session := range h.open {
		h.open[id] = h.withoutTaken(session)
	}
	if partial != wasPartial {
		logClaudeHostListing(partial, took, err, listing.Unanswered)
	}
	h.listed = time.Now()
	if h.forgotten.After(started) {
		h.listed = time.Time{}
	}
}

func (h *claudeHost) keepUnseen(previous map[string]int, previousOpen map[string]claudehost.Session, previousListed map[string]time.Time) {
	for id, pid := range previous {
		if _, seen := h.sessions[id]; seen || !claudeProcessAlive(pid) {
			continue
		}
		h.sessions[id] = pid
		if session, ok := previousOpen[id]; ok && session.PID == pid {
			h.open[id] = session
			h.openListed[id] = previousListed[id]
		}
	}
}

func logClaudeHostListing(partial bool, took time.Duration, err error, unanswered []string) {
	took = took.Round(time.Millisecond)
	switch {
	case !partial:
		log.Printf("Claude host: the session listing answers again (%s)", took)
	case errors.Is(err, context.DeadlineExceeded):
		log.Printf("Claude host: the session listing timed out after %s; sessions last listed keep their requests for %s", took, claudeHostSilentOpenTTL)
	case err != nil:
		log.Printf("Claude host: the session listing failed after %s: %s; sessions last listed keep their requests for %s", took, err, claudeHostSilentOpenTTL)
	default:
		log.Printf("Claude host: %s did not answer the session listing (%s); sessions last listed keep their requests for %s", strings.Join(unanswered, ", "), took, claudeHostSilentOpenTTL)
	}
}

// store keeps a session as its host listed it alone at readAt, unless a
// later read has replaced it.
func (h *claudeHost) store(listed claudehost.Session, readAt time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions == nil || h.openListed[listed.SessionID].After(readAt) {
		return
	}
	h.sessions[listed.SessionID] = listed.PID
	h.open[listed.SessionID] = h.withoutTaken(listed)
	h.openListed[listed.SessionID] = readAt
}

// answered drops a request its host took an answer to from the cached
// listing, now and from any snapshot stored after.
func (h *claudeHost) answered(conversation, requestID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	if h.taken == nil {
		h.taken = map[string]time.Time{}
	}
	for id, at := range h.taken {
		if now.Sub(at) > claudeHostTakenTTL {
			delete(h.taken, id)
		}
	}
	h.taken[requestID] = now
	if session, ok := h.open[conversation]; ok {
		h.open[conversation] = h.withoutTaken(session)
	}
}

func (h *claudeHost) withoutTaken(session claudehost.Session) claudehost.Session {
	if len(h.taken) == 0 || !slices.ContainsFunc(session.Requests, func(request claudehost.Request) bool { _, ok := h.taken[request.RequestID]; return ok }) {
		return session
	}
	session.Requests = slices.DeleteFunc(slices.Clone(session.Requests), func(request claudehost.Request) bool {
		_, ok := h.taken[request.RequestID]
		return ok
	})
	return session
}

// unanswered reports whether a host did not answer the last listing.
func (h *claudeHost) unanswered() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.partial
}

func (h *claudeHost) requestsKnown(conversation string, pid int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.partial {
		return true
	}
	if session, ok := h.open[conversation]; ok && session.PID == pid {
		return time.Since(h.openListed[conversation]) <= claudeHostSilentOpenTTL
	}
	return h.sessions[conversation] != pid
}

// session is the host's listing of the conversation it runs as process pid.
// An orphan, or any process while the host does not answer, has none.
func (h *claudeHost) session(ctx context.Context, conversation string, pid int) (claudehost.Session, bool) {
	if h == nil || pid <= 0 {
		return claudehost.Session{}, false
	}
	h.listing(ctx)
	h.mu.Lock()
	defer h.mu.Unlock()
	session, ok := h.open[conversation]
	return session, ok && session.PID == pid
}

// readState reads the processes the host recorded starting. One counts only
// while its pid runs with the recorded start time; a different start time means
// another process reused the pid.
func (h *claudeHost) readState(sessions map[string]int) {
	data, err := os.ReadFile(h.state)
	if err != nil {
		return
	}
	var state struct {
		Sessions map[string]struct {
			PID     int    `json:"pid"`
			Started string `json:"started"`
		} `json:"sessions"`
	}
	if json.Unmarshal(data, &state) != nil {
		return
	}
	for id, entry := range state.Sessions {
		if entry.Started == "" || !claudeProcessAlive(entry.PID) {
			continue
		}
		if started, err := procstart.Lstart(context.Background(), entry.PID); err == nil && started == entry.Started {
			sessions[id] = entry.PID
		}
	}
}

// The intent uuid becomes the transcript record's uuid: the turn is followed
// by it, and a resend of the same message runs once. Only a person's turn
// carries the human origin; any other turn Claude records as the host's.
// sendHostTurn with whenIdle leaves a busy session's message unaccepted, so
// the sender's busy delivery decides whether it waits or steers.
func (c *Claude) sendHostTurn(ctx context.Context, sess *surface.Session, message, priority string, images surface.Uploads, person, whenIdle bool) (*surface.SendResult, error) {
	id := surface.MessageUUID(ctx)
	if id == "" {
		id = uuid.NewString()
	}
	var attached []claudehost.Image
	for _, image := range images {
		attached = append(attached, claudehost.Image{Path: image.Path, MediaType: image.MediaType})
	}
	request := claudehost.SendRequest{SessionID: sess.ID, UUID: id, Text: message, Images: attached, Priority: priority, IfIdle: whenIdle}
	if person {
		request.Origin = &claudehost.Origin{Kind: "human"}
	}
	if _, err := c.host.client.Send(ctx, request); err != nil {
		if whenIdle && claudehost.IsCode(err, "busy") {
			return &surface.SendResult{UUID: id, Accepted: false}, nil
		}
		return nil, claudeHostDeliveryError(err)
	}
	return &surface.SendResult{UUID: id, Accepted: true}, nil
}

func (c *Claude) SendAgenthailTurn(ctx context.Context, sess *surface.Session, message string) (*surface.SendResult, error) {
	if sess == nil || !claudeHosted(sess) {
		return nil, surface.DeliveryUnavailable(errors.New("only a session the Claude host runs takes Agenthail's turn"))
	}
	if err := c.EnsureWritable(ctx, sess); err != nil {
		return nil, err
	}
	if _, err := c.reach(ctx, sess); err != nil {
		return nil, err
	}
	return c.sendHostTurn(ctx, sess, message, "", nil, false, true)
}

func (c *Claude) interruptHost(ctx context.Context, sess *surface.Session) error {
	if err := c.host.client.Interrupt(ctx, sess.ID); err != nil {
		return claudeHostDeliveryError(err)
	}
	return nil
}

func claudeHostDeliveryError(err error) error {
	var unknown *claudehost.OutcomeUnknownError
	switch {
	case errors.As(err, &unknown), claudehost.IsCode(err, "unknown"):
		return surface.DeliveryOutcomeUnknown(err)
	case errors.Is(err, claudehost.ErrNotRunning), errors.Is(err, claudehost.ErrOtherProtocol), claudehost.IsCode(err, "draining"), claudehost.IsCode(err, "held"):
		return surface.DeliveryUnavailable(fmt.Errorf("%w: %s", surface.ErrDeliveryHeld, err))
	case claudehost.IsCode(err, "invalid"):
		return surface.DeliveryTerminal(err, surface.DeliveryInvalidRequest)
	default:
		return surface.DeliveryUnavailable(err)
	}
}

func ClaudeHostLaunch(home, version string) (claudehost.Launch, error) {
	script := strings.TrimSpace(os.Getenv("AGENTHAIL_CLAUDE_HOST"))
	if script == "" {
		root, err := surface.InstallRoot()
		if err != nil {
			return claudehost.Launch{}, err
		}
		script = filepath.Join(root, "claude-host", "host.mjs")
	}
	if _, err := os.Stat(script); err != nil {
		return claudehost.Launch{}, fmt.Errorf("the Claude host script is missing (set AGENTHAIL_CLAUDE_HOST): %w", err)
	}
	node, err := surface.ShippedNode()
	if err != nil {
		return claudehost.Launch{}, err
	}
	claude, err := surface.ClaudeBinary(home)
	if err != nil {
		return claudehost.Launch{}, err
	}
	launch := claudehost.Launch{Node: node, Script: script, Claude: claude, Version: version, Env: claudeCommandEnv(home)}
	launch.Agenthail = surface.AgenthailExecutable()
	return launch, nil
}

// HostedProcessSession names the conversation a Claude host runs as the
// process pid, including one an earlier host started.
func (c *Claude) HostedProcessSession(ctx context.Context, pid int) string {
	for id, hosted := range c.host.listing(ctx) {
		if hosted == pid {
			return id
		}
	}
	return ""
}
