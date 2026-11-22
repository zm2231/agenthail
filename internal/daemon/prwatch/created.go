package prwatch

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/github"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const createdPRReadTimeout = 30 * time.Second

var shellTools = []string{"Bash", "exec_command", "shell", "local_shell"}

// LinkCreated links the pull requests a session's newest turn created with a
// shell command.
func (w *Watcher) LinkCreated(ctx context.Context, adapter surface.Surface, session surface.Session) {
	ctx, cancel := context.WithTimeout(ctx, createdPRReadTimeout)
	defer cancel()
	items, err := w.host.LatestTimeline(ctx, &session, adapter, createdPRReadTimeout)
	if err != nil {
		w.log.Printf("pull requests created by %s: %s", w.host.Display(session.ID), err)
		return
	}
	for _, ref := range w.createdPRs(session.ID, items) {
		if err := w.Link(registry.SessionPR{SessionID: session.ID, Repo: ref.Repo.String(), Number: ref.Number, URL: ref.URL(), Source: registry.PRSourceCreated}, false); err != nil {
			w.log.Printf("link %s to %s: %s", ref.URL(), w.host.Display(session.ID), err)
		}
	}
}

func (w *Watcher) createdPRs(sessionID string, items []surface.TimelineItem) []github.Ref {
	commands := map[string]string{}
	var refs []github.Ref
	for _, item := range items {
		switch item.Kind {
		case "toolCall":
			if !slices.Contains(shellTools, item.Title) || !item.Truncated && !strings.Contains(item.Text, "pr create") {
				continue
			}
			if command := shellCommand(w.host.TimelineText(sessionID, item)); strings.Contains(command, "pr create") {
				commands[item.CallID] = command
			}
		case "toolResult":
			if command, ok := commands[item.CallID]; ok && item.Status != "error" {
				refs = append(refs, github.CreatedBy(command, w.host.TimelineText(sessionID, item))...)
			}
		}
	}
	return refs
}

func shellCommand(input string) string {
	var fields struct {
		Command json.RawMessage `json:"command"`
		Cmd     json.RawMessage `json:"cmd"`
	}
	if json.Unmarshal([]byte(input), &fields) != nil {
		return ""
	}
	for _, raw := range []json.RawMessage{fields.Command, fields.Cmd} {
		var text string
		if json.Unmarshal(raw, &text) == nil && text != "" {
			return text
		}
		var parts []string
		if json.Unmarshal(raw, &parts) == nil && len(parts) > 0 {
			return strings.Join(parts, " ")
		}
	}
	return ""
}
