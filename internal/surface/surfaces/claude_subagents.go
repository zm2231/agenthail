package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// A subagent whose last turn never recorded a terminal stop (a killed
// process) stops counting as working once its transcript is this old.
const claudeSubagentStaleAfter = 15 * time.Minute

const claudeSubagentTailBytes = 256 * 1024

type claudeSubagentObserver struct {
	mu   sync.Mutex
	dirs map[string]*claudeSubagentDir
	now  func() time.Time
}

type claudeSubagentDir struct {
	mu       sync.Mutex
	modTime  time.Time
	listedAt time.Time
	listed   bool
	agents   map[string]*claudeSubagentFile
}

type claudeSubagentFile struct {
	path      string
	agentID   string
	meta      claudeSubagentMeta
	hasMeta   bool
	validated bool
	read      bool
	stamp     surface.FileStamp
	cursor    transcriptCursor
	done      bool
}

type claudeSubagentMeta struct {
	AgentType   string `json:"agentType"`
	Description string `json:"description"`
	ToolUseID   string `json:"toolUseId"`
	SpawnDepth  int    `json:"spawnDepth"`
}

type claudeSubagentRecord struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	AgentID   string `json:"agentId"`
	Message   struct {
		StopReason string `json:"stop_reason"`
	} `json:"message"`
}

func newClaudeSubagentObserver() *claudeSubagentObserver {
	return &claudeSubagentObserver{dirs: map[string]*claudeSubagentDir{}, now: time.Now}
}

// Claude Code keeps a session's subagents beside its transcript:
// <project>/<transcript-id>.jsonl and <project>/<transcript-id>/subagents/.
// The transcript ID differs from a bridge session ID, so the transcript path
// is the only reliable key.
func claudeSubagentDirectory(transcript string) (dir, transcriptID string, ok bool) {
	if !strings.HasSuffix(transcript, ".jsonl") {
		return "", "", false
	}
	base := strings.TrimSuffix(transcript, ".jsonl")
	transcriptID = filepath.Base(base)
	if transcriptID == "" || transcriptID == "." || transcriptID == string(filepath.Separator) {
		return "", "", false
	}
	return filepath.Join(base, "subagents"), transcriptID, true
}

func (o *claudeSubagentObserver) observeSession(ctx context.Context, session *surface.Session) ([]surface.ClaudeSubagentLink, error) {
	dir, transcriptID, ok := claudeSubagentDirectory(session.Transcript)
	if !ok {
		return []surface.ClaudeSubagentLink{}, nil
	}
	return o.observe(ctx, session.ID, transcriptID, dir)
}

// rollup counts the subagents a session row shows: those working or active
// within surface.ActiveWindow. The subagent list keeps every one.
func (o *claudeSubagentObserver) rollup(ctx context.Context, session *surface.Session) (*surface.SubagentRollup, error) {
	links, err := o.observeSession(ctx, session)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var rollup surface.SubagentRollup
	for _, link := range links {
		if link.Working {
			rollup.Working++
		} else if now.Sub(link.LastActive) > surface.ActiveWindow {
			continue
		}
		rollup.Count++
	}
	if rollup.Count == 0 {
		return nil, nil
	}
	return &rollup, nil
}

func (o *claudeSubagentObserver) observe(ctx context.Context, parentSessionID, transcriptID, dir string) ([]surface.ClaudeSubagentLink, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		o.mu.Lock()
		delete(o.dirs, dir)
		o.mu.Unlock()
		return []surface.ClaudeSubagentLink{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat Claude subagent directory %s: %w", dir, err)
	}
	o.mu.Lock()
	state := o.dirs[dir]
	if state == nil {
		state = &claudeSubagentDir{agents: map[string]*claudeSubagentFile{}}
		o.dirs[dir] = state
	}
	o.mu.Unlock()
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.listed || !surface.UnchangedSince(state.listedAt, state.modTime, info.ModTime()) {
		listedAt := time.Now()
		if err := state.list(dir); err != nil {
			return nil, err
		}
		state.modTime, state.listedAt = info.ModTime(), listedAt
		state.listed = true
	}
	now := o.now()
	links := make([]surface.ClaudeSubagentLink, 0, len(state.agents))
	for _, agent := range state.agents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := agent.refresh(ctx, transcriptID, now); err != nil {
			return nil, err
		}
		links = append(links, agent.link(parentSessionID, now))
	}
	sort.Slice(links, func(i, j int) bool {
		if !links[i].LastActive.Equal(links[j].LastActive) {
			return links[i].LastActive.After(links[j].LastActive)
		}
		return links[i].AgentID < links[j].AgentID
	})
	return links, nil
}

func (d *claudeSubagentDir) list(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read Claude subagent directory %s: %w", dir, err)
	}
	present := map[string]struct{}{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		agentID := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl")
		if agentID == "" {
			continue
		}
		present[agentID] = struct{}{}
		agent := d.agents[agentID]
		if agent == nil {
			agent = &claudeSubagentFile{path: filepath.Join(dir, name), agentID: agentID}
			d.agents[agentID] = agent
		}
		if !agent.hasMeta {
			agent.readMeta()
		}
	}
	for agentID := range d.agents {
		if _, found := present[agentID]; !found {
			delete(d.agents, agentID)
		}
	}
	return nil
}

