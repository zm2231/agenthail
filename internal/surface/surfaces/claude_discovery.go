package surfaces

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// List returns one session per conversation. A conversation open in several
// processes, as when it is resumed or branched into a second terminal, lists
// every process as a holder and attaches none of them.
func (c *Claude) List(ctx context.Context) ([]surface.Session, error) {
	processes, err := c.conversations(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]surface.Session, 0, len(processes))
	for _, process := range processes {
		c.enrich(ctx, &process)
		out = append(out, process.session)
	}
	return out, nil
}

// conversations returns each conversation with the live processes that hold
// it, before its status is read.
func (c *Claude) conversations(ctx context.Context) ([]claudeProcess, error) {
	processes, err := c.processes(ctx)
	if err != nil {
		return nil, err
	}
	var order []string
	held := map[string][]claudeProcess{}
	for _, process := range processes {
		if _, seen := held[process.session.ID]; !seen {
			order = append(order, process.session.ID)
		}
		held[process.session.ID] = append(held[process.session.ID], process)
	}
	out := make([]claudeProcess, 0, len(order))
	for _, id := range order {
		if holders := held[id]; len(holders) > 1 {
			out = append(out, shareConversation(holders))
		} else {
			out = append(out, holders[0])
		}
	}
	return out, nil
}

type claudeProcess struct {
	session   surface.Session
	record    map[string]any
	startedAt float64
	// holders are the processes of a conversation several of them hold.
	holders []claudeProcess
}

// enrich reads a process's status, what it waits on, its background tasks and
// its subagents. A lookup by id, name or process needs none of them, so only
// the sessions a caller returns are read.
func (c *Claude) enrich(ctx context.Context, process *claudeProcess) {
	c.applyRecordStatus(ctx, &process.session, process.record)
	applyHoldersStatus(&process.session)
	if process.session.HasLocal {
		if rollup, err := c.subagents.rollup(ctx, &process.session); err == nil {
			process.session.Subagents = rollup
		}
	}
}

// processes returns a session for every live Claude process, including
// processes that share a session id, without reading their status.
func (c *Claude) processes(ctx context.Context) ([]claudeProcess, error) {
	roots, records, err := c.records()
	if err != nil {
		return nil, err
	}
	var out []claudeProcess
	transcripts := c.transcripts.pass(roots)
	c.prompts.retain()
	for _, record := range records {
		if process, ok := c.process(ctx, record, transcripts); ok {
			out = append(out, process)
		}
	}
	return out, nil
}

// CatalogWatchDirs are the folders where each Claude process writes its
// session record when it starts, one per config root.
func (c *Claude) CatalogWatchDirs() []string {
	roots, err := surface.ClaudeConfigRoots(c.home)
	if err != nil {
		return nil
	}
	dirs := make([]string, 0, len(roots))
	for _, root := range roots {
		dirs = append(dirs, filepath.Join(root, "sessions"))
	}
	return dirs
}

// process builds the session for a record Agenthail can deliver to: one with
// a Remote Control bridge or a validated messaging socket. The session is the
// record's conversation; the bridge and socket belong to the process.
func (c *Claude) process(ctx context.Context, record claudeSessionRecord, transcripts func(string) []string) (claudeProcess, bool) {
	m := record.fields
	conversation := str(m, "sessionId")
	bridge := ""
	if !c.localOnly {
		bridge = str(m, "bridgeSessionId")
	}
	socket := c.peerSocket(ctx, m)
	pid, _ := m["pid"].(float64)
	hosted := c.host.runs(ctx, int(pid), conversation)
	if conversation == "" || (bridge == "" && socket == "" && !hosted) {
		return claudeProcess{}, false
	}
	sess := surface.Session{
		ID:          conversation,
		Bridge:      bridge,
		Surface:     surface.KindClaude,
		RuntimeKind: surface.RuntimeClaudeCLI,
		Cwd:         str(m, "cwd"),
		Status:      surface.SessionStatus(str(m, "status")),
	}
	sess.PID = int(pid)
	if socket != "" {
		sess.Transport = "uds"
	}
	switch {
	case hosted:
		sess.RuntimeKind = surface.RuntimeClaudeHost
	case str(m, "kind") == "bg":
		sess.RuntimeKind = surface.RuntimeClaudeBackground
	}
	if n, ok := m["name"].(string); ok {
		sess.Name = n
	}
	sess.Transcript = claudeRootTranscript(transcripts(conversation), record.root)
	sess.HasLocal = sess.Transcript != "" && fileExists(sess.Transcript)
	if sess.HasLocal {
		sess.ForkedFrom = claudeForkedFrom(sess.Transcript)
	}
	if sess.Name == "" {
		sess.Name = c.firstUserMessage(sess.Transcript)
	}
	startedAt, ok := m["startedAt"].(float64)
	if ok {
		sess.StartedAt = time.UnixMilli(int64(startedAt))
	} else {
		startedAt = math.MaxFloat64
	}
	return claudeProcess{session: sess, record: m, startedAt: startedAt}, true
}

func (c *Claude) Resolve(ctx context.Context, target string) (*surface.Session, error) {
	processes, err := c.conversations(ctx)
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(target)
	var matches []claudeProcess
	var exactMatches []claudeProcess
	for _, process := range processes {
		s := process.session
		if pid, err := strconv.Atoi(target); err == nil && pid > 0 {
			if holder, ok := process.pinned(pid); ok {
				exactMatches = append(exactMatches, holder)
				continue
			}
		}
		if s.ID == target ||
			strings.TrimSuffix(filepath.Base(s.Transcript), ".jsonl") == target {
			exactMatches = append(exactMatches, process)
			continue
		}
		if strings.HasPrefix(s.ID, target) ||
			strings.Contains(strings.ToLower(s.Cwd), lower) ||
			strings.Contains(strings.ToLower(s.Name), lower) {
			matches = append(matches, process)
		}
	}
	if len(exactMatches) > 0 {
		matches = exactMatches
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("%w '%s'", surface.ErrSessionNotFound, target)
	}
	if len(matches) > 1 {
		var lines []string
		for _, m := range matches {
			lines = append(lines, fmt.Sprintf("  pid=%d %s cwd=%s", m.session.PID, m.session.ID, m.session.Cwd))
		}
		return nil, fmt.Errorf("ambiguous target '%s':\n%s", target, strings.Join(lines, "\n"))
	}
	c.enrich(ctx, &matches[0])
	return &matches[0].session, nil
}
