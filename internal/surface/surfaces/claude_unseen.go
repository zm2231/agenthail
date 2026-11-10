package surfaces

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type claudeEntrypoint struct {
	kind      string
	cwd       string
	sidechain bool
	size      int64
	modified  time.Time
}

func (e claudeEntrypoint) listable() bool {
	return e.kind != "" && !strings.HasPrefix(e.kind, "sdk-") && !e.sidechain && e.cwd != ""
}

const claudeEntrypointLineLimit = 16 << 20

// Claude Code never rewrites a transcript's first entrypoint, so a found one is read once per path.
type claudeEntrypoints struct {
	mu     sync.Mutex
	byPath map[string]claudeEntrypoint
	open   func(string) (io.ReadCloser, error)
}

func newClaudeEntrypoints() *claudeEntrypoints {
	return &claudeEntrypoints{byPath: map[string]claudeEntrypoint{}, open: func(path string) (io.ReadCloser, error) { return os.Open(path) }}
}

func (x *claudeEntrypoints) of(path string, info os.FileInfo) claudeEntrypoint {
	x.mu.Lock()
	cached, found := x.byPath[path]
	x.mu.Unlock()
	if found && (cached.kind != "" || cached.size == info.Size() && cached.modified.Equal(info.ModTime())) {
		return cached
	}
	read := x.read(path)
	read.size, read.modified = info.Size(), info.ModTime()
	x.mu.Lock()
	x.byPath[path] = read
	x.mu.Unlock()
	return read
}

func (x *claudeEntrypoints) read(path string) claudeEntrypoint {
	file, err := x.open(path)
	if err != nil {
		return claudeEntrypoint{}
	}
	defer file.Close()
	marker := []byte(`"entrypoint"`)
	reader := bufio.NewReaderSize(file, 64<<10)
	var line []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		if len(line)+len(chunk) > claudeEntrypointLineLimit {
			line = line[:0]
		}
		line = append(line, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		whole := line
		line = line[:0]
		if bytes.Contains(whole, marker) {
			var record struct {
				Entrypoint  string `json:"entrypoint"`
				Cwd         string `json:"cwd"`
				IsSidechain bool   `json:"isSidechain"`
			}
			if json.Unmarshal(whole, &record) == nil && record.Entrypoint != "" {
				return claudeEntrypoint{kind: record.Entrypoint, cwd: record.Cwd, sidechain: record.IsSidechain}
			}
		}
		if err != nil {
			return claudeEntrypoint{}
		}
	}
}

func (c *Claude) UnseenConversations(known func(id string) bool) []surface.Session {
	roots, err := surface.ClaudeConfigRoots(c.home)
	if err != nil {
		return nil
	}
	lookup := c.transcripts.pass(roots)
	var out []surface.Session
	for _, id := range c.transcripts.ids() {
		if known(id) {
			continue
		}
		path := onlyTranscript(lookup(id))
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		entry := c.entrypoints.of(path, info)
		if !entry.listable() {
			continue
		}
		out = append(out, surface.Session{
			ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI,
			Name: c.firstUserMessage(path), Cwd: entry.cwd, Status: surface.StatusOffline, Ended: true,
			Transcript: path, HasLocal: true, LastActive: info.ModTime(),
		})
	}
	return out
}