func (f *claudeSubagentFile) readMeta() {
	data, err := os.ReadFile(strings.TrimSuffix(f.path, ".jsonl") + ".meta.json")
	if err != nil {
		return
	}
	var meta claudeSubagentMeta
	if json.Unmarshal(data, &meta) != nil {
		return
	}
	f.meta = meta
	f.hasMeta = true
}

func (f *claudeSubagentFile) refresh(ctx context.Context, transcriptID string, now time.Time) error {
	info, err := os.Stat(f.path)
	if err != nil {
		return fmt.Errorf("stat Claude subagent transcript %s: %w", f.path, err)
	}
	stamp := surface.Stamp(info)
	if !f.cursor.reset(info) && f.stamp.Equal(stamp) {
		return nil
	}
	if f.cursor.offset == 0 {
		f.validated, f.read, f.done = false, false, false
	}
	if !f.validated {
		if err := f.validate(transcriptID); err != nil {
			return err
		}
	}
	f.stamp = stamp
	if now.Sub(info.ModTime()) >= claudeSubagentStaleAfter {
		f.read = false
		return nil
	}
	if !f.read {
		lines, end, err := readRecentJSONLLines(ctx, f.path, 0, claudeSubagentTailBytes, maxClaudeTranscriptRecordBytes)
		if err != nil {
			return fmt.Errorf("read Claude subagent transcript %s: %w", f.path, err)
		}
		f.done = false
		for _, line := range lines {
			f.apply(line)
		}
		f.cursor.offset, f.read = end, true
		return nil
	}
	offset, err := scanAppendedJSONL(ctx, f.path, f.cursor.offset, maxClaudeTranscriptRecordBytes, func(line []byte) error {
		f.apply(line)
		return nil
	})
	f.cursor.offset = offset
	if err != nil {
		return fmt.Errorf("read Claude subagent transcript %s: %w", f.path, err)
	}
	return nil
}

func (f *claudeSubagentFile) validate(transcriptID string) error {
	err := findJSONL(f.path, func(line []byte) (bool, error) {
		var record claudeSubagentRecord
		if json.Unmarshal(line, &record) != nil || record.SessionID == "" || record.AgentID == "" {
			return false, nil
		}
		if record.SessionID != transcriptID || record.AgentID != f.agentID {
			return false, fmt.Errorf("Claude subagent transcript %s does not match its path", f.path)
		}
		f.validated = true
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("read Claude subagent transcript %s: %w", f.path, err)
	}
	if f.validated {
		return nil
	}
	return fmt.Errorf("Claude subagent transcript %s has no identifying record", f.path)
}

func (f *claudeSubagentFile) apply(line []byte) {
	var record claudeSubagentRecord
	if json.Unmarshal(line, &record) != nil {
		return
	}
	switch record.Type {
	case "assistant":
		f.done = record.Message.StopReason == "end_turn" || claudeTerminalInterruption(record.Message.StopReason)
	case "user":
		f.done = false
	}
}

func (f *claudeSubagentFile) link(parentSessionID string, now time.Time) surface.ClaudeSubagentLink {
	depth := f.meta.SpawnDepth
	if depth < 1 {
		depth = 1
	}
	link := surface.ClaudeSubagentLink{
		ParentSessionID: parentSessionID,
		AgentID:         f.agentID,
		AgentType:       f.meta.AgentType,
		Description:     f.meta.Description,
		ToolUseID:       f.meta.ToolUseID,
		Depth:           depth,
		TranscriptPath:  f.path,
	}
	if !f.stamp.Modified.IsZero() {
		link.LastActive = f.stamp.Modified
		link.Working = f.read && !f.done && now.Sub(link.LastActive) < claudeSubagentStaleAfter
	}
	return link
}

// ObserveAllClaudeSubagents lists every subagent on the host for the
// `agenthail runs` diagnostic. Each parent is keyed by its transcript ID.
func ObserveAllClaudeSubagents(ctx context.Context, home string) ([]surface.ClaudeSubagentLink, error) {
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home: %w", err)
		}
	}
	roots, err := surface.ClaudeConfigRoots(home)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, root := range roots {
		found, err := filepath.Glob(filepath.Join(root, "projects", "*", "*", "subagents"))
		if err != nil {
			return nil, fmt.Errorf("discover Claude subagent directories: %w", err)
		}
		dirs = append(dirs, found...)
	}
	sort.Strings(dirs)
	observer := newClaudeSubagentObserver()
	links := []surface.ClaudeSubagentLink{}
	for _, dir := range dirs {
		transcriptID := filepath.Base(filepath.Dir(dir))
		found, err := observer.observe(ctx, transcriptID, transcriptID, dir)
		if err != nil {
			return nil, err
		}
		links = append(links, found...)
	}
	return links, nil
}
