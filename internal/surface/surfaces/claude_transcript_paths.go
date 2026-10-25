package surfaces

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// conversationRoot returns the config root that holds a conversation's
// session records and transcripts, or "" when no root holds it yet. A
// conversation found in two roots is refused rather than guessed.
func (c *Claude) conversationRoot(conversationID string) (string, error) {
	roots, records, err := c.records()
	if err != nil {
		return "", err
	}
	found := map[string]bool{}
	for _, record := range records {
		if str(record.fields, "sessionId") == conversationID {
			found[record.root] = true
		}
	}
	return claudeHoldingRoot(conversationID, roots, found, c.transcripts.pass(roots)(conversationID))
}

func claudeHoldingRoot(conversationID string, roots []string, found map[string]bool, transcripts []string) (string, error) {
	for _, path := range transcripts {
		found[claudeTranscriptRoot(path)] = true
	}
	var holding []string
	for _, root := range roots {
		if found[root] {
			holding = append(holding, root)
		}
	}
	if len(holding) > 1 {
		return "", surface.DeliveryTerminal(fmt.Errorf("Claude conversation %s exists in more than one Claude config directory (%s); remove it from all but one", conversationID, strings.Join(holding, ", ")), surface.DeliveryInvalidRequest)
	}
	if len(holding) == 0 {
		return "", nil
	}
	return holding[0], nil
}

func claudeTranscriptRoot(path string) string {
	return filepath.Dir(filepath.Dir(filepath.Dir(path)))
}

// sessionTranscript returns the transcript a read of sess uses, refusing a
// conversation whose transcripts lie in more than one config root.
func (c *Claude) sessionTranscript(sess *surface.Session) (string, error) {
	roots, err := surface.ClaudeConfigRoots(c.home)
	if err != nil {
		return "", err
	}
	paths := c.transcripts.pass(roots)(sess.ID)
	if _, err := claudeHoldingRoot(sess.ID, roots, map[string]bool{}, paths); err != nil {
		return "", err
	}
	if sess.Transcript != "" {
		return sess.Transcript, nil
	}
	return onlyTranscript(paths), nil
}

// claudeRootTranscript returns a conversation's one transcript when it lies
// under root.
func claudeRootTranscript(paths []string, root string) string {
	path := onlyTranscript(paths)
	if path == "" || claudeTranscriptRoot(path) != root {
		return ""
	}
	return path
}

// awaitHostTranscript waits for a hosted session's first transcript write.
// Claude creates the file when the first turn starts, in a project folder it
// names itself. Each tick checks the folder Claude usually picks for the
// session's working directory, which costs a stat per config root; a lookup
// across every project folder, which costs a directory listing of each, runs
// when that file appears and every few seconds otherwise, so a transcript in
// a folder named some other way is still found.
func (c *Claude) awaitHostTranscript(ctx context.Context, sess *surface.Session, timeout time.Duration) (string, error) {
	roots, err := surface.ClaudeConfigRoots(c.home)
	if err != nil {
		return "", err
	}
	var likely []string
	if sess.Cwd != "" && claudeConversationID(sess.ID) {
		for _, root := range roots {
			likely = append(likely, filepath.Join(root, "projects", claudeProjectDirName(sess.Cwd), sess.ID+".jsonl"))
		}
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(claudeHostTranscriptTick)
	defer tick.Stop()
	probe := *sess
	probe.Transcript = ""
	for ticks := 0; ; ticks++ {
		if ticks%claudeHostTranscriptLookupTicks == 0 || slices.ContainsFunc(likely, fileExists) {
			path, err := c.sessionTranscript(&probe)
			if err != nil {
				return "", err
			}
			if path != "" && fileExists(path) {
				return path, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", surface.ErrStreamWindow
		case <-tick.C:
		}
	}
}

const claudeHostTranscriptTick = 250 * time.Millisecond

var claudeHostTranscriptLookupTicks = 8
