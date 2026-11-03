package surfaces

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

// ConversationState counts a process that holds the conversation, because a
// conversation that just started has not written its transcript yet.
func (c *Claude) ConversationState(ctx context.Context, sess *surface.Session) (surface.ConversationState, error) {
	roots, err := surface.ClaudeConfigRoots(c.home)
	if err != nil {
		return surface.ConversationUnknown, err
	}
	transcripts, err := claudeTranscriptsOnDisk(roots, sess.ID)
	if err != nil {
		return surface.ConversationUnknown, err
	}
	if len(transcripts) > 1 {
		return surface.ConversationUnknown, fmt.Errorf("Claude conversation %s has %d transcripts (%s)", sess.ID, len(transcripts), strings.Join(transcripts, ", "))
	}
	state, err := c.conversation(ctx, sess.ID, claudePin{})
	if state.alive {
		return surface.ConversationLive, nil
	}
	if err != nil {
		return surface.ConversationUnknown, err
	}
	if len(transcripts) == 1 {
		return surface.ConversationResumable, nil
	}
	return surface.ConversationGone, nil
}

func claudeTranscriptsOnDisk(roots []string, conversationID string) ([]string, error) {
	if conversationID == "" || strings.ContainsAny(conversationID, `/\`) || conversationID == "." || conversationID == ".." {
		return nil, fmt.Errorf("Claude conversation id %q is not a transcript name", conversationID)
	}
	var found []string
	for _, root := range roots {
		projects := filepath.Join(root, "projects")
		entries, err := os.ReadDir(projects)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read Claude projects in %s: %w", root, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(projects, entry.Name(), conversationID+".jsonl")
			if _, err := os.Stat(path); err == nil {
				found = append(found, path)
			} else if !os.IsNotExist(err) {
				return nil, fmt.Errorf("read Claude transcript %s: %w", path, err)
			}
		}
	}
	return found, nil
}
