package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type codexBackgroundState struct {
	cursor transcriptCursor
	window surface.CodexToolWindow
	turnID string
	read   bool
}

// Background processes live in the app-server that has the thread loaded.
func (c *Codex) codexBackground(ctx context.Context, threadID, path string, info os.FileInfo) *surface.BackgroundTasks {
	commands := c.backgroundCommands(ctx, path, info)
	if len(commands) == 0 || !c.threadLoaded(threadID) {
		return nil
	}
	tasks := make([]surface.BackgroundTask, 0, len(commands))
	for _, command := range commands {
		since, _ := time.Parse(time.RFC3339Nano, command.At)
		tasks = append(tasks, surface.BackgroundTask{ID: command.CallID, Kind: surface.BackgroundShell, Description: codexCommandLine(command.Command), Since: since.UTC()})
	}
	return backgroundOf(tasks, surface.BackgroundSourceTranscript)
}

func (c *Codex) backgroundCommands(ctx context.Context, path string, info os.FileInfo) []surface.CodexBackgroundCommand {
	c.backgroundMu.Lock()
	defer c.backgroundMu.Unlock()
	if c.backgroundState == nil {
		c.backgroundState = map[string]*codexBackgroundState{}
	}
	state := c.backgroundState[path]
	reset := state == nil || state.cursor.reset(info)
	if reset {
		state = &codexBackgroundState{}
		state.cursor.reset(info)
		lines, offset, err := readRecentJSONLLines(ctx, path, 0, codexPlanFirstReadBytes, maxCodexTranscriptRecordBytes)
		if err != nil {
			delete(c.backgroundState, path)
			return nil
		}
		for _, line := range lines {
			state.apply(line)
		}
		state.cursor.offset = offset
		c.backgroundState[path] = state
	}
	if info.Size() > state.cursor.offset {
		offset, err := scanAppendedJSONL(ctx, path, state.cursor.offset, maxCodexTranscriptRecordBytes, func(line []byte) error {
			state.apply(line)
			return nil
		})
		if err == nil {
			state.cursor.offset = offset
		}
	}
	state.read = true
	return append([]surface.CodexBackgroundCommand(nil), state.window.Background...)
}

func (s *codexBackgroundState) apply(line []byte) {
	var record map[string]any
	if json.Unmarshal(line, &record) != nil {
		return
	}
	if turnID := codexRecordTurnID(record); turnID != "" {
		s.turnID = turnID
	}
	codexRecordItems(&s.window, record, s.turnID)
}

func (c *Codex) forgetUnreadBackground() {
	c.backgroundMu.Lock()
	defer c.backgroundMu.Unlock()
	for path, state := range c.backgroundState {
		if !state.read {
			delete(c.backgroundState, path)
			continue
		}
		state.read = false
	}
}

// A runtime whose loaded threads could not be listed keeps its last listing.
func (c *Codex) noteLoaded(runtimes *codexRuntimes) {
	c.backgroundMu.Lock()
	defer c.backgroundMu.Unlock()
	if c.loadedThreads == nil {
		c.loadedThreads = map[string]map[string]bool{}
	}
	for _, runtime := range []struct {
		name   string
		client codexClient
		loaded map[string]bool
	}{{codexTransportDesktop, runtimes.desktop, runtimes.desktopLoaded}, {codexTransportManaged, runtimes.managed, runtimes.managedLoaded}} {
		switch {
		case runtime.client == nil:
			delete(c.loadedThreads, runtime.name)
		case runtime.loaded != nil:
			c.loadedThreads[runtime.name] = runtime.loaded
		}
	}
}

func (c *Codex) threadLoaded(threadID string) bool {
	c.backgroundMu.Lock()
	defer c.backgroundMu.Unlock()
	for _, loaded := range c.loadedThreads {
		if loaded[threadID] {
			return true
		}
	}
	return false
}
