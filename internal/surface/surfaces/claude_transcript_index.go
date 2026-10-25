package surfaces

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Claude) transcriptPath(s *surface.Session) string {
	return c.resolveTranscript(s.ID)
}

// resolveTranscript returns the transcript Claude Code wrote for a
// conversation, or "" when none exists yet; callers resolve again later.
func (c *Claude) resolveTranscript(conversationID string) string {
	roots, err := surface.ClaudeConfigRoots(c.home)
	if err != nil {
		return ""
	}
	return onlyTranscript(c.transcripts.pass(roots)(conversationID))
}

func onlyTranscript(paths []string) string {
	if len(paths) != 1 {
		return ""
	}
	return paths[0]
}

// claudeTranscriptIndex maps conversation ids to transcripts. Claude Code
// writes every conversation to <root>/projects/<project>/<id>.jsonl, so the
// index is built from directory listings and never predicts project names.
// Each pass re-lists only project directories whose modification time
// changed, which is when Claude Code adds or removes a transcript, by
// surface.UnchangedSince.
type claudeTranscriptIndex struct {
	mu   sync.Mutex
	dirs map[string]claudeProjectListing
	byID map[string]map[string]struct{}
}

type claudeProjectListing struct {
	modified time.Time
	listed   time.Time
	ids      []string
}

func newClaudeTranscriptIndex() *claudeTranscriptIndex {
	return &claudeTranscriptIndex{dirs: map[string]claudeProjectListing{}, byID: map[string]map[string]struct{}{}}
}

// pass brings the index up to date across roots once and returns a lookup
// over that state, so a discovery pass lists each projects directory one
// time however many sessions it resolves. The lookup returns every
// transcript of an id; one in more than one project resolves to none.
func (x *claudeTranscriptIndex) pass(roots []string) func(conversationID string) []string {
	x.mu.Lock()
	x.refresh(roots)
	x.mu.Unlock()
	return func(conversationID string) []string {
		if conversationID == "" || strings.ContainsAny(conversationID, `/\`) {
			return nil
		}
		x.mu.Lock()
		defer x.mu.Unlock()
		var paths []string
		for dir := range x.byID[conversationID] {
			paths = append(paths, filepath.Join(dir, conversationID+".jsonl"))
		}
		sort.Strings(paths)
		return paths
	}
}

func (x *claudeTranscriptIndex) refresh(roots []string) {
	present := map[string]bool{}
	for _, root := range roots {
		projects := filepath.Join(root, "projects")
		entries, err := os.ReadDir(projects)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			dir := filepath.Join(projects, entry.Name())
			present[dir] = true
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if listing, ok := x.dirs[dir]; ok && surface.UnchangedSince(listing.listed, listing.modified, info.ModTime()) {
				continue
			}
			x.forget(dir)
			x.list(dir, info.ModTime())
		}
	}
	for dir := range x.dirs {
		if !present[dir] {
			x.forget(dir)
		}
	}
}

func (x *claudeTranscriptIndex) list(dir string, modified time.Time) {
	listed := time.Now()
	files, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	listing := claudeProjectListing{modified: modified, listed: listed}
	for _, file := range files {
		id, ok := strings.CutSuffix(file.Name(), ".jsonl")
		if !ok || file.IsDir() {
			continue
		}
		listing.ids = append(listing.ids, id)
		if x.byID[id] == nil {
			x.byID[id] = map[string]struct{}{}
		}
		x.byID[id][dir] = struct{}{}
	}
	x.dirs[dir] = listing
}

func (x *claudeTranscriptIndex) forget(dir string) {
	for _, id := range x.dirs[dir].ids {
		delete(x.byID[id], dir)
		if len(x.byID[id]) == 0 {
			delete(x.byID, id)
		}
	}
	delete(x.dirs, dir)
}

func (c *Claude) firstUserMessage(path string) string {
	if path == "" || !fileExists(path) {
		return ""
	}
	message := ""
	_ = findJSONL(path, func(line []byte) (bool, error) {
		var e struct {
			Type string `json:"type"`
			Msg  struct {
				Content any `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &e) != nil {
			return false, nil
		}
		if e.Type != "user" {
			return false, nil
		}
		text := ""
		switch c := e.Msg.Content.(type) {
		case string:
			text = c
		case []any:
			for _, item := range c {
				if m, ok := item.(map[string]any); ok {
					if t, _ := m["type"].(string); t == "text" {
						if s, _ := m["text"].(string); s != "" {
							text = s
							break
						}
					}
				}
			}
		}
		if IsTypedHumanPrompt(text) {
			message = surface.TruncateString(strings.TrimSpace(text), 60)
			return true, nil
		}
		return false, nil
	})
	return message
}
