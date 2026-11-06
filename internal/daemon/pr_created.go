package daemon

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

func (d *Daemon) linkCreatedPRs(ctx context.Context, adapter surface.Surface, session surface.Session) {
	ctx, cancel := context.WithTimeout(ctx, createdPRReadTimeout)
	defer cancel()
	read, _, err := d.readSessionPage(ctx, &session, adapter, 0, 1, createdPRReadTimeout, true)
	if err != nil {
		d.log.Printf("pull requests created by %s: %s", d.resolveDisplay(session.ID), err)
		return
	}
	for _, ref := range d.createdPRs(session.ID, read.Items) {
		if _, err := d.changePRs(func(r *registry.Registry) (bool, error) {
			return r.LinkPR(registry.SessionPR{SessionID: session.ID, Repo: ref.Repo.String(), Number: ref.Number, URL: ref.URL(), Source: registry.PRSourceCreated}, false, time.Now())
		}); err != nil {
			d.log.Printf("link %s to %s: %s", ref.URL(), d.resolveDisplay(session.ID), err)
		}
	}
}

func (d *Daemon) createdPRs(sessionID string, items []surface.TimelineItem) []github.Ref {
	commands := map[string]string{}
	var refs []github.Ref
	for _, item := range items {
		switch item.Kind {
		case "toolCall":
			if !slices.Contains(shellTools, item.Title) || !item.Truncated && !strings.Contains(item.Text, "pr create") {
				continue
			}
			if command := shellCommand(d.itemText(sessionID, item)); strings.Contains(command, "pr create") {
				commands[item.CallID] = command
			}
		case "toolResult":
			if command, ok := commands[item.CallID]; ok && item.Status != "error" {
				refs = append(refs, github.CreatedBy(command, d.itemText(sessionID, item))...)
			}
		}
	}
	return refs
}

func (d *Daemon) itemText(sessionID string, item surface.TimelineItem) string {
	if !item.Truncated || item.BodyRef == "" {
		return item.Text
	}
	body, _, err := d.Registry.SessionJournalBody(sessionID, item.BodyRef, 0, sessionJournalRetentionBytes)
	if err != nil {
		return item.Text
	}
	return string(body)
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
