package surfaces

import (
	"context"
	"path/filepath"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/surface"
)

const claudeCommandsUnavailable = "Commands run only when this session has Remote Control."

// A folder the host has not listed yet is listed in the background; a later
// stream pass carries it.
const claudeCommandsStreamWait = 250 * time.Millisecond

// Only a person's turn runs a command: the native peer socket other senders
// take refuses a message that starts with a slash.
func (c *Claude) Commands(ctx context.Context, sess *surface.Session) (*surface.RuntimeCommands, error) {
	if sess == nil || !c.canSendHumanTurn(sess) {
		return &surface.RuntimeCommands{Commands: []surface.RuntimeCommand{}, Unavailable: claudeCommandsUnavailable}, nil
	}
	if claudeHosted(sess) {
		if listed, ok := c.host.session(ctx, sess.ID, sess.PID); ok && listed.Commands != nil {
			return claudeRuntimeCommands(listed.Commands), nil
		}
	}
	if sess.Cwd == "" || c.host == nil {
		return &surface.RuntimeCommands{Commands: []surface.RuntimeCommand{}}, nil
	}
	configDir, err := c.listingConfigDir(sess)
	if err != nil {
		return nil, err
	}
	listing, err := c.host.client.ListCommands(ctx, sess.Cwd, configDir)
	if err != nil {
		return nil, err
	}
	return claudeRuntimeCommands(listing.Commands), nil
}

func (c *Claude) listingConfigDir(sess *surface.Session) (string, error) {
	root := ""
	if sess.Transcript != "" {
		root = claudeTranscriptRoot(sess.Transcript)
	} else {
		var err error
		if root, err = c.conversationRoot(sess.ID); err != nil {
			return "", err
		}
	}
	if root == "" || filepath.Clean(root) == filepath.Clean(surface.ClaudeConfigDir(c.home)) {
		return "", nil
	}
	return root, nil
}

func claudeRuntimeCommands(commands []claudehost.Command) *surface.RuntimeCommands {
	out := make([]surface.RuntimeCommand, 0, len(commands))
	for _, command := range commands {
		out = append(out, surface.RuntimeCommand{Name: command.Name, Description: command.Description, ArgumentHint: command.ArgumentHint, Insert: "/" + command.Name})
	}
	return &surface.RuntimeCommands{Commands: out}
}

type claudeCommandsStream struct {
	sent    *surface.RuntimeCommands
	version uint64
}

func (s *claudeCommandsStream) emit(ctx context.Context, c *Claude, sess *surface.Session, onEvent func(surface.StreamEvent)) {
	waitCtx, cancel := context.WithTimeout(ctx, claudeCommandsStreamWait)
	commands, err := c.Commands(waitCtx, sess)
	cancel()
	if err != nil || commands == nil || sameRuntimeCommands(s.sent, commands) {
		return
	}
	s.sent = commands
	s.version++
	onEvent(surface.StreamEvent{ID: "commands", ProviderKey: "commands", Version: s.version, Operation: "replace", Kind: "commands", Commands: commands})
}

func sameRuntimeCommands(a, b *surface.RuntimeCommands) bool {
	if a == nil || b == nil || a.Unavailable != b.Unavailable || len(a.Commands) != len(b.Commands) {
		return false
	}
	for i := range a.Commands {
		if a.Commands[i] != b.Commands[i] {
			return false
		}
	}
	return true
}
